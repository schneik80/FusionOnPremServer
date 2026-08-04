# Settings / admin console — status

The Settings dialog is the server's admin console: a master-detail dialog
(`web/src/components/settings/SettingsDialog.tsx`, shell cloned from the hub
browser) with a tool list on the left and the selected tool on the right.
Each tool receives `active` (dialog open **and** tool selected) so polling
queries only run while their screen is visible. Callers can deep-link to a
tool (the AppBar hub label opens straight to Connection).

**Gating (settled product decision):** every `/api/admin/*` route — and
`/api/settings/port` — runs behind the standard authenticated session plus
the hub lock (`protHub` in `server/routes.go`). There are no extra
server-side admin roles on this single-user local server; destructive
operations layer **typed confirmations** in the UI (and the backup restore
enforces its confirmation server-side too).

## Per-hub vs global settings

Hubs are IP boundaries ([hubs](../hubs/STATUS.md)); settings split
accordingly:

| Per hub | Global |
|---|---|
| Theme mode + custom colors — hub-scoped localStorage keys (`state/hubKeys.ts: resolveHubScopedKey`), a deliberate anti-mixup cue between clients' hubs | Listen port (`server.json`, server-side) |
| Backup config — `hubs/<hubslug>/backup.json` ([backups](../backup/STATUS.md)) | APS region (`config.json`, read-only in the UI) |
| All data the Data tool lists/deletes (session hub's profile only) | Language (`fdc.locale` localStorage) |

## The tools

### Appearance — `AppearanceTool.tsx`
Theme preference (light/dark/system), language (six locales, switches live),
and per-mode custom color tokens (background/panel/border/text/accent…).
Color edits apply live — `App.tsx` rebuilds the MUI theme from the overrides
— and target the currently resolved mode; a reset restores defaults. Theme
state is per-hub, language is global.

### Connection — `ConnectionTool.tsx`
- **Hub lock** — the ONLY place hubs are switched: lists the user's hubs with
  the session's lock marked; applying re-locks via `POST /api/session/hub`
  (no re-OAuth), saves `fls.lastHub`, then runs the shared teardown + reload
  (`state/teardown.ts`).
- **Port** — `POST /api/settings/port` (`server/handlers_settings.go`):
  1024–65535 only, rejected with 409 in dev mode (the server doesn't own the
  port) or when the port is busy (pre-checked with a test bind). On success
  the server persists `server.json`, replies `{restarting:true}`, and rebinds
  ~0.5 s later; the client waits ~2.5 s and navigates to the new port. The
  same reply-then-rebind mechanism is reused by backup restore.
- **Region** — read-only display; changing region means editing config and
  restarting.

### Uptime — `UptimeTool.tsx`
`GET /api/admin/status`, polled every 30 s while visible: start time, uptime,
requests served, app + Go version, log path and size.

### Backups — `BackupsTool.tsx`
The per-hub GFS backup console — config, manual run, snapshot table, verify,
restore. Documented in [backup/STATUS.md](../backup/STATUS.md);
`DirPickerDialog.tsx` is its server-side folder browser
(`GET /api/admin/fs/dirs` — directories only, hidden dirs excluded, files
never listed).

### Logs — `LogsTool.tsx`
`GET /api/admin/log` serves the tail of `server.log` (default 64 KiB, capped
at 512 KiB, aligned to a line start so it never opens mid-line);
`?download=1` streams the whole current file as an attachment. Logging itself
(`server/logging.go`): slog to stdout + `~/.config/fusionlocalserver/server.log`,
**rotated by lumberjack at 10 MB, keeping 3 compressed generations**; `-v`
raises the level to debug (per-request lines + redacted GraphQL tracing).
TLS-handshake noise from port probes is demoted to debug.

### Data — `DataTool.tsx`
Disk usage and local-data lifecycle for the **session hub's profile only**
(`server/handlers_admin_data.go`). Deleting here never touches Fusion
documents or the APS project — only this server's local app data.

- `GET /api/admin/disk` — per-store, per-project sizes for the four app
  stores (chat, tasks, production, whiteboards) plus the pins pseudo-store;
  project identity comes from each store's self-describing envelope file (the
  only file whose *contents* the walk reads — everything else is sizes), with
  everything else in the profile lumped into `otherBytes`.
