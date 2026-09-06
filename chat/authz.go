package chat

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
	"golang.org/x/sync/singleflight"
)

// Capability is one thing a user may do in a project's chat. Capabilities
// derive from the caller's APS project role (docs/chat/PLAN.md §Decisions):
// Viewer/Reader read; Editor and up post, react, edit their own messages,
// and create channels; Manager/Administrator moderate (rename/archive
// channels, manage private-channel members, delete others' messages).
type Capability int

const (
	CapRead Capability = iota
	CapPost
	CapReact
	CapEditOwn
	CapCreateChannel
	CapModerate
)

// Role ranks. An unknown role string ranks below rankRead and can do
// nothing — deny-by-default for any FolderRoleEnum value this build
// doesn't recognize.
const (
	rankNone = iota
	rankRead
	rankWrite
	rankModerate
)

// roleRank maps APS FolderRoleEnum values (uppercase wire strings) to
// capability ranks. Matching is case-insensitive to be resilient to casing
// drift in the GraphQL schema.
var roleRank = map[string]int{
	"VIEWER":        rankRead,
	"READER":        rankRead,
	"EDITOR":        rankWrite,
	"MANAGER":       rankModerate,
	"ADMINISTRATOR": rankModerate,
}

// capRank is the minimum rank each capability requires.
var capRank = map[Capability]int{
	CapRead:          rankRead,
	CapPost:          rankWrite,
	CapReact:         rankWrite,
	CapEditOwn:       rankWrite,
	CapCreateChannel: rankWrite,
	CapModerate:      rankModerate,
}

// groupDerivedRank is what a caller gets when the roster fetch made with
// their OWN token succeeds but doesn't list them as an individual member —
// i.e. their access to the project comes through a project GROUP.
// folderLevelProjectMembers enumerates only direct contributors, and group
// membership can't be expanded without hub-admin, so we can't read such a
// caller's exact role. Rather than lock them out (they demonstrably have
// project access — the same "your token sees it or it doesn't" trust the
// Dashboard and Wiki tabs already run on), grant contributor capabilities
// (read, post, react, edit own, create channels) but never moderation,
// whose scope (deleting others' messages, renaming/archiving channels,
// managing private ACLs) demands a confirmed Manager/Administrator role.
// Resolves docs/chat/PLAN.md open question 3.
const groupDerivedRank = rankWrite

// Authorizer answers "may this user do X in this project's chat" by
// fetching the project roster with the caller's own APS token
// (api.GetProjectMembers — readable by any project member, no hub-admin
// needed) and caching the resolved entry per (user, project). Entries for
// individually-listed members live for ttl; entries resolved by fallback
// (not individually listed → group-derived) live for negTTL, so a change in
// a caller's standing is picked up within negTTL at worst.
type Authorizer struct {
	fetch  func(ctx context.Context, token, projectID string) ([]api.Member, error)
	ttl    time.Duration
	negTTL time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]roleEntry
	sf    singleflight.Group
}

// roleEntry is a resolved roster lookup. listed records whether the identity
// appeared as an individual member row at all (any status); active narrows
// that to status ACTIVE; role is the uppercase FolderRoleEnum, set only when
// active. A caller who is not listed (but whose fetch succeeded) is
// group-derived — see groupDerivedRank.
type roleEntry struct {
	role   string
	listed bool
	active bool
	at     time.Time
}

// rankOf turns a resolved entry into a capability rank. Listed-but-inactive
// (PENDING/INACTIVE) and listed-with-an-unrecognized-role both deny;
// not-listed falls through to group-derived contributor access.
func rankOf(e roleEntry) int {
	if e.listed {
		if !e.active {
			return rankNone // on the roster but suspended → deny
		}
		return roleRank[e.role] // known role → its tier; unknown → rankNone
	}
	return groupDerivedRank
}

// NewAuthorizer returns an Authorizer with production wiring and TTLs.
func NewAuthorizer() *Authorizer {
	return &Authorizer{
		// The authorizer IS the roster cache (ttl/negTTL below), so its own
		// reads bypass the GraphQL coalescing cache: a revocation must be seen
		// the moment this cache expires, not a coalescing TTL later.
		fetch: func(ctx context.Context, token, projectID string) ([]api.Member, error) {
			return api.GetProjectMembers(apsbudget.WithFresh(ctx), token, projectID)
		},
		ttl:    60 * time.Second,
		negTTL: 15 * time.Second,
		now:    time.Now,
		cache:  make(map[string]roleEntry),
	}
}

