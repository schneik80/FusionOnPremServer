# Security Follow-ups

Originally findings from the security review of 2026-05-02. The high-severity
items (H1, H2) and the Python-injection mitigation (M2) shipped first. The
dedicated-server refactor (per-user login, TUI removal) then resolved or
obviated most of the rest. This file tracks what remains.

## Resolved

- [x] **M1. OAuth `state` parameter (CSRF).** The per-user login flow generates
  a random `state`, stores it server-side keyed to the in-flight login, and
  rejects the callback unless the `state` query param matches both that entry
  and the `fls_pending` cookie. See `server/auth.go` and `docs/authentication.md`.
- [x] **M3. Redact signed URLs from debug logs.** `api/debug.go` runs every
  trace line through `redactSignedURLs` before it reaches the console or log
  file, so `"signedUrl":"…"` values never appear in `-v` output.
- [x] **L3. Refresh `golang.org/x/text` / indirect deps.** Resolved by the
  TUI removal, which dropped `x/text`. The module has since deliberately
  taken on exactly two small dependencies — `golang.org/x/sync`
  (singleflight) and `gopkg.in/natefinch/lumberjack.v2` (log rotation) —
  both pinned in `go.sum` and easy to keep current.
- [x] **L4. Bump the `go` directive.** Now `go 1.25.0`.
- [x] **TLS / `Secure` cookie.** `-tls` serves HTTPS (self-signed cert
  auto-generated/cached, or bring your own via `-tls-cert`/`-tls-key`); the
  session cookie's `Secure` flag is driven by `r.TLS`, so it is set whenever a
  request arrives over HTTPS. Closes the plaintext-cookie-sniffing exposure on
  the LAN when `-tls` (or a TLS-terminating front) is used.
- [x] **Session persistence across restarts.** Sessions are mirrored to
  `~/.config/fusionlocalserver/sessions.enc`, AES-256-GCM encrypted under a key
  file (`session.key`, 0600). This is encryption-at-rest of the refresh tokens,
  not OS-keychain storage (see below).
- [x] **L1 (2026-06). OAuth `code`/`state` in `-v` request logs.** `logRequest`
  runs the query string through `redactQuery` (`server/middleware.go`), which
  blanks `code`, `state`, `code_verifier`, and the token/secret parameter names
  and redacts an unparseable query whole. Benign params still log verbatim.
- [x] **L2 (2026-06). `X-Forwarded-*` trusted from any client.** A trusted-proxy
  boundary (`server/proxy.go`): forwarded proto/host/for are honored only when
  the immediate peer is loopback or inside a `-trusted-proxy` CIDR. Loopback is
  always trusted, so the Caddy deployment (`deploy/Caddyfile.example`) needs no
  configuration, while a LAN client can no longer force the `Secure` cookie flag
  or HSTS. `clientIP` reads `X-Forwarded-For` under the same rule — right to
  left, skipping trusted hops — which is what gives the auth limiter below a
  real per-user key behind a proxy.
- [x] **L3 (2026-06). CSRF rested on `SameSite=Lax` alone.** `requireSameOrigin`
  (`server/middleware.go`, in the chain after `canonicalRedirect`) refuses
  `POST`/`PUT`/`PATCH`/`DELETE` whose `Origin` — or `Referer`, when `Origin` is
  absent — is a different site. A request carrying neither header is allowed:
  browsers always send `Origin` on cross-site mutations, so nothing exploitable
  passes, and non-browser clients keep working. No-op under `-dev`.
- [x] **Rate limiter on `/api/auth/*`.** The pre-session routes are metered per
  client IP (`perIP` in `server/routes.go`) using the same `chat.Limiter` bucket
  the app routes use per session: login/callback/logout at 10 burst refilling
  one per 2 s, `/api/auth/me` generously (5/s, burst 60) because the SPA
  re-probes it on every window focus. `/api/meta` stays unmetered on purpose.

## Obviated by the architecture change

- **L2 / L5. `OpenBrowser` URL-scheme validation / error surfacing.** The server
  no longer opens a browser on the host (`OpenBrowser` was deleted with the
  loopback login). Each user authenticates in their own browser.

## New — deferred

- [ ] **APS app callback registration.** APS validates `redirect_uri` by
  exact match (no wildcards). Use **`-public-url`** to fix the callback to one
  canonical URL and register just that — the server redirects clients arriving
  via other hosts to it. Without it, the callback is derived per origin and each
  must be registered (`localhost` ≠ `127.0.0.1`, each LAN IP/hostname is
  separate, `-tls` makes it https). Still operator/portal work (and tied to the
  broader APS client_id/secret provisioning), so it stays deferred — but
  `-public-url` reduces it to a single registration.
- [ ] **Stronger token-at-rest (L1).** Persisted sessions are AES-256-GCM
  encrypted, but the key sits beside the data (`session.key`, 0600) — this
  defends a casual file read, not an attacker with the user's home directory. OS
  keychain / DPAPI / secret-service storage of the key (or the sessions) would
  be stronger.

## Open items from the 2026-06 review

**All closed** (see *Resolved* above): L1 log redaction, L2 the trusted-proxy
boundary, L3 the same-origin CSRF backstop, and the `/api/auth/*` rate limiter.
The review's own open-items table in
[`docs/security/SECURITY-REVIEW-2026-06.md`](docs/security/SECURITY-REVIEW-2026-06.md)
is annotated accordingly; M3 (non-`Secure` cookie over plain-HTTP LAN) remains
the documented trade-off it always was, mitigated by TLS being the default
posture. What is left in this file is the two deferred items above — both
operator/platform work rather than code.
