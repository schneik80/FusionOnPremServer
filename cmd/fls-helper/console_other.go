//go:build !windows

package main

// attachConsole is a no-op everywhere but Windows. macOS and Linux have no
// subsystem split: the same binary writes to a terminal when there is one and
// is simply silent when there isn't, with no window to flash either way.
func attachConsole() {}
