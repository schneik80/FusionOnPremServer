// Package hubslug maps APS hub ids to filesystem-safe slugs and locates the
// per-hub profile directory every local store roots under. The slug function
// was extracted verbatim from pins.sanitizeHubID (which now delegates here) so
// existing on-disk pins-<slug>.json names keep resolving byte-identically.
package hubslug

import (
	"path/filepath"
	"strings"
)

// Slug maps a hub ID to a filesystem-safe slug: any character outside
// [A-Za-z0-9_.\-] is replaced with '_'. The result is capped at 120 chars to
// stay well clear of per-platform path length limits. Byte-identical to the
// historical pins.sanitizeHubID.
func Slug(hubID string) string {
	if hubID == "" {
		return "_unset"
	}
	var b strings.Builder
	for _, r := range hubID {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// ProfileDir returns the hub's profile directory under the config dir:
// <configDir>/hubs/<slug>. Every hub-scoped store (chat, tasks, production,
// whiteboards, pins, hub.json, backup.json) lives inside it.
func ProfileDir(configDir, hubID string) string {
	return filepath.Join(configDir, "hubs", Slug(hubID))
}
