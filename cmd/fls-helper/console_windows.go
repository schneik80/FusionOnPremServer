//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// The Windows helper is linked with -H windowsgui.
//
// It has to be. The OS starts this program for every Open and Insert click, and
// a console-subsystem binary flashes a black window each time — which, for an
// action whose whole job is to be invisible, reads as a malfunction.
//
// The cost is that a GUI-subsystem process starts with no standard handles at
// all, so `fls-helper status` from a terminal would print nothing into the
// void. attachConsole re-attaches to the console that launched us, when there
// is one, and rebinds stdout/stderr to it — restoring the CLI without
// reintroducing the flash.
//
// One quirk is inherent and cannot be fixed from here: a shell does not WAIT
// for a GUI-subsystem process, so output from `pair` / `register` / `status`
// arrives after the prompt has already returned. These are one-time setup
// commands, so that is tolerable; the alternative is shipping two binaries
// (the python / pythonw convention).

// attachParentProcess is ATTACH_PARENT_PROCESS (0xFFFFFFFF) — attach to the
// console of whatever started us, rather than allocating a new one.
const attachParentProcess = ^uint32(0)

var (
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
)

// attachConsole wires stdout/stderr to the launching terminal, if there is one.
// Every failure is silent and non-fatal: no console simply means we were
// started by the OS as a protocol handler, which is the common case.
func attachConsole() {
	r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess))
	if r == 0 {
		return // no parent console — launched from Explorer or a URL handler
	}
	// The standard handles inherited before the attach are not valid consoles,
	// so open the console device directly rather than trusting GetStdHandle.
	// stdout and stderr get separate handles so neither owns the other.
	if h, err := openConsoleOut(); err == nil {
		os.Stdout = os.NewFile(uintptr(h), "CONOUT$")
		_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, h)
	}
	if h, err := openConsoleOut(); err == nil {
		os.Stderr = os.NewFile(uintptr(h), "CONOUT$")
		_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, h)
	}
}

// openConsoleOut opens CONOUT$, the console's output device.
func openConsoleOut() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
}
