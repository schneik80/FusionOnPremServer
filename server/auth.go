package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/schneik80/fusionlocalserver/auth"
)

const (
	sessionCookieName = "fls_session"
	pendingCookieName = "fls_pending"
	authCtxTimeout    = 30 * time.Second
)

// Indirections over the auth package so tests can stub the network round-trips
// (matching the package's const→var injection style). Production code never
// reassigns them.
var (
	authExchange = auth.ExchangeCode
	authRefresh  = auth.Refresh
	authUserInfo = auth.FetchUserProfile
)

// tokenCtxKey carries the per-request APS access token that requireAuth
// resolves from the session; sessionCtxKey carries the session itself, for
// handlers (chat) that need the caller's identity, not just their token.
// Private type so no other package can collide.
type ctxKey int

const (
	tokenCtxKey ctxKey = iota
	sessionCtxKey
	// storesCtxKey carries the storeSet requireHub resolved from the
	// session's selected hub (see hubgate.go).
	storesCtxKey
)

func tokenFromCtx(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenCtxKey).(string)
	return t, ok && t != ""
}

func sessionFromCtx(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionCtxKey).(*Session)
	return s, ok && s != nil
}

// AuthMeDTO is the GET /api/auth/me response: the SPA's login-state probe.
// SelectedHubID/Name reflect the session's hub lock (empty until POST
// /api/session/hub) so a returning client can re-enter its hub directly.
type AuthMeDTO struct {
	Authenticated   bool     `json:"authenticated"`
	User            *UserDTO `json:"user,omitempty"`
	SelectedHubID   string   `json:"selectedHubId,omitempty"`
	SelectedHubName string   `json:"selectedHubName,omitempty"`
}

// UserDTO is the minimal logged-in identity shown in the web UI. ID is the
// OIDC subject (stable Autodesk user id); the SPA uses it for own-message
// affordances in chat. Empty when the profile fetch failed at login.
type UserDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// isSecure reports whether the request arrived over TLS (directly or via a
// terminating proxy). The session cookie's Secure flag is set from this, so the
// same binary works over plain-HTTP LAN today and auto-hardens behind TLS.
func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// requestOrigin is the external scheme://host the client used to reach the
// server, honoring a TLS-terminating proxy via X-Forwarded-Proto.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if isSecure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// callbackURI returns the OAuth redirect_uri. With a canonical public URL
// configured it is fixed — so only that one callback need be registered on the
// APS app, regardless of how each client connects. Otherwise it is derived from
// the request origin (and every origin used must then be registered).
func (s *Server) callbackURI(r *http.Request) string {
	base := s.publicURL
	if base == "" {
		base = requestOrigin(r)
	}
	return base + "/api/auth/callback"
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		// Lax (not Strict): the OAuth callback is a top-level cross-site
		// navigation from autodesk.com, and Strict would drop the cookie there.
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	setCookie(w, name, "", -1, secure)
}

// handleAuthLogin starts the OAuth flow: mint PKCE + state, remember them, and
// 302 to the Autodesk authorize page.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	verifier, challenge, err := auth.NewPKCE()
	if err != nil {
		s.logger.Error("auth: pkce generation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not start login")
		return
	}
	state, err := randToken(32)
	if err != nil {
		s.logger.Error("auth: state generation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not start login")
		return
	}
	redirectURI := s.callbackURI(r)
	s.pending.Put(state, pendingEntry{verifier: verifier, redirectURI: redirectURI, createdAt: time.Now()})
	setCookie(w, pendingCookieName, state, int(pendingTTL.Seconds()), isSecure(r))
	http.Redirect(w, r, auth.BuildAuthURL(s.clientID, challenge, redirectURI, state), http.StatusFound)
}

