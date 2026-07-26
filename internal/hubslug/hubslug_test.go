package hubslug

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSlug locks the sanitize behavior byte-for-byte to the historical
// pins.sanitizeHubID table (which delegates here and keeps its own copy of
// this test as a ratchet).
func TestSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"alnum passthrough", "abcXYZ123", "abcXYZ123"},
		{"empty becomes unset", "", "_unset"},
		{"URN colons replaced", "urn:adsk.ace:prod.scope:abc", "urn_adsk.ace_prod.scope_abc"},
		{"slashes replaced", "a/b/c", "a_b_c"},
		{"spaces and special chars replaced", "hello world! @#", "hello_world____"},
		{"dot/dash/underscore preserved", "ab.cd-ef_gh", "ab.cd-ef_gh"},
		{"capped at 120 chars", strings.Repeat("x", 200), strings.Repeat("x", 120)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Slug(tc.in); got != tc.want {
				t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestProfileDir(t *testing.T) {
	got := ProfileDir("/cfg", "urn:adsk.ace:prod.scope:abc")
	want := filepath.Join("/cfg", "hubs", "urn_adsk.ace_prod.scope_abc")
	if got != want {
		t.Errorf("ProfileDir = %q, want %q", got, want)
	}
}
