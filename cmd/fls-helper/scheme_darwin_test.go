//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The applet is the one place this program generates code that another
// interpreter runs, and one of its two inputs is attacker-chosen: this_URL is
// whatever web page navigated to the scheme. So these tests are about the
// boundary, not about the feature.

func TestAppletSourceQuotesTheURLAtRuntime(t *testing.T) {
	src := appletSource("/usr/local/bin/fls-helper")

	// The URL must reach the shell through AppleScript's own quoting. A source
	// that interpolated it would be a shell injection from any web page.
	if !strings.Contains(src, "quoted form of this_URL") {
		t.Fatalf("the applet must pass the URL through `quoted form of`; got:\n%s", src)
	}
	if !strings.Contains(src, "on open location this_URL") {
		t.Fatalf("the applet must handle `open location` — argv never carries the URL on macOS; got:\n%s", src)
	}
}

// nastyPath exercises every character that matters: a backslash and a double
// quote must be escaped to keep the AppleScript literal intact, while a single
// quote must NOT be — it is not special inside a double-quoted literal, and
// `quoted form of` is what makes it safe for the shell at runtime.
const nastyPath = `/Users/o'brien/we"ird/bin\x/fls-helper`

func TestAppletSourceEscapesTheHelperPath(t *testing.T) {
	src := appletSource(nastyPath)
	want := `quoted form of "/Users/o'brien/we\"ird/bin\\x/fls-helper"`
	if !strings.Contains(src, want) {
		t.Fatalf("path not escaped as an AppleScript literal\nwant substring: %s\ngot:\n%s", want, src)
	}
}

// TestAppletSourceCompiles is the test that would have caught the original bug:
// it asks macOS itself whether the generated script is a valid URL handler,
// rather than trusting that it looks like one. It compiles the awkward path, so
// a regression in the escaping shows up as a compile failure here rather than as
// a registration that silently does nothing.
func TestAppletSourceCompiles(t *testing.T) {
	if _, err := exec.LookPath("osacompile"); err != nil {
		t.Skip("osacompile not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "applet.applescript")
	if err := os.WriteFile(src, []byte(appletSource(nastyPath)), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("osacompile", "-o", filepath.Join(dir, "Test.app"), src).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated AppleScript does not compile: %v\n%s", err, out)
	}
	// The compiled script is what Launch Services delivers the Apple Event to;
	// schemeRegistration treats its absence as a stale registration.
	if _, err := os.Stat(filepath.Join(dir, "Test.app", "Contents", "Resources", "Scripts", "main.scpt")); err != nil {
		t.Fatalf("compiled applet has no main.scpt: %v", err)
	}
}
