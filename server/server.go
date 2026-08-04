// Package server runs fusionlocalserver as an HTTP service: a JSON REST API over
// the shared api/config/pins packages, plus an embedded React/MUI SPA. Each
// browser user logs in with their own Autodesk account; the server holds their
// APS tokens in a per-session store and proxies their data calls under their
// own identity.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/config"
)

// Options configures a server run. Config may be nil when CfgErr is set (no
// usable APS configuration); Run then fails fast with a clear message.
type Options struct {
	// Verbose raises the log level to debug and adds per-request and upstream
	// API tracing, to both the console and the log file.
	Verbose bool
	// Dev proxies the web UI to the Vite dev server for HMR and pins the listen
	// port to the default (so the proxy target is stable); the runtime
	// port-change endpoint is disabled in this mode.
	Dev bool
	// TLS serves over HTTPS. TLSCert/TLSKey are an optional caller-supplied
	// PEM pair; when TLS is set but they are empty, a self-signed cert is
	// generated and cached under config.Dir().
	TLS     bool
	TLSCert string
	TLSKey  string
	// PublicURL is the canonical external base URL clients reach the server by
	// (e.g. https://fusion.lan:8080). When set, the OAuth redirect_uri is built
	// from it (so only one callback need be registered with APS) and requests
	// arriving via any other host are redirected to it.
	PublicURL string
	Config    *config.Config
	CfgErr    error
	Version   string
}

// Server holds the runtime state shared across handlers.
type Server struct {
	opts   Options
	logger *slog.Logger
	region string // resolved APS region ("" == US)

	// startedAt anchors the Uptime admin tool; requestCount is a plain
	// served-requests counter (both process-lifetime, not persisted).
	startedAt    time.Time
	requestCount atomic.Int64

	// APS app credentials, used by the login/callback handlers and per-session
	// token refresh. clientSecret is empty for public (PKCE) clients.
	clientID     string
	clientSecret string

	// adminUsers is the sign-in whitelist (config admin_users / FLS_ADMIN_USERS):
	// OIDC subs matched exactly, emails case-insensitively. Empty means open
	// access — the local/LAN posture. Immutable after Run wires it, so handlers
	// read it without locking. See userAllowed in auth.go.
	adminUsers []string

	// sessions holds one logged-in identity per browser user; pending holds
	// in-flight logins between the authorize redirect and the callback.
	sessions *SessionStore
	pending  *PendingStore

	// TLS state, resolved once in Run. When tlsEnabled, the listener serves
	// HTTPS from tlsCertFile/tlsKeyFile and the session cookie is Secure.
	tlsEnabled  bool
	tlsCertFile string
	tlsKeyFile  string

	// publicURL is the canonical external base URL (no trailing slash) used to
	// build the OAuth redirect_uri; publicOrigin is its scheme://host, used to
	// detect requests that arrived via a different host. Empty disables both
	// (the callback is then derived per request from r.Host).
	publicURL    string
	publicOrigin string

	// portConfigurable gates the runtime port-change endpoint. The server owns
	// the port (and so derives the bind address from server.json) unless it is
	// in dev mode, where the Vite proxy is pinned to the default port.
	portConfigurable bool

	// restartCh signals the bind loop to drain the current listener and rebind
	// from the updated port setting. Buffered (cap 1) so the handler never
	// blocks; a pending restart coalesces.
	restartCh chan struct{}

	// addrMu guards addr, the currently bound address, read by handleMeta.
	addrMu sync.RWMutex
	addr   string

	// pinsMu serialises the non-atomic Load->mutate->Save pin cycle.
	pinsMu sync.Mutex

	// logo caches the sign-in logo's bytes (branding.go). The zero value is a
	// valid empty store that loads from disk on first use, so nothing has to
	// be built here — and the sign-in screen, the one page every visitor
	// loads first, is served without touching the disk after that.
	logo logoStore

	// thumbs caches thumbnail status/URLs and image bytes by component version
	// id, shared across all clients. warmSem bounds background image prefetches
	// kicked off from the classify probe.
	thumbs  *thumbCache
	warmSem chan struct{}

	// hubs owns one storeSet (chat/tasks/production/whiteboards + per-hub SSE
	// fan-out) per hub, rooted under <config>/hubs/<slug>/ and resolved
	// per-request from the session's selected hub by requireHub. Nil when the
	// config dir is unavailable; local-data endpoints then reply 503.
	// Authorization stays global: chatAuthz maps the caller's APS project
	// role to capabilities — one roster cache across every hub and feature.
	hubs *hubStores

	// chatAuthz + the per-session rate limiters are process-global (a session
	// spans hubs across switches; the limits are per user, not per hub).
	// chatKeepalive is the events stream's ping/entitlement-recheck cadence
	// (defaulted in Run; overridable in tests).
	chatAuthz     *chat.Authorizer
	chatMsgLim    *chat.Limiter
	chatOpLim     *chat.Limiter
	chatSyncLim   *chat.Limiter
	chatKeepalive time.Duration

	taskOpLim       *chat.Limiter
	prodOpLim       *chat.Limiter
	whiteboardOpLim *chat.Limiter
	// whiteboardDocLim meters the document PUT, which an open canvas issues far
	// more often than a person renames a board — so it cannot share the op
	// limiter's human-paced budget, but a 24 MiB body must not be unmetered.
	whiteboardDocLim *chat.Limiter
	// whiteboardPatchLim meters live edits. Drawing is bursty by nature, so the
	// budget is generous and keyed per session AND board — two open boards must
	// not starve each other.
	whiteboardPatchLim *chat.Limiter
	notifOpLim         *chat.Limiter

	// uploads tracks background file-upload jobs (per-session; see uploads.go).
	uploads *uploadManager

	// backupPoke wakes the backup scheduler goroutine to recompute its timers
	// after a per-hub config change (buffered, cap 1, so posting never blocks
	// a handler). Engines are built on demand per hub (backupEngineFor) —
	// there is no cached process-global engine anymore.
	backupPoke chan struct{}
}

