//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// MessageBox flags: an OK button with a warning icon, and MB_SETFOREGROUND |
// MB_TOPMOST so the dialog is actually seen — we were launched by a URL click,
// so nothing about this process is in front of the user.
const (
	mbOK            = 0x00000000
	mbIconWarning   = 0x00000030
	mbSetForeground = 0x00010000
	mbTopMost       = 0x00040000
)

// notify shows a native message box. There is no browser tab to report into —
// the launch happened through the OS — so this is the only channel the user
// sees if they are watching the desktop rather than the page.
func notify(title, body string) {
	titleUTF16, err := windows.UTF16PtrFromString(title)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n\n%s\n", title, body)
		return
	}
	bodyUTF16, err := windows.UTF16PtrFromString(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n\n%s\n", title, body)
		return
	}
	_, _ = windows.MessageBox(0, bodyUTF16, titleUTF16,
		mbOK|mbIconWarning|mbSetForeground|mbTopMost)
}
