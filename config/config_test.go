package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// clearEnv unsets the APS_* and FLS_* environment variables for the duration
// of the test. Load() treats empty strings as unset (it checks `!= ""`), so
// this works correctly.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APS_CLIENT_ID", "")
	t.Setenv("APS_CLIENT_SECRET", "")
	t.Setenv("APS_REGION", "")
	t.Setenv("FLS_ADMIN_USERS", "")
}

// setHome points the config dir at a temp home on every platform —
// os.UserHomeDir reads HOME on Unix and USERPROFILE on Windows.
func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// writeConfigFile creates ~/.config/fusionlocalserver/config.json under the given home dir.
func writeConfigFile(t *testing.T, home, contents string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "fusionlocalserver")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// saveDefaults snapshots the package-level ldflags vars and restores them via t.Cleanup.
func saveDefaults(t *testing.T) {
	t.Helper()
	prevID := DefaultClientID
	prevRegion := DefaultRegion
	t.Cleanup(func() {
		DefaultClientID = prevID
		DefaultRegion = prevRegion
	})
}

func TestLoad_EnvVarsTakePrecedence(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = "ld-id"
	DefaultRegion = "EMEA"

	// File present with a different client_id — env should still win.
	writeConfigFile(t, home, `{"client_id":"file-id","region":"AUS"}`)

	t.Setenv("APS_CLIENT_ID", "env-id")
	t.Setenv("APS_CLIENT_SECRET", "env-secret")
	t.Setenv("APS_REGION", "EMEA")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "env-id" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "env-id")
	}
	if cfg.ClientSecret != "env-secret" {
		t.Errorf("ClientSecret = %q, want %q", cfg.ClientSecret, "env-secret")
	}
	if cfg.Region != "EMEA" {
		t.Errorf("Region = %q, want %q", cfg.Region, "EMEA")
	}
}

func TestLoad_FileFallback(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""
	DefaultRegion = ""

	writeConfigFile(t, home, `{"client_id":"file-id","region":"EMEA"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "file-id" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "file-id")
	}
	if cfg.Region != "EMEA" {
		t.Errorf("Region = %q, want %q", cfg.Region, "EMEA")
	}
}

func TestLoad_FileFallback_RegionEnvOverride(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""
	DefaultRegion = ""

	writeConfigFile(t, home, `{"client_id":"file-id","region":"EMEA"}`)
	t.Setenv("APS_REGION", "AUS")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "file-id" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "file-id")
	}
	if cfg.Region != "AUS" {
		t.Errorf("Region = %q, want %q (env override)", cfg.Region, "AUS")
	}
}

func TestLoad_LdflagsFallback(t *testing.T) {
	clearEnv(t)
	home := t.TempDir() // empty — no config file
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = "ld-id"
	DefaultRegion = "EMEA"

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "ld-id" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "ld-id")
	}
	if cfg.Region != "EMEA" {
		t.Errorf("Region = %q, want %q", cfg.Region, "EMEA")
	}
}

func TestLoad_NoneConfigured_Errors(t *testing.T) {
	clearEnv(t)
	home := t.TempDir() // empty — no config file
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""
	DefaultRegion = ""

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load: got nil error, want error; cfg = %+v", cfg)
	}
	if !strings.Contains(err.Error(), "no APS client_id") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "no APS client_id")
	}
}

func TestLoad_MalformedFile_Errors(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""
	DefaultRegion = ""

	writeConfigFile(t, home, `{garbage`)

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load: got nil error, want error; cfg = %+v", cfg)
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parsing")
	}
}

func TestLoad_EmptyClientIDInFile_Errors(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""
	DefaultRegion = ""

	writeConfigFile(t, home, `{"client_id":""}`)

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load: got nil error, want error; cfg = %+v", cfg)
	}
	if !strings.Contains(err.Error(), "client_id is empty") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "client_id is empty")
	}
}

func TestLoad_AdminUsersFromFile(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""

	writeConfigFile(t, home, `{"client_id":"file-id","admin_users":["ada@x.io","sub-123"]}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AdminUsers) != 2 || cfg.AdminUsers[0] != "ada@x.io" || cfg.AdminUsers[1] != "sub-123" {
		t.Errorf("AdminUsers = %v, want [ada@x.io sub-123]", cfg.AdminUsers)
	}
}

func TestLoad_AdminUsersEnvOverride(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""

	writeConfigFile(t, home, `{"client_id":"file-id","admin_users":["file@x.io"]}`)
	// Messy value: spaces trimmed, empty entries dropped.
	t.Setenv("FLS_ADMIN_USERS", " env@x.io ,, sub-9 ,")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AdminUsers) != 2 || cfg.AdminUsers[0] != "env@x.io" || cfg.AdminUsers[1] != "sub-9" {
		t.Errorf("AdminUsers = %v, want [env@x.io sub-9] (env override)", cfg.AdminUsers)
	}
}

// TestLoad_EnvCreds_AdminUsersStillFromFile guards the layer-1 trap: with
// credentials from APS_CLIENT_ID, the file's whitelist must still load —
// silently dropping it would fail open on a security setting.
func TestLoad_EnvCreds_AdminUsersStillFromFile(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = ""

	// The file carries only the whitelist; client_id comes from the env.
	writeConfigFile(t, home, `{"admin_users":["ada@x.io"]}`)
	t.Setenv("APS_CLIENT_ID", "env-id")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "env-id" {
		t.Errorf("ClientID = %q, want env-id", cfg.ClientID)
	}
	if len(cfg.AdminUsers) != 1 || cfg.AdminUsers[0] != "ada@x.io" {
		t.Errorf("AdminUsers = %v, want [ada@x.io]", cfg.AdminUsers)
	}
}

// TestLoad_AdminUsersOnlyFile_LdflagsCreds: a published binary (baked-in
// client id) plus a config.json created solely to hold admin_users.
func TestLoad_AdminUsersOnlyFile_LdflagsCreds(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	setHome(t, home)
	saveDefaults(t)
	DefaultClientID = "ld-id"
	DefaultRegion = "EMEA"

	writeConfigFile(t, home, `{"admin_users":["ada@x.io"]}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "ld-id" || cfg.Region != "EMEA" {
		t.Errorf("cfg = %+v, want ld-id/EMEA", cfg)
	}
	if len(cfg.AdminUsers) != 1 || cfg.AdminUsers[0] != "ada@x.io" {
		t.Errorf("AdminUsers = %v, want [ada@x.io]", cfg.AdminUsers)
	}
}

func TestDir_CreatesWithMode0700(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(home, ".config", "fusionlocalserver")
	if dir != want {
		t.Errorf("Dir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
	// Unix permission bits don't map onto Windows (Go reports 0777 there).
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0700 {
			t.Errorf("perm = %o, want %o", perm, 0700)
		}
	}
}

func TestPath_ReturnsExpected(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	got := Path()
	want := filepath.Join(home, ".config", "fusionlocalserver", "config.json")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}