// handleAuthCallback completes the flow: validate state (CSRF), exchange the
// code, create the session, set the cookie, and 302 back to the SPA. Failures
// redirect to /?auth_error=<reason> so the login screen can explain.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.logger.Warn("auth: callback returned error", "error", e, "desc", q.Get("error_description"))
		s.redirectAuthError(w, r, e)
		return
	}

	// Validate state against both the query param (proves the round-trip) and
	// the pending cookie (proves the same browser started the login). Clear the
	// pending cookie regardless.
	state := q.Get("state")
	pc, cerr := r.Cookie(pendingCookieName)
	clearCookie(w, pendingCookieName, isSecure(r))
	if cerr != nil || state == "" || pc.Value != state {
		s.redirectAuthError(w, r, "state_mismatch")
		return
	}
	pe, ok := s.pending.Take(state)
	if !ok {
		s.redirectAuthError(w, r, "state_expired")
		return
	}
	code := q.Get("code")
	if code == "" {
		s.redirectAuthError(w, r, "no_code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), authCtxTimeout)
	defer cancel()

	td, err := authExchange(ctx, s.clientID, s.clientSecret, code, pe.verifier, pe.redirectURI)
	if err != nil {
		s.logger.Error("auth: code exchange failed", "err", err)
		s.redirectAuthError(w, r, "exchange_failed")
		return
	}

	profile, perr := authUserInfo(ctx, td.AccessToken)
	if perr != nil {
		s.logger.Warn("auth: user profile fetch failed (continuing)", "err", perr)
	}

	sess, err := s.sessions.Create(td, profile)
	if err != nil {
		s.logger.Error("auth: session creation failed", "err", err)
		s.redirectAuthError(w, r, "session_failed")
		return
	}
	setCookie(w, sessionCookieName, sess.ID, int(sessionAbsTTL.Seconds()), isSecure(r))
	s.logger.Info("auth: login complete", "user", profile.Email)
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleAuthLogout drops the server session and clears the cookie. Idempotent.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.Delete(c.Value)
	}
	clearCookie(w, sessionCookieName, isSecure(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthMe reports login state to the SPA. It returns 200 in both cases
// (never 401) so the SPA's bootstrap probe doesn't trip the 401 interceptor.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeJSON(w, http.StatusOK, AuthMeDTO{Authenticated: false})
		return
	}
	sess, ok := s.sessions.Get(c.Value)
	if !ok {
		writeJSON(w, http.StatusOK, AuthMeDTO{Authenticated: false})
		return
	}
	writeJSON(w, http.StatusOK, authMeDTO(sess))
}

// authMeDTO builds the login-state DTO for a live session (shared by
// /api/auth/me and POST /api/session/hub).
func authMeDTO(sess *Session) AuthMeDTO {
	hubID, hubName := sess.SelectedHub()
	return AuthMeDTO{
		Authenticated:   true,
		User:            &UserDTO{ID: sess.Profile.Sub, Name: sess.Profile.Name, Email: sess.Profile.Email},
		SelectedHubID:   hubID,
		SelectedHubName: hubName,
	}
}

func (s *Server) redirectAuthError(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, "/?auth_error="+url.QueryEscape(reason), http.StatusFound)
}

// requireAuth gates the data API: resolve the session cookie to a valid access
// token (refreshing per-session if needed) and inject it into the request
// context, or reply 401. A 401 here is what the SPA turns into a login redirect.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		sess, ok := s.sessions.Get(c.Value)
		if !ok {
			clearCookie(w, sessionCookieName, isSecure(r))
			writeError(w, http.StatusUnauthorized, "session expired or unknown")
			return
		}
		tok, err := s.sessionToken(r.Context(), sess)
		if err != nil {
			s.sessions.Delete(c.Value)
			clearCookie(w, sessionCookieName, isSecure(r))
			writeError(w, http.StatusUnauthorized, "re-authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), tokenCtxKey, tok)
		ctx = context.WithValue(ctx, sessionCtxKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sessionToken returns a valid access token for the session, refreshing it once
// (under the session's lock) if it has expired. The lock plus the re-check of
// Valid() means concurrent requests on the same session do at most one refresh.
func (s *Server) sessionToken(ctx context.Context, sess *Session) (string, error) {
	sess.refreshMu.Lock()
	defer sess.refreshMu.Unlock()
	cur := sess.token.Load()
	if cur.Valid() {
		return cur.AccessToken, nil
	}
	if cur == nil || cur.RefreshToken == "" {
		return "", errors.New("token expired and not refreshable")
	}
	rctx, cancel := context.WithTimeout(ctx, authCtxTimeout)
	defer cancel()
	td, err := authRefresh(rctx, s.clientID, s.clientSecret, cur.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh failed: %w", err)
	}
	sess.token.Store(td)
	// Persist the rotated refresh token so a restart keeps this session usable
	// (an old refresh token would have been invalidated by APS).
	s.sessions.persist()
	return td.AccessToken, nil
}
