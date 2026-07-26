// Package appver holds the running binary's version string for packages
// that stamp data files (schemameta) without threading a parameter through
// every store constructor. main sets it once from the -ldflags version
// before anything else runs.
package appver

import "sync/atomic"

var version atomic.Value // string

// Set records the app version. Call once at startup.
func Set(v string) {
	if v == "" {
		v = "dev"
	}
	version.Store(v)
}

// Get returns the recorded version, or "dev" before Set runs (tests).
func Get() string {
	if v, ok := version.Load().(string); ok {
		return v
	}
	return "dev"
}
