package server

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
)

// The built React/MUI SPA is provided by embeddedFS(), whose implementation is
// selected at compile time by a build tag:
//
//   - static_embed.go (//go:build embed_ui) — embeds server/webdist via
//     go:embed. `make build` runs `vite build` first, then `go build -tags
//     embed_ui`, so the binary ships the real UI.
//   - static_stub.go (//go:build !embed_ui) — serves a tiny "not built yet"
//     shell from an in-memory FS, so a plain `go build` (and `go test`/`go vet`)
//     compiles without server/webdist existing and without committing a
//     placeholder that the build would clobber.
//
// The whole server/webdist/ tree is gitignored as pure build output.

// defaultViteDevServer is the origin the dev reverse-proxy targets when
// VITE_DEV_SERVER is unset.
const defaultViteDevServer = "http://localhost:5173"

// staticHandler returns the handler that serves the SPA for all non-/api
// routes. In production it serves the embedded build with SPA fallback; with
// -dev it reverse-proxies to the Vite dev server for hot module reload.
func (s *Server) staticHandler() http.Handler {
	if s.opts.Dev {
		return s.devProxyHandler()
	}
	return s.embeddedHandler()
}

// embeddedHandler serves files from the embedded webdist FS. Requests that
// don't map to a real file fall back to index.html so client-side routes
// (deep links) resolve to the SPA shell. /api routes never reach here — they
// match more specific patterns on the mux.
func (s *Server) embeddedHandler() http.Handler {
	dist, err := embeddedFS()
	if err != nil {
		s.logger.Error("static: cannot open embedded webdist", "err", err)
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if name == "." || name == "/" {
			name = "index.html"
		}
		// Serve the real file when it exists.
		if f, err := dist.Open(name); err == nil {
			_ = f.Close()
			w.Header().Set("Cache-Control", cacheControlFor(name))
			fileServer.ServeHTTP(w, r)
			return
		}
		// A build asset that isn't there is a MISS, not a client route: answer
		// 404 rather than the SPA shell. Returning index.html for
		// /assets/index-<oldhash>.js hands a module loader a page of HTML, which
		// surfaces as an opaque MIME error — and, when the browser still has the
		// matching stale bundle cached, as no error at all and a silently
		// out-of-date app.
		if isBuildAsset(name) {
			http.NotFound(w, r)
			return
		}
		// SPA fallback: hand back index.html for unknown client routes.
		data, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "web UI not built (run `make web`)", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", noStoreShell)
		_, _ = w.Write(data)
	})
}

// Cache policy for the SPA. Vite fingerprints every build asset
// (assets/index-<hash>.js), so those bytes can never change under their name
// and are safe to cache forever. index.html is the opposite: it is the one file
// that names the current hashes, so a browser holding a stale copy loads a
// stale bundle — a rebuilt, restarted server that still shows the old UI, with
// nothing in the logs to say so. Embedded files carry a zero mtime, so
// net/http sends no Last-Modified and there is nothing to revalidate against;
// the shell therefore has to be no-store rather than no-cache.
const (
	immutableAsset = "public, max-age=31536000, immutable"
	noStoreShell   = "no-store"
)

// isBuildAsset reports whether a path is one of Vite's fingerprinted build
// outputs, i.e. a file whose absence means a stale reference rather than a
// client-side route.
func isBuildAsset(name string) bool {
	return strings.HasPrefix(name, "assets/")
}

func cacheControlFor(name string) string {
	if isBuildAsset(name) {
		return immutableAsset
	}
	return noStoreShell
}

// devProxyHandler reverse-proxies non-/api requests to the Vite dev server so
// the React app is served (with HMR) through the same origin as /api. Target
// is VITE_DEV_SERVER or http://localhost:5173. httputil's proxy forwards the
// websocket Upgrade Vite's HMR client needs.
func (s *Server) devProxyHandler() http.Handler {
	target := os.Getenv("VITE_DEV_SERVER")
	if target == "" {
		target = defaultViteDevServer
	}
	u, err := url.Parse(target)
	if err != nil {
		s.logger.Error("static: invalid VITE_DEV_SERVER, falling back to embedded", "target", target, "err", err)
		return s.embeddedHandler()
	}
	s.logger.Info("static: dev mode — proxying UI to Vite", "target", target)
	return httputil.NewSingleHostReverseProxy(u)
}
