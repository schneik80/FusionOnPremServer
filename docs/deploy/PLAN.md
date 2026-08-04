# Production deployment — public hosting, admin whitelist, release-driven deploys

> **STATUS: in progress.** This is a living document — the checklist below is
> updated as each step lands, with notes on deviations and findings.

## Status

- [x] **0. This plan document** (`docs/deploy/PLAN.md`)
- [x] **1. Workstream A — admin whitelist**
  - [x] Config: `admin_users` in `config.json` + `FLS_ADMIN_USERS` env override — loads in *all three* `Load()` layers; `readFile` split from validation so a whitelist-only `config.json` works beside env/ldflags credentials. Note: a malformed `config.json` now fails startup even when `APS_CLIENT_ID` is set (it carries a security setting; silently ignoring it would fail open).
  - [x] Enforcement: OAuth callback — `userAllowed` gate before `sessions.Create`, fail-closed on a zero profile (`server/auth.go`)
  - [x] Enforcement: `requireAuth` revocation — delete session + clear cookie + 401
  - [x] Enforcement: persisted-session restore filter (`SessionStore.allowed`, wired before `EnablePersistence`)
  - [x] Enforcement: `/api/auth/me` (outside `requireAuth`; deletes + reports unauthenticated)
  - [x] Frontend: `not_allowed` → `login.errors.notAllowed`, all six locales
  - [x] Tests: 11 new (callback allow/deny/fail-closed, revocation ×2, restore filter, config precedence ×4, `userAllowed` table) — all green; `go vet` clean; web build + `lint:i18n` clean
  - [x] Docs: `docs/authentication.md` "Admin whitelist" section; future restricted-role task in `docs/admin/STATUS.md`
- [x] **2. Workstream B — deploy artifacts**
  - [x] `deploy/`: `fusionlocalserver.service`, `env.example`, `Caddyfile.example`, `sudoers-deploy`, `bootstrap.sh` (adds ufw + sshd hardening + Caddy install)
  - [x] `docs/deploy/production-server.md` (runbook incl. rollback + schema warning)
  - [x] `.github/workflows/deploy.yml` (reusable; pinned-host-key SSH; version-asserting smoke test)
  - [x] `release.yml`: `deploy` job (stable tags only, `needs: release`, not gated on mac-installer)
  - Verified: YAML parses; `actionlint`/`shellcheck` not available on this machine — worth a pass in CI or on the Linux box before first use.
- [ ] **3. Server provisioning** (operator, runbook-guided): order the VPS, DNS, bootstrap, env + whitelist, first install, APS callback registration, GitHub secret/variables
- [ ] **4. First release** containing the whitelist, tagged *before* DNS goes live
- [ ] **5. Pipeline verification**: prerelease-skip proven, dispatch deploy of a test tag, rollback rehearsal, first automatic stable deploy

### Findings along the way

- **Windows test-suite hazard (pre-existing, partially fixed):** many tests
  fake the home dir with `t.Setenv("HOME", …)`, but `os.UserHomeDir()` on
  Windows reads `USERPROFILE` — so on a Windows machine parts of
  `go test ./server` read *and write* the real
  `%USERPROFILE%\.config\fusionlocalserver` (it accumulates test artifacts:
  fake hubs, port 9000, a test logo). Fixed for `./config` (a `setHome`
  helper sets both vars; the 0700 assertion is skipped on Windows — full
  package green on Windows now). `./server` still has this; run its full
  suite on Linux. The polluted profile dir on the Windows dev box is test
  junk and safe to delete.
- `SECURITY-TODO.md` briefly vanished from the working tree during this
  session (cause unknown, nothing in this work touches it); restored via
  `git restore`.

## Context

The server is moving from a LAN tool to a small-team deployment on a public
host. Requirements settled with the operator:

- **No VPN client on team devices** — the app is reached at a normal HTTPS URL.
- **Hosting at DreamHost** (the team's main site already lives there):
  **Self-Managed VPS "Stack 4"** — 2 vCPU / 4 GB / 75 GB NVMe, Ubuntu, full
  root ($8.99/mo intro → $15.99/mo). DreamHost's managed VPS/shared plans have
  no root/sudo and cannot run a systemd daemon or Caddy. Fallback options:
  DreamCompute 2 GB ($12/mo) or Hetzner CX22 (~€5/mo). The runbook is
  provider-agnostic (any root Ubuntu VM).
- **Access gate: admin whitelist** (`admin_users`). Whitelisted emails are
  admins. Everyone else will eventually get a **restricted role — a future
  task** (see `docs/admin/STATUS.md`). Until that lands, non-listed users
  cannot sign in at all: today *every* authenticated session has admin powers
  (logs, restores, data deletion), so an ungated public login page is not
  acceptable.
- **Deploys are push-based over plain SSH** from GitHub Actions on each stable
  release tag; the released `linux-amd64` artifact is downloaded and
  checksum-verified, never rebuilt.

Constraints from the code that shaped everything:

- **One process, one persistent disk.** All stores live under
  `~/.config/fusionlocalserver` (hardcoded); concurrency control is in-process;
  multi-instance is a documented non-goal. Sessions persist encrypted
  (`sessions.enc`), so restarts/deploys do **not** log the team out.
- **Long-lived SSE streams** (chat, whiteboards) — the proxy must not time out
  streaming responses.
- **The binary binds `0.0.0.0:8080` unconditionally** (`server/server.go:435,443`)
  — the host firewall (ufw) is what keeps 8080 off the internet.
- **OAuth callback host is exact-match** registered with APS; `-public-url`
  must be passed at runtime (released binaries bake only the client id).
- ~1 GB memory floor (whiteboard live rooms worst case ~768 MiB).

## Workstream A — admin whitelist

**Config** — `admin_users` (JSON array of emails and/or OIDC subs) in
`config.json`; env override `FLS_ADMIN_USERS` (comma-separated), following the
`APS_REGION` cross-layer pattern. The `APS_CLIENT_ID` env branch of
`config.Load()` returns early without reading `config.json` — the whitelist
must load in both branches. Startup-only by design: no settings-UI editor
(there are no admin roles yet, so a UI editor would let any user edit the
list), and the file on disk is the lockout recovery path.

**Semantics (v1, interim)** — non-empty list: only listed users may establish
a session; all sessions keep today's full powers (the whole team is listed
initially). Empty list: open access — backward compatible with the local/LAN
posture. The runbook makes a non-empty list a precondition for DNS going live.

**Matching** — `Profile.Sub` exact, or `Profile.Email` case-insensitive
(emails are stored unnormalized and may originate from the legacy `emailId`
claim — `auth/userinfo.go`; same convention as `notifUserKey`).

**Fail closed** — the userinfo fetch is deliberately non-fatal today
(`server/auth.go:182-185`): on failure, login proceeds with a zero profile.
With a non-empty whitelist an empty sub+email must **deny**, otherwise a
userinfo outage is an auth bypass.

**Enforcement points (all four):**
1. **Callback** — after the profile fetch, before `sessions.Create`
   (`server/auth.go:185-187`): deny → `redirectAuthError(w, r, "not_allowed")`.
2. **`requireAuth`** (`server/auth.go:249`) — the choke point for every data
   route: no-longer-listed session → delete + clear cookie + **401** (the SPA
   auto-bounces to login on 401; the re-login shows the authored message).
3. **Persistence restore** (`server/session_persist.go` `load()`) — restore
   bypasses the callback; filter removed users or they survive forever.
4. **`/api/auth/me`** — registered outside `requireAuth`; needs its own check.

**Frontend** — `LoginScreen.tsx` `authErrorKey`: map `not_allowed` →
`login.errors.notAllowed` (six locales). Unmapped reasons already degrade to
the generic sign-in error.

**Future task (recorded, not built): restricted role.** Session carries
`isAdmin` from the whitelist; non-listed users may sign in but server-level
admin surfaces (settings/port, logs, backups/restore, data deletion, branding
writes) are gated; the SPA hides those tools. Project data is already limited
by APS project-role authorization (`chat.Authorizer`), so non-listed sign-in
becomes safe once this lands. `admin_users` is named so the relaxation is
additive — no config migration.

## Workstream B — hosting + deploys