- `DELETE /api/admin/projects/data?projectId&apps=chat,tasks,…` — per-project
  deletion across the named stores. The whole apps list validates before
  anything deletes (a typo can't half-delete); each store's `DeleteProject`
  is idempotent; the UI requires typing the project name.
- `POST /api/admin/cleanup` — allow-listed stale-artifact cleanup: legacy
  pre-hub cruft at the config root (`tokens.json`, `debug.log`,
  `server-restart.log`, `models/`) and `.bak`/`.tmp` files **older than 30
  days** across the session hub's four store dirs (suffix allow-list only —
  live data, sessions, TLS material and the server log are never candidates).

## API

```
GET    /api/admin/status                 uptime, counters, versions, log size
GET    /api/admin/log                    ?tailBytes (≤512 KiB) | ?download=1
GET    /api/admin/backups                \
POST   /api/admin/backups/run             |
GET    /api/admin/backups/config          |  see docs/backup/STATUS.md
POST   /api/admin/backups/config          |
POST   /api/admin/backups/verify          |
POST   /api/admin/backups/restore        /
GET    /api/admin/fs/dirs                ?path — dirs-only browse (backup picker)
GET    /api/admin/disk                   per-store / per-project usage (session hub)
DELETE /api/admin/projects/data          ?projectId&apps=…  idempotent per app
POST   /api/admin/cleanup                allow-listed stale artifacts
POST   /api/settings/port                {port} → {restarting} + listener rebind
```

## Residual risks / known gaps

- **Any authenticated session is an admin.** That is the settled single-user
  posture, but on a shared deployment every project member who can log in can
  read logs, delete local project data, and trigger restores (each destructive
  step still needs its typed confirmation). The interim mitigation for public
  deployments is the **`admin_users` sign-in whitelist** (see
  [`authentication.md`](../authentication.md)): non-listed users cannot sign
  in at all.

## Future work: restricted role for non-whitelisted users

Planned relaxation of the `admin_users` gate (not yet built): whitelisted
emails stay full admins; everyone else may sign in with a **restricted role**.

- Session carries `isAdmin`, resolved from the whitelist at creation (and
  re-checked in `requireAuth`, so removal demotes live sessions).
- Server-side gating of the admin-only surfaces for non-admins: settings/port,
  admin status + log tail, backups/verify/restore, per-project data deletion,
  `fs/dirs` browsing, branding writes (see the `protHub` admin routes in
  `server/routes.go`).
- The SPA hides the corresponding Settings tools for non-admins.
- Project data needs no new gating — `chat.Authorizer` already maps the APS
  project role to capabilities, so a signed-in non-member sees nothing.
- `admin_users` keeps its name and shape — the relaxation is additive, no
  config migration.
- `GET /api/admin/fs/dirs` browses the server's filesystem (directory names
  only, no files) from any authenticated session — inherent to picking a
  backup folder, same trust model as above.
- The log tail endpoint reads only the current `server.log`; rotated
  `.gz` generations aren't downloadable through the UI.
- Cleanup's 30-day `.bak` age gate means a corrupt-store `.bak` you might
  still want is protected, but also that junk lingers a month.
- Theme/color and language settings are browser-local (localStorage) — they
  don't roam between devices.

## Verifying

```
go test ./server/...                 # admin, data-tool, backup-handler, settings tests
cd web && npx tsc --noEmit && npm run build
```

End-to-end: open Settings → flip theme + a custom color (confirm it applies
live and survives reload, per hub) → switch language → check Uptime counters
tick → refresh and download the log → change the port and reconnect → in
Data, delete one project's chat data (typed confirm) and run cleanup → switch
hubs in Connection and confirm the full teardown + relock.
