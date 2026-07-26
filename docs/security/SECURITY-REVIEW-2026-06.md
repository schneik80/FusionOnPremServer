# Security review & hardening — June 2026

Branch: `feature/activity-reports`. Primary commit: `cd1adf2`
(*security: generic error envelopes, request-body caps, and security headers*).

> **Status addendum (2026-07, pre-beta).** This is a point-in-time review;
> the codebase has moved since. What changed:
> - **M3 (non-`Secure` cookie over plain HTTP)** is mitigated in practice:
>   `-tls` is now the default posture (`make run`), with a self-signed cert
>   auto-generated and a logged warning on non-loopback plain HTTP.
> - **"No application-level rate limiter"** is no longer fully true: chat,
>   task, production, and whiteboard mutations are limited per session
>   (`chat.Limiter`, wired in `server/server.go`). `/api/auth/*` and the
>   remaining routes are still unlimited.
> - The Prompt-4 test suites listed as "untracked" below
>   (`server/fuzz_security_test.go`, `server/integration_security_test.go`,
>   `web/test/api-security.test.ts`) have since been committed.
> - Sessions now persist encrypted at rest (`sessions.enc`, AES-256-GCM),
>   and all local data is partitioned per hub with a session hub lock —
>   see [`docs/hubs/STATUS.md`](../hubs/STATUS.md).
> - **Still open**: L1 (OAuth `code`/`state` in `-v` request logs), L2
>   (`X-Forwarded-Proto` trusted unconditionally), L3 (CSRF rests on
>   `SameSite=Lax` alone). Tracked in [`SECURITY-TODO.md`](../../SECURITY-TODO.md).

A work breakdown of a security review performed across four prompts. Each
section records **the prompt that actioned it**, **what was reviewed**, **what
changed**, and — just as important — **what was deliberately *not* changed and
why**. The recurring theme: this is a hardened codebase, several "standard"
checklist items do not apply to it, and we did not fabricate findings or tests
to make them appear to.

> Scope: Go backend (`api/`, `auth/`, `server/`, `pins/`) and the React/TS SPA
> (`web/src`). Architecture is a local **BFF** — 3-legged PKCE OAuth against
> Autodesk APS, opaque server-side sessions, APS tokens never sent to the
> browser. **No database, no `os/exec`, no Node backend, no local passwords.**

---

## At a glance

| Area | Verdict | Action |
|---|---|---|
| SQL injection / string-concat queries | **N/A** — no DB/ORM anywhere | None (confirmed by code) |
| OS command injection | **N/A** — no `os/exec` | None |
| JWT forgery | **N/A** — no JWT parsed/trusted; opaque session IDs | None |
| Password hashing | **N/A** — auth delegated to APS OAuth | None |
| XSS (`innerHTML`/`dangerouslySetInnerHTML`/`eval`) | **None found** — React auto-escaping | None |
| Internal-error leakage to clients | **Fixed** | `s.fail` now returns generic, status-keyed messages |
| Missing security headers / CSP | **Fixed** | `securityHeaders` middleware (CSP, XFO, nosniff, HSTS) |
| Unbounded request bodies | **Fixed** | `MaxBytesReader` + fan-out cap on rollup/pins |
| CORS | **OK** — dev-only wildcard, never in prod | None |
| Rate limiting | **Absent (by design today)** | Documented; not implemented |
| Session cookie `Secure` over plain-HTTP LAN | **Accepted trade-off** | Documented (M3, open) |

No **Critical** or **High** findings were identified. All actioned items were
**Medium/Low** hardening. Open items are tracked at the end.

---

## Prompt 1 — OWASP Top 10 audit (Principal Application Security Architect)

> *"Audit the provided Go and TypeScript codebase focusing on the OWASP Top 10
> … missing input validation, SQLi, OS command injection, insecure goroutines,
> JWT/session validation, password hashing, XSS, CSRF, cookie flags …
> severity-ranked report with vulnerable + remediated snippets."*