// serveReason explains why the inner serve loop returned.
type serveReason int

const (
	reasonShutdown serveReason = iota // lifecycle context cancelled (SIGINT/TERM)
	reasonRestart                     // port changed; rebind the listener
)

// Run starts the HTTP server and blocks until it shuts down (SIGINT/SIGTERM)
// or fails to start. There is no startup login: each browser user signs in
// through the /api/auth flow once the listener is up.
func Run(opts Options) error {
	logger, closeLog := setupLogging(opts.Verbose)
	defer closeLog()

	var clientID, clientSecret, region string
	var adminUsers []string
	if opts.Config != nil {
		clientID = opts.Config.ClientID
		clientSecret = opts.Config.ClientSecret
		region = opts.Config.Region
		adminUsers = opts.Config.AdminUsers
	} else if opts.CfgErr != nil {
		logger.Warn("config load failed", "err", opts.CfgErr)
	}
	if clientID == "" {
		return fmt.Errorf("no APS client_id configured (build with CLIENT_ID, or set APS_CLIENT_ID / config.json)")
	}

	// Region is process-global; set once before any API call.
	api.SetRegion(region)

	s := &Server{
		opts:             opts,
		logger:           logger,
		clientID:         clientID,
		clientSecret:     clientSecret,
		region:           region,
		adminUsers:       adminUsers,
		sessions:         NewSessionStore(sessionIdleTTL, sessionAbsTTL, logger),
		pending:          NewPendingStore(pendingTTL),
		portConfigurable: !opts.Dev,
		restartCh:        make(chan struct{}, 1),
		startedAt:        time.Now(),
		thumbs:           newThumbCache(512, 10*time.Minute),
		warmSem:          make(chan struct{}, 12),
		uploads:          newUploadManager(uploadConcurrency),
	}

	// A canonical public URL fixes the OAuth redirect_uri (one APS registration)
	// and lets the server bounce clients who arrived via another host to it.
	if opts.PublicURL != "" {
		u, perr := url.Parse(opts.PublicURL)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("invalid -public-url %q: want an absolute URL like https://host:port", opts.PublicURL)
		}
		s.publicOrigin = u.Scheme + "://" + u.Host
		s.publicURL = s.publicOrigin
		logger.Info("public URL set: OAuth callback is fixed and other hosts are redirected here", "public_url", s.publicURL)
	}

	if len(adminUsers) > 0 {
		logger.Info("admin whitelist active: sign-in restricted to listed users", "entries", len(adminUsers))
	}

	// Restore sessions from the last run so a restart doesn't log everyone out.
	// The whitelist predicate must be wired before the load so sessions of
	// since-removed users are dropped rather than restored.
	s.sessions.allowed = s.userAllowed
	if dir, derr := config.Dir(); derr != nil {
		logger.Warn("sessions: persistence disabled (config dir unavailable)", "err", derr)
	} else if lerr := s.sessions.EnablePersistence(dir); lerr != nil {
		logger.Warn("sessions: could not load persisted sessions", "err", lerr)
	}

	// Authorization + per-session rate limits (global — see the field docs).
	// Messages 2/s with burst 5, channel ops 10/min, sync (read cursors +
	// typing pings) 2/s with burst 20; task/production mutations 2/s burst 10
	// (generous enough for Kanban drags, tight enough to stop scripted
	// floods); whiteboard create/rename likewise (document autosaves are
	// debounced client-side and never hit this limiter).
	s.chatAuthz = chat.NewAuthorizer()
	s.chatMsgLim = chat.NewLimiter(2, 5)
	s.chatOpLim = chat.NewLimiter(10.0/60.0, 10)
	s.chatSyncLim = chat.NewLimiter(2, 20)
	s.chatKeepalive = 25 * time.Second
	s.taskOpLim = chat.NewLimiter(2, 10)
	s.prodOpLim = chat.NewLimiter(2, 10)
	s.whiteboardOpLim = chat.NewLimiter(2, 10)
	// Autosave fires at most once per 1.5s idle window per open board, so 2/s
	// with a burst of 20 covers several boards open at once and a flush on
	// unmount, while still bounding a runaway client.
	s.whiteboardDocLim = chat.NewLimiter(2, 20)
	// A patch is debounced client-side to ~10/s at worst; 20/s with a burst of
	// 40 absorbs a fast drag and a paste without ever pacing normal drawing.
	s.whiteboardPatchLim = chat.NewLimiter(20, 40)
	// Notification mark-read/dismiss: user clicks in the bell, so the same
	// generous 2/s burst 10 as the other app op limiters. Reads (the polled
	// list) are unmetered here — react-query paces them.
	s.notifOpLim = chat.NewLimiter(2, 10)

	// Per-hub store sets, rooted under <config>/hubs/<slug>/ and built
	// lazily when a session locks to a hub. Nil (all local-data endpoints
	// 503) when the config dir is unavailable.
	if dir, derr := config.Dir(); derr != nil {
		logger.Warn("local stores: disabled (config dir unavailable)", "err", derr)
	} else {
		s.hubs = newHubStores(dir, s.chatAuthz)
		defer s.hubs.closeAll()
	}

	// Backup engines are built on demand per hub from hubs/<slug>/backup.json
	// (backupEngineFor); backupPoke re-arms the scheduler after config edits.
	s.backupPoke = make(chan struct{}, 1)

	// Resolve TLS once, before the bind loop spans restarts. A self-signed
	// cert is generated/cached when -tls is given without a cert pair.
	if opts.TLS {
		if (opts.TLSCert == "") != (opts.TLSKey == "") {
			return fmt.Errorf("-tls-cert and -tls-key must be given together")
		}
		// Make sure a self-signed cert covers the canonical hostname, so the
		// address clients are redirected to validates.
		var extraHosts []string
		if u, e := url.Parse(s.publicURL); s.publicURL != "" && e == nil && u.Hostname() != "" {
			extraHosts = append(extraHosts, u.Hostname())
		}
		certFile, keyFile, selfSigned, err := resolveTLSPaths(opts.TLSCert, opts.TLSKey, extraHosts)
		if err != nil {
			return fmt.Errorf("preparing TLS: %w", err)
		}
		s.tlsEnabled = true
		s.tlsCertFile = certFile
		s.tlsKeyFile = keyFile
		if selfSigned {
			logger.Info("TLS: using self-signed certificate (browsers will warn once)", "cert", certFile)
		} else {
			logger.Info("TLS: using provided certificate", "cert", certFile)
		}
	}

	// Lifecycle context cancelled on first interrupt signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Expire idle/old sessions and abandoned in-flight logins in the background.
	s.sessions.StartJanitor(ctx)
	s.pending.StartJanitor(ctx)

	// Daily backup scheduler. Idles (rather than exits) while backups are
	// disabled or unconfigured so enabling them at runtime needs no goroutine
	// management; stops with the lifecycle context.
	go s.runBackupScheduler(ctx)

	// Listener (re)bind loop. Auth + the token refresher above are set up once
	// and span restarts; only the HTTP listener is recreated. A runtime port
	// change (POST /api/settings/port) writes server.json and signals
	// restartCh, dropping us out of serveUntil with reasonRestart so we rebind
	// on the new port.
	var prevAddr string // last successfully-served address; "" until first bind
	for {
		// If shutdown was requested (possibly while a restart was also pending,
		// where select could have picked the restart), bail before rebinding.
		select {
		case <-ctx.Done():
			logger.Info("server stopped")
			return nil
		default:
		}

		addr := s.resolveAddr()
		s.setAddr(addr)

		srv := &http.Server{
			Addr:              addr,
			Handler:           s.routes(),
			ReadHeaderTimeout: 10 * time.Second,
			// Route the server's internal complaints through slog, demoting
			// no-handshake disconnect noise (probes, browser preconnects) to
			// debug — see httpErrorLog.
			ErrorLog: httpErrorLog(logger),
		}

		logger.Info("server starting",
			"addr", addr,
			"version", opts.Version,
			"region", regionLabel(region),
			"dev", opts.Dev,
			"tls", s.tlsEnabled,
			"portConfigurable", s.portConfigurable,
		)
		warnOpenNetwork(logger, addr, s.tlsEnabled)
		scheme := "http"
		if s.tlsEnabled {
			scheme = "https"
		}
		for _, u := range lanURLs(scheme, addr) {
			logger.Info("reachable on the LAN", "url", u)
		}

		reason, err := s.serveUntil(ctx, srv)
		if err != nil {
			if prevAddr == "" {
				return fmt.Errorf("http server: %w", err) // initial bind failed → fatal
			}
			// A runtime rebind failed — most likely the new port was taken in
			// the TOCTOU window after the handler's bind pre-check. Revert the
			// persisted port and keep serving on the previous one rather than
			// killing the server out from under the operator. The previous port
			// is free again (its listener was drained when the restart fired).
			logger.Error("rebind failed; reverting to previous port",
				"failed_addr", addr, "prev_addr", prevAddr, "err", err)
			if p, perr := portFromAddr(prevAddr); perr == nil {
				if serr := UpdateSettings(func(set *Settings) { set.Port = p }); serr != nil {
					logger.Error("reverting persisted port failed", "err", serr)
				}
			}
			continue
		}
		if reason == reasonShutdown {
			logger.Info("server stopped")
			return nil
		}
		prevAddr = addr
		logger.Info("port changed — restarting listener", "next_addr", s.resolveAddr())
	}
}

