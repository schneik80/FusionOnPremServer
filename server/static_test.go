package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The SPA shell must never be cached and fingerprinted assets always should:
// index.html is the only file naming the current bundle hashes, so a stale copy
// silently pins a browser to an old build even after a rebuild and restart.
func TestEmbeddedHandlerCacheHeaders(t *testing.T) {
	h := (&Server{logger: quietLogger()}).embeddedHandler()

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"root serves the shell uncached", "/", noStoreShell},
		{"a client route serves the shell uncached", "/projects/whatever", noStoreShell},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q", got, tc.want)
			}
		})
	}
}

// A missing build asset is a stale reference, not a client route: it must 404
// rather than hand a module loader the HTML shell.
func TestEmbeddedHandlerMissingAssetIs404(t *testing.T) {
	h := (&Server{logger: quietLogger()}).embeddedHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/index-staleHash.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (got body %q)", rec.Code, rec.Body.String())
	}
}

func TestCacheControlFor(t *testing.T) {
	if got := cacheControlFor("assets/index-abc123.js"); got != immutableAsset {
		t.Errorf("asset = %q, want %q", got, immutableAsset)
	}
	if got := cacheControlFor("index.html"); got != noStoreShell {
		t.Errorf("shell = %q, want %q", got, noStoreShell)
	}
}