**Reviewed:** session/OAuth core (`server/auth.go`, `server/session.go`,
`server/session_persist.go`, `auth/oauth.go`, `auth/tokens.go`), routing and
middleware (`server/routes.go`, `server/middleware.go`, `server/respond.go`),
the SSRF-shaped proxy paths (`api/thumbnail.go`, `api/derivative.go`), pins
persistence (`pins/pins.go`), and the SPA (`web/src`).

**Key conclusions**
- **Strong core, verified by inspection:** PKCE + S256; OAuth `state` validated
  against *both* the query param **and** a single-use, TTL-bounded pending
  cookie; opaque 256-bit CSPRNG session IDs (no fixation); per-session refresh
  serialized via mutex + `Valid()` re-check; AES-256-GCM session-file encryption;
  `io.LimitReader` byte caps on every outbound proxy; `path.Clean` on static
  serving; pins hub IDs sanitised to `[A-Za-z0-9_.-]` before `filepath.Join`.
- **Categories that do not apply** (confirmed, not assumed): no SQL/ORM, no
  `os/exec`, no JWT parsed/trusted, no local passwords, no `innerHTML`/`eval`.
- **Findings raised** (all Medium/Low): M1 missing security headers, M2
  unbounded `/api/activity/rollup` body, M3 non-`Secure` cookie under plain-HTTP
  LAN, L1 OAuth `code`/`state` logged via `RawQuery` under `-v`, L2
  `X-Forwarded-Proto` trusted unconditionally, L3 CSRF rests on `SameSite=Lax`
  only, L4 dev CORS wildcard (dev-only).

**Actioned in later prompts:** M1, M2 (and the error-leak issue surfaced in
Prompt 3). **Not actioned:** M3, L1–L4 — see *Open items*.

---

## Prompt 2 — "do M1 and M2" + header regression test

> *"do m1 and m2"* … then *"add the header regression test"*.

### M1 — HTTP security headers
**Change:** new `securityHeaders` middleware in `server/middleware.go`, wired
into the chain in `server/routes.go`
(`recoverPanic → logRequest → securityHeaders → canonicalRedirect → devCORS`).

Sets on every response:
- `Content-Security-Policy` — `default-src 'self'`; `img-src 'self' data: blob:`
  (thumbnails / canvas previews); `style-src 'self' 'unsafe-inline'` (MUI/emotion
  runtime style injection); `connect-src 'self'`; `frame-ancestors 'none'`
  (clickjacking backstop); `base-uri`/`form-action 'self'`.
- `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer`.
- `Strict-Transport-Security: max-age=31536000` **only when the request is TLS**
  (so the plain-HTTP LAN mode is not pinned to https).

**Note / deliberate carve-out:** the middleware is a **no-op under `-dev`**. The
strict CSP (`connect-src 'self'` + default `script-src`) is incompatible with
the Vite dev server's HMR websocket and inline module preamble, and dev never
faces the network. This mirrors how `devCORS` is dev-scoped.

**Note on `style-src 'unsafe-inline'`:** required by MUI's runtime style
injection today, and a nonce is not feasible with the current static (non-
templated) `index.html`. Tightening this is a future task if MUI's styling
approach changes.

### M2 — request-body / fan-out caps on `/api/activity/rollup`
**Change** (`server/handlers_activity.go`):
- body read through `http.MaxBytesReader(w, r.Body, 1<<20)` (1 MiB);
- `const maxRollupChildren = 2000`; over-limit `childItemIds` → `400` *before*
  any APS fan-out.

**Why:** each child id becomes a GraphQL call to APS under the user's token with
a 5-minute timeout — an unbounded list is an authenticated amplification vector.
Mirrors the existing cap on `handleSetPort`.

### Tests — `server/middleware_test.go`
`TestSecurityHeadersProd` (headers + CSP directives present, HSTS absent on
HTTP), `TestSecurityHeadersHSTSOverTLS` (HSTS present on TLS),
`TestSecurityHeadersDevNoOp` (all headers absent under `-dev`). The CSP check is
a `strings.Contains` spot-check on load-bearing directives so cosmetic
reordering won't false-fail, but loosening one will.

---

## Prompt 3 — secure-by-default refactor (Senior Go backend engineer)

