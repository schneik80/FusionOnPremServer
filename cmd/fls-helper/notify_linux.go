//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// notify shows a desktop notification, falling back to stderr. There is no
// browser tab to report into — the launch happened through the OS — so this is
// the only channel the user will see if they are watching the desktop rather
// than the page.
func notify(title, body string) {
	if bin, err := exec.LookPath("notify-send"); err == nil {
		// --app-name keeps it attributable in notification history; the body is
		// passed as a separate argument, never interpolated into a shell.
		if exec.Command(bin, "--app-name=fls-helper", title, body).Run() == nil {
			return
		}
	}
	if bin, err := exec.LookPath("zenity"); err == nil {
		if exec.Command(bin, "--warning", "--title", title, "--text", body).Run() == nil {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "%s\n\n%s\n", title, body)
}
