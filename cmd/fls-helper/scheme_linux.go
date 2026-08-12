//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
)

// On Linux a URL scheme handler is an ordinary .desktop entry declaring the
// x-scheme-handler MIME type. Everything is per-user under ~/.local/share, so
// nothing here needs root.

const desktopFileName = "fls-helper.desktop"

func desktopPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return filepath.Join(dir, desktopFileName), nil
}

func registerScheme() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating this program: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving this program's path: %w", err)
	}
	path, err := desktopPath()
	if err != nil {
		return err
	}
	// NoDisplay keeps it out of application menus: this is a protocol handler,
	// not something anyone should launch from a launcher.
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Fusion Local Server Helper
Comment=Opens and inserts Fusion documents on behalf of fusionlocalserver
Exec=%s %%u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/%s;
`, exe, fusionlink.Scheme)
	if err := atomicfile.WriteFile(path, []byte(entry), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	// Refresh the desktop database and set the default handler. Both are
	// best-effort: some minimal desktops ship neither tool, and the .desktop
	// file alone is enough on those.
	dir := filepath.Dir(path)
	if bin, lerr := exec.LookPath("update-desktop-database"); lerr == nil {
		_ = exec.Command(bin, dir).Run()
	}
	if bin, lerr := exec.LookPath("xdg-mime"); lerr == nil {
		_ = exec.Command(bin, "default", desktopFileName, "x-scheme-handler/"+fusionlink.Scheme).Run()
	}
	return nil
}

func unregisterScheme() error {
	path, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	if bin, lerr := exec.LookPath("update-desktop-database"); lerr == nil {
		_ = exec.Command(bin, filepath.Dir(path)).Run()
	}
	return nil
}

// schemeRegistration reports whether our handler is installed, and where.
func schemeRegistration() (string, bool) {
	path, err := desktopPath()
	if err != nil {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if !strings.Contains(string(data), "x-scheme-handler/"+fusionlink.Scheme) {
		return "", false
	}
	return path, true
}