> *"Apply these constraints rigidly … input validation library; parameterized
> queries; prevent leaking internal errors (generic 500 + internal logging);
> strict CORS & headers. Refactor … add unit tests for payload malformations
> and SQL injection payloads."*

This prompt's template assumed a SQL backend and a validation framework. We
applied the constraints **that map to real code** and pushed back on the rest.

### Actioned — constraint #3 (error-handling leak): the one real issue
**Change** (`server/handlers.go`): `s.fail` previously logged the full error
**and** sent `err.Error()` to the client. On the default `502` path that chain
can carry raw APS/GraphQL response bodies and internal URLs.

- Now returns a generic, **status-keyed** message via `safeErrorMessage(status)`
  (`authentication required` / `you do not have access…` / `rate limited…` /
  `upstream request timed out` / `upstream service error`). One change fixes all
  ~25 `s.fail` call sites.
- The full error is still logged server-side.
- **Follow-up prompt** *("preserve the richer messages but only with -v")*: under
  `-v` (`s.opts.Verbose`) the detailed error is appended to the client message —
  a developer-run diagnostic, never the production default. Reuses the existing
  `-v` flag that already gates request tracing and debug routes.
- Also dropped the appended `err.Error()` from the two `400` decode paths
  (`handlers_settings.go`, `handlers_pins.go`).

### Actioned — constraint #1 (input validation): closed the last unbounded body
`handlePinsAdd` now decodes through `http.MaxBytesReader(w, r.Body, 64<<10)`.
Existing manual field validation (required `id`, `IsPinnable(kind)`) is kept.

### Actioned — constraint #4 (CORS & headers)
Already delivered by M1. `devCORS` wildcard is `-dev`-gated and not combined
with credentials, so production is unaffected. No change.

### Not actioned — and why
- **Parameterized queries / SQLi tests:** there is **no SQL sink**. Re-verified:
  no `database/sql`, ORM, or query string-building; APS GraphQL calls pass user
  input as typed variables, never concatenation. Writing SQLi tests against code
  with no SQL would be theater. *Instead* we added a test proving an
  injection-style payload is treated as **inert data** end-to-end (see Prompt 4).
- **`go-playground/validator`:** declined. Adding a struct-tag validation
  framework for 2–3 tiny JSON bodies cuts against the codebase's explicit
  `reqParam`/manual-check convention (per `CLAUDE.md`) and would lower
  consistency, not raise security. Revisit only as a deliberate project-wide
  convention change.

### Tests — `server/handlers_security_test.go`
`TestFailDoesNotLeakInternalError` (sensitive substrings never reach the client;
message is generic), `TestFailIncludesDetailWhenVerbose` (`-v` appends detail),
`TestSafeErrorMessageByStatus`, `TestPinsAddRejectsMalformedJSON` /
`…OversizedBody` / `…UnpinnableKind`, and `TestInjectionPayloadIsStoredVerbatim`
(a `'; DROP TABLE …; $(rm -rf /); <script>` payload round-trips byte-for-byte —
the honest "no injection sink" proof).

---

## Prompt 4 — fuzzing & integration test suites (QA Security Engineer)

> *"Generate fuzzing and integration test cases … malformed JSON; boundary
> values (huge ints, null bytes, long strings); brute-force/rate-limiting on
> login and password-reset endpoints. Provide Go (testing) and TypeScript
> (Supertest/Playwright) scripts."*

Again, item 3 assumed endpoints that don't exist. We built genuine suites for
what's real and were explicit about the gap.

### Go — `server/fuzz_security_test.go` (runnable; fuzz with `-fuzz`)
- `FuzzPinsAddBody`, `FuzzRollupBody` — native Go fuzzers feeding arbitrary
  bytes to the JSON-body handlers. Invariant: **never panic, never 5xx** — only
  contract statuses (`400`, or `401` for well-formed-but-unauthenticated).
- `TestSetPortBoundaries` — integer boundaries (`-1, 0, 80, 1023, 65536`, int64
  max, beyond-int64 overflow) rejected; avoids any valid *different* port so no
  real bind/save/restart fires.
- `TestNullBytesAndLongStringsArePinData`, `TestRollupChildCountBoundary` — null
  bytes inert; body cap and the exact 2000-child boundary enforced.

