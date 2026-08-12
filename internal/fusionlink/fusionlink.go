// Package fusionlink defines the contract between the server and the helper
// app: the custom URL scheme the browser launches, and the stable error codes
// the helper reports back.
//
// It exists so the two sides cannot drift. The server builds a URL the helper
// parses, and the helper reports a code the server stores and the SPA
// localizes — three components, one definition each, in one place.
//
// The URL is deliberately thin:
//
//	fusionlocal://v1/open?ticket=<id>&server=https%3A%2F%2Fhost%3A8080
//
// It carries no document id, no project, and nothing about the user. A URL
// scheme invocation is visible to the OS and to any other handler registered
// for it, and any web page can navigate to one — so the URL is a pointer to a
// grant, never the grant itself. The helper redeems the ticket over HTTPS
// against a server it has been paired with, and everything real travels there.
//
// "v1" is a version segment, not a host. If the payload ever needs to change
// shape, an older helper sees an unknown version and can say so instead of
// misreading it.
package fusionlink

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Scheme is the URL scheme registered with the OS by the helper app.
//
// Deliberately NOT "fls": that prefix is already the app's internal card-token
// namespace (fls:doc, fls:task, …) which appears inside chat and wiki bodies.
// Registering it with the OS would mean a stray token in a message could
// launch a native app.
const Scheme = "fusionlocal"

// Version is the path segment following the scheme.
const Version = "v1"

// Actions the helper knows how to perform.
const (
	ActionOpen   = "open"
	ActionInsert = "insert"
)

// ValidAction reports whether a is an action the helper implements.
func ValidAction(a string) bool { return a == ActionOpen || a == ActionInsert }

// Outcome codes. These are stable tokens: the helper reports one, the server
// stores it, and the SPA maps it through its errors catalog. Anything the
// helper cannot classify becomes CodeFailed, which renders as a generic
// failure carrying no detail.
const (
	// CodeNotRunning — nothing answered on the Fusion MCP endpoint.
	CodeNotRunning = "fusion_not_running"
	// CodeWrongHub — Fusion is running but signed in to a different hub, so
	// the document is not visible to it.
	CodeWrongHub = "fusion_wrong_hub"
	// CodeNoActiveDesign — insert was asked for with no design open.
	CodeNoActiveDesign = "fusion_no_active_design"
	// CodeFailed — Fusion refused for a reason we cannot name precisely.
	CodeFailed = "fusion_failed"
)

// ValidCode reports whether c is a code this build defines. The callback
// endpoint uses it to reject anything else rather than storing an arbitrary
// string reported by an unauthenticated caller.
func ValidCode(c string) bool {
	switch c {
	case CodeNotRunning, CodeWrongHub, CodeNoActiveDesign, CodeFailed:
		return true
	}
	return false
}

// ErrNotFusionLink is returned by Parse for input that isn't one of our URLs.
var ErrNotFusionLink = errors.New("fusionlink: not a " + Scheme + " URL")

// Link is a parsed launch URL.
type Link struct {
	Action string
	Ticket string
	// Server is the origin (scheme://host[:port]) the ticket is redeemed
	// against. The helper checks it against its pairing before using it.
	Server string
}

// BuildURL renders the URL handed to the OS.
func BuildURL(action, ticket, serverOrigin string) string {
	var b strings.Builder
	b.WriteString(Scheme)
	b.WriteString("://")
	b.WriteString(Version)
	b.WriteString("/")
	b.WriteString(action)
	b.WriteString("?ticket=")
	b.WriteString(url.QueryEscape(ticket))
	if serverOrigin != "" {
		b.WriteString("&server=")
		b.WriteString(url.QueryEscape(serverOrigin))
	}
	return b.String()
}

// Parse reads a launch URL. It validates shape only — that the scheme, version
// and action are ones this build knows, and that a ticket is present. Whether
// the server may be talked to is the caller's decision (see the helper's
// pairing check), because that is an authorization question, not a parsing one.
func Parse(raw string) (Link, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Link{}, fmt.Errorf("%w: %v", ErrNotFusionLink, err)
	}
	if !strings.EqualFold(u.Scheme, Scheme) {
		return Link{}, fmt.Errorf("%w: scheme %q", ErrNotFusionLink, u.Scheme)
	}
	// "fusionlocal://v1/open" parses the version as the host and the action as
	// the path; accept the authority-less "fusionlocal:v1/open" spelling too,
	// since some OS handlers normalize one into the other.
	version, action := u.Host, strings.TrimPrefix(u.Path, "/")
	if version == "" {
		version, action, _ = strings.Cut(strings.TrimPrefix(u.Opaque, "/"), "/")
	}
	if version != Version {
		return Link{}, fmt.Errorf("%w: unsupported version %q", ErrNotFusionLink, version)
	}
	if !ValidAction(action) {
		return Link{}, fmt.Errorf("%w: unknown action %q", ErrNotFusionLink, action)
	}
	q := u.Query()
	ticket := q.Get("ticket")
	if ticket == "" {
		return Link{}, fmt.Errorf("%w: no ticket", ErrNotFusionLink)
	}
	return Link{Action: action, Ticket: ticket, Server: q.Get("server")}, nil
}

// NormalizeOrigin reduces a server URL to a bare scheme://host[:port] for
// comparison, so a pairing recorded as "https://host:8080/" matches a launch
// URL that carries "https://host:8080". Returns "" for anything that isn't an
// absolute http(s) URL — which the caller must treat as a refusal, not as a
// wildcard.
func NormalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + strings.ToLower(u.Host)
}
