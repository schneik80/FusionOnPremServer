package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
)

// A launch has nowhere to print.
//
// The OS starts this program from a browser navigation: there is no terminal
// attached, and on macOS the AppleScript applet that receives the URL discards
// the child's stdout and stderr outright. So every diagnostic the launch path
// writes to stderr is written to nothing at all — which is how a macOS bundle
// that could never receive its URL passed as "registered" for as long as it did.
//
// This log is the fix for that class of bug rather than for one instance of it.
// One line per launch, in the same directory as helper.json, mode 0600.

const (
	// logMaxBytes is when the file gets trimmed. This is a breadcrumb trail,
	// not an audit log; a few dozen kilobytes covers far more history than
	// anyone will read.
	logMaxBytes = 64 << 10
	// logKeepLines survives a trim.
	logKeepLines = 200
)

// helperLogPath is <config>/helper.log, beside helper.json.
func helperLogPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "helper.log"), nil
}

// logLaunch appends one timestamped line. Every failure here is swallowed: this
// is a diagnostic, and a helper that refused to open Fusion because it could
// not write its own log would be a worse bug than the ones the log exists to
// find.
func logLaunch(format string, args ...any) {
	path, err := helperLogPath()
	if err != nil {
		return
	}
	trimLog(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	line := strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " ")
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), line)
}

// trimLog keeps the file bounded, rewriting it with only the most recent lines
// once it grows past logMaxBytes.
func trimLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= logMaxBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > logKeepLines {
		lines = lines[len(lines)-logKeepLines:]
	}
	_ = atomicfile.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

// recentLaunches returns the last n log lines, oldest first, for `status` to
// print. A missing file yields nothing, which is not an error — it just means
// no launch has happened yet.
func recentLaunches(n int) []string {
	path, err := helperLogPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// redactTicket shortens a ticket id for the log. It is single-use and expires
// in two minutes, but it is still the whole grant a launch carries, and a
// credential does not belong in a log file in full. A prefix is enough to
// correlate a launch with the server's own record of it.
func redactTicket(t string) string {
	if len(t) <= 6 {
		return "…"
	}
	return t[:6] + "…"
}
