package chat

import (
	"context"
	"strings"

	"github.com/schneik80/fusionlocalserver/internal/sse"
)

// Chat's SSE fan-out. The ring buffer, replay, reset and slow-subscriber
// policy live in internal/sse, shared with the other features that push; what
// stays here is the part that is chat's alone — who may SEE an event.
//
// That split is deliberate. The hub has never made an authorization decision:
// Vis rides through it opaquely and Entitled below is the only reader. Keeping
// it that way means chat's visibility rules stay reviewable in one file rather
// than diffused into shared machinery.

// Event is the wire envelope for every chat SSE event (design doc §3).
type Event = sse.Event

// Frame is one rendered SSE event carrying the visibility its writer must
// enforce (via Hub.Entitled) before writing it to a particular subscriber.
type Frame = sse.Frame[Vis]

// Subscriber is one open event stream.
type Subscriber = sse.Subscriber[Vis]

// Vis controls which of a project's subscribers may see an event. The zero
// value is public (every project subscriber). Private events carry a
// snapshot of the channel whose ACL governs them; ExtraUserIDs are always
// delivered regardless of the ACL (e.g. a just-removed member learning of
// their removal). UserOnly events reach ONLY the ExtraUserIDs — no channel
// or role fallback — which is how per-user events (read.updated syncing a
// user's own tabs) stay invisible to everyone else.
type Vis struct {
	Private      bool
	Channel      Channel
	ExtraUserIDs []string
	UserOnly     bool
}

// Hub is chat's fan-out: the shared SSE hub plus chat's entitlement rule.
type Hub struct {
	*sse.Hub[Vis]
	authz *Authorizer
}

// NewHub wires a Hub to the authorizer (for per-subscriber visibility checks)
// and an epoch source (Store.EventEpoch). The ring is tuned for chat's traffic
// — a handful of events a minute, and a reconnect after a laptop lid closes
// should still replay rather than resync.
func NewHub(authz *Authorizer, epoch func(projectID string) (int64, error)) *Hub {
	return &Hub{Hub: sse.NewHub[Vis](epoch, sse.DefaultConfig()), authz: authz}
}

// Entitled reports whether a subscriber identified by (token, id) may see
// the frame: public frames always, private frames per the channel's
// two-layer rule, ExtraUserIDs unconditionally (matched on user id, or on
// email for sessions predating the Sub claim), UserOnly frames exclusively
// so. May fetch the roster when the authorizer's cache has lapsed — call
// it from the subscriber's own writer goroutine, never from Publish.
func (h *Hub) Entitled(ctx context.Context, token string, id Identity, projectID string, f Frame) (bool, error) {
	for _, u := range f.Vis.ExtraUserIDs {
		if u == "" {
			continue
		}
		if u == id.UserID || (id.Email != "" && strings.EqualFold(u, id.Email)) {
			return true, nil
		}
	}
	if f.Vis.UserOnly {
		return false, nil
	}
	if !f.Vis.Private {
		return true, nil
	}
	return h.authz.CanAccessChannel(ctx, token, id, projectID, f.Vis.Channel)
}