// serveUntil runs srv until the lifecycle context is cancelled (shutdown) or a
// restart is requested, draining connections gracefully in both cases. A
// ListenAndServe failure (e.g. the port is already in use) is returned as a
// terminal error.
func (s *Server) serveUntil(ctx context.Context, srv *http.Server) (serveReason, error) {
	errCh := make(chan error, 1)
	go func() {
		var err error
		if s.tlsEnabled {
			err = srv.ListenAndServeTLS(s.tlsCertFile, s.tlsKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		s.drain(srv)
		return reasonShutdown, <-errCh
	case <-s.restartCh:
		s.drain(srv)
		return reasonRestart, <-errCh
	case err := <-errCh:
		// ListenAndServe returned on its own — a bind failure or other fatal
		// error (a clean shutdown would have come via the cases above).
		return reasonShutdown, err
	}
}

// drain gracefully shuts down srv, waiting up to 10s for in-flight requests
// (including the just-sent port-change response) to complete. Chat event
// streams are closed first, fanned out over EVERY hub's SSE hub — they are
// indefinitely long-lived, and Shutdown would otherwise wait the full budget
// on them and stall the port-rebind (or restore-restart) loop.
func (s *Server) drain(srv *http.Server) {
	for _, set := range s.hubs.snapshotAll() {
		if set.chatHub != nil {
			set.chatHub.CloseAll()
		}
	}
	s.logger.Info("draining connections")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("graceful shutdown failed", "err", err)
	}
}

// resolveAddr computes the bind address, always on the wildcard host. In dev
// mode the port is pinned to the default (the Vite proxy targets it); otherwise
// the server owns the port and derives it from the persisted setting
// (server.json), falling back to the default.
func (s *Server) resolveAddr() string {
	if !s.portConfigurable {
		return fmt.Sprintf("0.0.0.0:%d", defaultPort)
	}
	port := defaultPort
	if set, err := LoadSettings(); err != nil {
		s.logger.Warn("settings load failed; using default port", "err", err, "port", defaultPort)
	} else if set.Port != 0 {
		port = set.Port
	}
	return fmt.Sprintf("0.0.0.0:%d", port)
}

func (s *Server) setAddr(addr string) {
	s.addrMu.Lock()
	s.addr = addr
	s.addrMu.Unlock()
}

// currentPort returns the port of the currently bound address, or 0 if it
// can't be parsed.
func (s *Server) currentPort() int {
	s.addrMu.RLock()
	addr := s.addr
	s.addrMu.RUnlock()
	p, _ := portFromAddr(addr)
	return p
}

// portFromAddr extracts the numeric port from a host:port address.
func portFromAddr(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(p)
}

// requestRestart asks the bind loop to rebind. Non-blocking: a pending restart
// coalesces rather than queuing.
func (s *Server) requestRestart() {
	select {
	case s.restartCh <- struct{}{}:
	default:
	}
}

// regionLabel renders the region for display/logging; empty maps to "US".
func regionLabel(region string) string {
	if region == "" {
		return "US"
	}
	return region
}

// lanURLs returns the browser URLs the server is reachable at, using the given
// scheme ("http" or "https"). When bound to a wildcard host it enumerates the
// machine's non-loopback IPv4 interface addresses so an operator can copy a LAN
// URL straight from the startup log.
func lanURLs(scheme, addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{scheme + "://" + addr}
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{scheme + "://" + net.JoinHostPort(host, port)}
	}

	urls := []string{scheme + "://" + net.JoinHostPort("localhost", port)}
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return urls
	}
	for _, a := range ifaceAddrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			urls = append(urls, scheme+"://"+net.JoinHostPort(ip4.String(), port))
		}
	}
	return urls
}

// warnOpenNetwork emits a visible warning when the server is bound to a
// non-loopback address over plain HTTP: the session cookie is then not Secure
// (browsers drop Secure cookies over http), so a wire sniffer on the network
// can capture a cookie and hijack that user's session. TLS closes this (the
// cookie becomes Secure), so the warning is suppressed when tls is set.
func warnOpenNetwork(logger *slog.Logger, addr string, tls bool) {
	if tls {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	loopback := host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1"
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if !loopback {
		logger.Warn("server is reachable on the network over plain HTTP — session cookies are not encrypted in transit; use -tls or front with TLS",
			"addr", addr)
	}
}
