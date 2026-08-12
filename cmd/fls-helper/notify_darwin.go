//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// notify shows a native alert. There is no browser tab to report into — the
// launch happened through the OS — so this is the only channel the user sees
// if they are watching the desktop rather than the page.
func notify(title, body string) {
	if bin, err := exec.LookPath("osascript"); err == nil {
		// The message is built as an AppleScript string LITERAL and quoted;
		// osascript is invoked with argv, never through a shell. The text can
		// contain an unpaired-server address chosen by whoever built the URL,
		// so it must not be able to escape into script syntax.
		script := fmt.Sprintf(
			`display dialog %s with title %s buttons {"OK"} default button "OK" with icon caution`,
			appleScriptString(body), appleScriptString(title))
		if exec.Command(bin, "-e", script).Run() == nil {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "%s\n\n%s\n", title, body)
}

// appleScriptString renders a Go string as an AppleScript string literal.
func appleScriptString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", "")
	return `"` + r.Replace(s) + `"`
}