**Host layout** (provisioned by `deploy/bootstrap.sh`, idempotent, root):
- Users: `fls` (system, nologin, home `/var/lib/fusionlocalserver` — app data
  in its `~/.config/fusionlocalserver`) and `deploy` (SSH target, owns
  `/opt/fusionlocalserver/bin`, so the atomic binary swap needs no sudo).
- `deploy/fusionlocalserver.service`: `User=fls`,
  `EnvironmentFile=/etc/fusionlocalserver/env` (root:root 0600 — read by
  systemd before privilege drop; neither `fls` nor `deploy` can read the APS
  secret), `ExecStart=… -public-url ${PUBLIC_URL}`, `Restart=on-failure`,
  `TimeoutStopSec=15` (the server drains SSE + shuts down in ≤10 s on
  SIGTERM), `NoNewPrivileges`, `PrivateTmp`.
- Sudoers (exact-match, single line):
  `deploy ALL=(root) NOPASSWD: /usr/bin/systemctl restart fusionlocalserver.service`.
- **ufw**: default deny incoming; allow 22/80/443. sshd hardening:
  `PasswordAuthentication no`, `PermitRootLogin prohibit-password`.
- **Caddy** (official apt repo): `fls.<domain> { reverse_proxy 127.0.0.1:8080 }`
  — auto Let's Encrypt, HTTP→HTTPS, preserves `Host`, sets
  `X-Forwarded-Proto` (the app derives Secure cookies/HSTS from it). The app
  runs plain HTTP, no `-tls`.
- DNS: A record `fls.<domain>` → VPS IP (DreamHost panel). APS app redirect
  URI: `https://fls.<domain>/api/auth/callback`.

**Deploy workflow** — `.github/workflows/deploy.yml`, reusable
(`workflow_call` from `release.yml` + `workflow_dispatch` with a `tag` input —
the dispatch path doubles as redeploy/rollback). Job outline:
fail-fast config check → `gh release download` (linux-amd64 archive +
`checksums.txt`) → `sha256sum --check --ignore-missing` → pinned-host-key SSH
setup (`DEPLOY_SSH_KEY` secret, `DEPLOY_KNOWN_HOSTS` variable,
`StrictHostKeyChecking=yes`) → `scp` to `…/bin/fusionlocalserver.new` →
remote: hardlink current → `.prev`, atomic `mv`, `sudo systemctl restart` →
smoke test: `GET /api/meta` must report the deployed version (public endpoint;
retry ~30 s). Serialized via `concurrency: deploy-production`.
**No auto-rollback**: local stores use forward-only schema migrations with a
future-version guard, so a blind rollback can make stores refuse to load —
failures report loudly and rollback is a deliberate dispatch of an older tag.

`release.yml` gains: `deploy` job, `needs: release`,
`if: ${{ !contains(github.ref_name, '-') }}` (stable tags only — matches
GoReleaser's `prerelease: auto` heuristic), `secrets: inherit`. Not gated on
`mac-installer` (a notarization flake must not block the Linux deploy).

GitHub configuration: secret `DEPLOY_SSH_KEY`; Actions variables
`DEPLOY_HOST`, `DEPLOY_KNOWN_HOSTS`.

**Runbook** (`docs/deploy/production-server.md`) additionally covers:
rollback paths under the forward-only-migration warning; the port caveat
(changing the app port in Settings breaks Caddy's `127.0.0.1:8080` target);
monitoring via `/api/meta`; app-level GFS backups to a separate directory and
offsiting it (no provider snapshots on this product); whitelist-before-DNS.

## Verification

1. `go build ./... && go test ./...`; `cd web && npm run build`,
   `npm run lint:i18n`.
2. Local: `FLS_ADMIN_USERS` excluding self → not-authorized message; including
   self → signs in; removed + restart → persisted session not restored.
3. Post-bootstrap: `ssh deploy@fls.<domain>`; `sudo systemctl restart …`;
   `curl https://fls.<domain>/api/meta`; port 8080 refused externally.
4. Throwaway prerelease tag: deploy job skipped (gate proven).
5. Dispatch-deploy the test tag: full chain through the version-asserting
   smoke test. Rollback rehearsal with the prior stable tag; delete test tag.
6. First real stable tag: automatic `release → deploy → smoke test`.