// SetTTLsForTesting overrides the cache lifetimes so tests can exercise
// expiry-driven behavior (revocation teardown) without waiting out the
// production TTLs. Production code MUST NOT call this.
func (a *Authorizer) SetTTLsForTesting(positive, negative time.Duration) {
	a.ttl, a.negTTL = positive, negative
}

// Identity is the caller as chat authorization sees them: the OIDC subject
// plus their email. The roster row is matched by UserID when the profile
// carried a sub, falling back to a case-insensitive email match (open
// question #1 in docs/chat/PLAN.md — the fallback keeps either ID space
// working).
type Identity struct {
	UserID string
	Email  string
}

// Can reports whether the identity holds cap in the project. The identity
// MUST be the authenticated caller whose token is passed — the group-derived
// fallback (rankOf) reasons "this token could read the roster, so this
// caller has project access," which is only sound for the token's owner. To
// test a THIRD party's membership (e.g. a private-channel invitee), use
// IsActiveMember, not Can.
func (a *Authorizer) Can(ctx context.Context, token string, id Identity, projectID string, cap Capability) (bool, error) {
	e, err := a.resolve(ctx, token, id, projectID)
	if err != nil {
		return false, err
	}
	return rankOf(e) >= capRank[cap], nil
}

// CanAccessChannel is the design doc's two-layer rule (§1): project-level
// read access first, then — for private channels — the channel ACL, with
// project moderators (Manager/Administrator) always allowed through. Like
// Can, this is a self-check: id must be the caller who owns token.
func (a *Authorizer) CanAccessChannel(ctx context.Context, token string, id Identity, projectID string, ch Channel) (bool, error) {
	e, err := a.resolve(ctx, token, id, projectID)
	if err != nil {
		return false, err
	}
	rank := rankOf(e)
	if rank < rankRead {
		return false, nil
	}
	if !ch.IsPrivate {
		return true, nil
	}
	if id.UserID != "" && memberIndex(ch.Members, id.UserID) >= 0 {
		return true, nil
	}
	return rank >= rankModerate, nil
}

// IsActiveMember reports whether id is listed as an ACTIVE individual member
// of the project — the STRICT check for validating a third party (e.g. a
// private-channel invitee), where the caller's own project access says
// nothing about the target's. Unlike Can it never applies the group-derived
// fallback, so a group-only user (who isn't individually listed) can't be
// confirmed this way and thus can't be added to a private channel's ACL
// until they hold a direct membership.
func (a *Authorizer) IsActiveMember(ctx context.Context, token string, id Identity, projectID string) (bool, error) {
	e, err := a.resolve(ctx, token, id, projectID)
	if err != nil {
		return false, err
	}
	return e.active, nil
}

// resolve looks the identity up in the project roster through the cache. A
// roster fetch error is returned as-is (and not cached) so the handler can
// map it with s.fail — a flaky APS response must not lock a user out for a
// TTL. A successful fetch that doesn't list the identity yields a zero-value
// (not listed) entry, which rankOf reads as group-derived access.
func (a *Authorizer) resolve(ctx context.Context, token string, id Identity, projectID string) (roleEntry, error) {
	key := id.UserID + "\x00" + strings.ToLower(id.Email) + "\x00" + projectID

	a.mu.Lock()
	if e, ok := a.cache[key]; ok {
		maxAge := a.ttl
		if !e.listed {
			maxAge = a.negTTL
		}
		if a.now().Sub(e.at) < maxAge {
			a.mu.Unlock()
			return e, nil
		}
	}
	a.mu.Unlock()

	v, err, _ := a.sf.Do(key, func() (any, error) {
		members, err := a.fetch(ctx, token, projectID)
		if err != nil {
			return roleEntry{}, err
		}
		e := roleEntry{at: a.now()}
		for _, m := range members {
			if !matchesMember(m, id) {
				continue
			}
			e.listed = true
			if strings.EqualFold(m.Status, "ACTIVE") {
				e.active = true
				e.role = strings.ToUpper(m.Role)
			}
			break
		}
		a.mu.Lock()
		a.cache[key] = e
		a.mu.Unlock()
		return e, nil
	})
	if err != nil {
		return roleEntry{}, err
	}
	return v.(roleEntry), nil
}

func matchesMember(m api.Member, id Identity) bool {
	if id.UserID != "" && m.UserID == id.UserID {
		return true
	}
	return id.Email != "" && strings.EqualFold(m.Email, id.Email)
}