### Go — `server/integration_security_test.go` (full middleware stack via `httptest.Server`)
- Security headers end-to-end; data routes `401` with a JSON envelope; malformed
  body hits the **auth gate before parsing**; unknown `/api` → JSON `404`.
- `TestIntegration_OAuthCallbackRejectsForgedState` — forged / replayed /
  no-cookie `state` is redirected to `/?auth_error=…` and **never mints a
  session cookie**. This is the auth-flow equivalent of a brute-force/replay
  attempt; the defense is the single-use, cookie-bound, 256-bit state.

### TypeScript — `web/test/api-security.test.ts` (Supertest + Vitest, against a running server)
Mirrors the three categories against the live HTTP surface, with an
`FLS_SESSION` env hook to exercise authenticated payload-validation paths rather
than just the `401` gate. **Standalone** — the repo has no JS test tooling yet;
the file header documents `npm i -D vitest supertest @types/supertest` and the
run command. Not wired into `package.json` (separate decision).

### Not actioned — rate limiting & password brute-force (the honest part)
There is **no application-level rate limiter** and **no password / password-reset
endpoint** (auth is delegated OAuth; the only `429` handling concerns *consuming*
APS's rate limit downstream). A green "rate limiter fires" test would be
fabricated. *Instead*, both suites include a **characterization probe**
(`TestIntegration_NoRateLimiterPresent_Characterization` and the TS `rate
limiting` block) that bursts 60 requests, asserts the *current* reality (0
throttled, all `< 500`), and logs the gap — honest and green today, and the
exact place to flip the expectation once a limiter ships.

---

## Files changed / added

**Committed in `cd1adf2`:**
- `server/handlers.go` — `s.fail` generic messages + `safeErrorMessage` + `-v` detail
- `server/handlers_activity.go` — rollup body cap + `maxRollupChildren`
- `server/handlers_pins.go` — pins body `MaxBytesReader`; drop decode-error leak
- `server/handlers_settings.go` — drop decode-error leak
- `server/middleware.go` — `securityHeaders` middleware
- `server/routes.go` — wire `securityHeaders` (carried one already-staged debug-route line)
- `server/middleware_test.go`, `server/handlers_security_test.go` — tests

**Untracked (test suites from Prompt 4, not yet committed):**
- `server/fuzz_security_test.go`
- `server/integration_security_test.go`
- `web/test/api-security.test.ts`

All Go: `gofmt` clean, `go build ./...` ok, `go test ./...` green.

---

## Open items (reviewed, not actioned)

| ID | Item | Severity | Why deferred |
|---|---|---|---|
| **M3** | Session cookie `Secure` flag is request-derived; over plain-HTTP LAN (bind `0.0.0.0`) the token is sniffable | Medium | Documented trade-off; default `make run` is TLS. Fix = refuse non-loopback HTTP, or bind loopback, or require a TLS proxy. |
| **L1** | OAuth `code`/`state` logged via `RawQuery` under `-v` | Low | Single-use, PKCE-bound code; redact `code`/`state` in `logRequest`. |
| **L2** | `X-Forwarded-Proto` trusted from any client | Low | Low impact (mostly over-hardens); gate on a trusted-proxy flag. |
| **L3** | CSRF rests on `SameSite=Lax` only (no token / Origin check) | Low | Lax blocks cross-site cookie POSTs; add an `Origin` check on mutating verbs as a backstop. |
| **L4** | Dev CORS wildcard | Info | `-dev`-only, no credentials; acceptable. |
| — | **No rate limiter** | — | Not a regression; offered as a follow-up (per-IP limiter on `/api/auth/*` + mutating routes, then convert the characterization probes to real assertions). |

## Methodology note

Every "N/A" above was confirmed by searching the code (e.g. grep for
`database/sql|gorm|os/exec|jwt|bcrypt|innerHTML|rate.?limit`), not assumed from
the prompt template. Where a prompt asked for work with no corresponding sink
(SQLi tests, validation framework, rate-limiter assertions), we said so plainly
and substituted the honest equivalent rather than producing tests that pass
against nothing.
