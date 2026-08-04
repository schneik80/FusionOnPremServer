package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultClientID is the publisher's APS app client_id for public-client PKCE.
// It is injected at build time:
//
//	go build -ldflags "-X github.com/schneik80/fusionlocalserver/config.DefaultClientID=<id>"
//
// End users running a published binary never need to configure a client_id.
// Developers building from source can override via APS_CLIENT_ID env var or config.json.
var DefaultClientID = ""

// DefaultRegion is the publisher's default APS region ("", "EMEA", or "AUS").
// Empty means US, which is the server-side default.
// Injected at build time alongside DefaultClientID when targeting a specific region.
var DefaultRegion = ""

// DefaultPublicURL is the canonical external base URL the APS app's OAuth
// callback is registered under, e.g. "https://ryzen-nobara.local:8080". Injected
// at build time alongside DefaultClientID so a built binary serves on the host
// the APS app expects without needing the -public-url flag. The -public-url flag
// still overrides it. Empty means "derive the redirect_uri from each request".
var DefaultPublicURL = ""

// Config holds the application configuration.
type Config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"` // optional — only needed for confidential clients
	Region       string `json:"region,omitempty"`        // US (default), EMEA, or AUS

	// AdminUsers is the sign-in whitelist for shared/public deployments: the
	// OIDC subs and/or emails allowed to establish a session. Empty means open
	// access (the local/LAN posture). Editable only here and via the
	// FLS_ADMIN_USERS env var — deliberately not in the settings UI, so the
	// file on disk stays the lockout recovery path. See docs/authentication.md.
	AdminUsers []string `json:"admin_users,omitempty"`
}

// Dir returns the fusionlocalserver config directory path (~/.config/fusionlocalserver), creating it if needed.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "fusionlocalserver")
	return dir, os.MkdirAll(dir, 0700)
}

// Path returns the path to the config file without creating any directories.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.config/fusionlocalserver/config.json"
	}
	return filepath.Join(home, ".config", "fusionlocalserver", "config.json")
}

// Load resolves configuration using a three-layer priority:
//
//  1. APS_CLIENT_ID / APS_CLIENT_SECRET / APS_REGION env vars (highest priority)
//  2. ~/.config/fusionlocalserver/config.json — optional file for power users
//  3. DefaultClientID / DefaultRegion linker variables — embedded by the publisher at build time
//
// End users running a published binary never need to create a config file.
// Developers building from source must supply a client_id via one of the above.
//
// AdminUsers resolves independently of which layer supplied the credentials:
// FLS_ADMIN_USERS (comma-separated) beats the file's admin_users. The env
// branches must not skip the file's whitelist — silently dropping a configured
// whitelist would fail open on a security setting.
func Load() (*Config, error) {
	fileCfg, haveFile, err := readFile()
	if err != nil {
		return nil, err // file exists but is malformed — surface the error
	}

	// Layer 1: environment variables take priority over everything.
	if id := os.Getenv("APS_CLIENT_ID"); id != "" {
		cfg := &Config{
			ClientID:     id,
			ClientSecret: os.Getenv("APS_CLIENT_SECRET"),
			Region:       os.Getenv("APS_REGION"),
		}
		if haveFile {
			cfg.AdminUsers = fileCfg.AdminUsers
		}
		applyAdminUsersEnv(cfg)
		return cfg, nil
	}

	// Layer 2: optional config file (power users / developers with their own APS app).
	if haveFile && fileCfg.ClientID != "" {
		// Apply env-var region override even when using a config file.
		if r := os.Getenv("APS_REGION"); r != "" {
			fileCfg.Region = r
		}
		applyAdminUsersEnv(fileCfg)
		return fileCfg, nil
	}
	// A file without a client_id is valid only when the credentials come from
	// somewhere else (a published binary's baked-in id) — e.g. a config.json
	// created solely to hold admin_users.
	if haveFile && DefaultClientID == "" {
		return nil, fmt.Errorf("client_id is empty in %s", Path())
	}

	// Layer 3: publisher-embedded defaults (normal distribution case).
	if DefaultClientID != "" {
		cfg := &Config{
			ClientID: DefaultClientID,
			Region:   DefaultRegion,
		}
		if haveFile {
			cfg.AdminUsers = fileCfg.AdminUsers
		}
		applyAdminUsersEnv(cfg)
		return cfg, nil
	}

	// Nothing worked — show a developer-facing error.
	return nil, fmt.Errorf("no APS client_id configured")
}

// applyAdminUsersEnv overrides cfg.AdminUsers from the FLS_ADMIN_USERS env var
// (comma-separated, entries trimmed, empties dropped). Unset/empty leaves the
// file's list in place.
func applyAdminUsersEnv(cfg *Config) {
	v := os.Getenv("FLS_ADMIN_USERS")
	if v == "" {
		return
	}
	parts := strings.Split(v, ",")
	users := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			users = append(users, p)
		}
	}
	cfg.AdminUsers = users
}

// readFile attempts to read ~/.config/fusionlocalserver/config.json.
// Returns (cfg, true, nil) on success, (nil, false, nil) when the file is absent,
// and (nil, false, err) when the file exists but cannot be parsed. Field
// validation (client_id presence) is the caller's concern — the file is also
// used to carry admin_users alongside env- or ldflags-supplied credentials.
func readFile() (*Config, bool, error) {
	path := Path()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, true, nil
}
