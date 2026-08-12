//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
)

// On Windows a URL scheme lives in the registry. Everything here is written
// under HKEY_CURRENT_USER\Software\Classes, which is per-user and needs no
// elevation — a machine-wide registration would require an installer running as
// administrator, for no benefit to a single-user desktop tool.

func schemeKeyPath() string { return `Software\Classes\` + fusionlink.Scheme }

func registerScheme() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating this program: %w", err)
	}

	root, _, err := registry.CreateKey(registry.CURRENT_USER, schemeKeyPath(), registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("creating the registry key: %w", err)
	}
	defer root.Close()
	if err := root.SetStringValue("", "URL:Fusion Local Server Helper"); err != nil {
		return err
	}
	// The presence of "URL Protocol" — not its value — is what marks a key as a
	// scheme handler.
	if err := root.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER,
		schemeKeyPath()+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("creating the command key: %w", err)
	}
	defer cmdKey.Close()
	// The quotes matter: without them a path containing spaces (which
	// %LOCALAPPDATA% paths routinely do) would be split into several arguments.
	return cmdKey.SetStringValue("", `"`+exe+`" "%1"`)
}

func unregisterScheme() error {
	// Delete the leaves first: DeleteKey refuses a key that still has subkeys.
	for _, sub := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ``} {
		err := registry.DeleteKey(registry.CURRENT_USER, schemeKeyPath()+sub)
		if err != nil && !strings.Contains(err.Error(), "cannot find") {
			return fmt.Errorf("removing %s%s: %w", schemeKeyPath(), sub, err)
		}
	}
	return nil
}

// schemeRegistration reports whether our handler is installed. The registry
// value is the command line the OS will run, so there is no separate question of
// whether delivery can work — but it can name a binary this one has moved away
// from, which is stale rather than absent.
func schemeRegistration() (string, schemeState, string) {
	where := `HKCU\` + schemeKeyPath()
	k, err := registry.OpenKey(registry.CURRENT_USER,
		schemeKeyPath()+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", schemeAbsent, ""
	}
	defer k.Close()
	cmd, _, err := k.GetStringValue("")
	if err != nil || cmd == "" {
		return "", schemeAbsent, ""
	}
	if exe, eerr := os.Executable(); eerr == nil && !strings.Contains(cmd, exe) {
		return where, schemeStale, "it dispatches to " + cmd + ", not to this binary at " + exe
	}
	return where, schemeGood, ""
}
