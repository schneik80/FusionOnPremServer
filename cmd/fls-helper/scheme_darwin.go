//go:build darwin

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

// macOS resolves URL schemes through Launch Services, which only knows about
// bundles — a bare executable cannot claim a scheme. So `register` builds a
// minimal .app around this binary in ~/Applications and asks lsregister to
// notice it. Per-user, no root, no installer package.

const appBundleName = "Fusion Local Server Helper.app"

func bundlePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", appBundleName), nil
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
	bundle, err := bundlePath()
	if err != nil {
		return err
	}
	macOSDir := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macOSDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", bundle, err)
	}

	// The bundle's executable is a two-line launcher rather than a copy of the
	// binary: copying would silently pin the bundle to whichever build was
	// current at registration time, so an upgraded helper would keep running
	// the old code until someone re-registered.
	launcher := filepath.Join(macOSDir, "fls-helper")
	shim := "#!/bin/sh\nexec " + shellQuote(exe) + " \"$@\"\n"
	if err := atomicfile.WriteFile(launcher, []byte(shim), 0755); err != nil {
		return fmt.Errorf("writing %s: %w", launcher, err)
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>Fusion Local Server Helper</string>
	<key>CFBundleIdentifier</key><string>com.schneik80.fusionlocalserver.helper</string>
	<key>CFBundleExecutable</key><string>fls-helper</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>%s</string>
	<key>LSUIElement</key><true/>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleURLName</key><string>Fusion Local Server</string>
			<key>CFBundleURLSchemes</key><array><string>%s</string></array>
		</dict>
	</array>
</dict>
</plist>
`, version, fusionlink.Scheme)
	info := filepath.Join(bundle, "Contents", "Info.plist")
	if err := atomicfile.WriteFile(info, []byte(plist), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", info, err)
	}

	// Nudge Launch Services to pick up the new bundle immediately; without it
	// the scheme only starts working after the system rescans on its own.
	const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/" +
		"LaunchServices.framework/Support/lsregister"
	if _, serr := os.Stat(lsregister); serr == nil {
		_ = exec.Command(lsregister, "-f", bundle).Run()
	}
	return nil
}

func unregisterScheme() error {
	bundle, err := bundlePath()
	if err != nil {
		return err
	}
	const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/" +
		"LaunchServices.framework/Support/lsregister"
	if _, serr := os.Stat(lsregister); serr == nil {
		_ = exec.Command(lsregister, "-u", bundle).Run()
	}
	if err := os.RemoveAll(bundle); err != nil {
		return fmt.Errorf("removing %s: %w", bundle, err)
	}
	return nil
}

func schemeRegistration() (string, bool) {
	bundle, err := bundlePath()
	if err != nil {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return "", false
	}
	if !strings.Contains(string(data), "<string>"+fusionlink.Scheme+"</string>") {
		return "", false
	}
	return bundle, true
}

// shellQuote makes a path safe inside the single-quoted /bin/sh launcher.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
