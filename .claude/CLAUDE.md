# fusionlocalserver — Claude Code context

A local **BFF**: a Go HTTP server that signs the user into Autodesk Platform
Services (APS) and serves a React SPA for browsing **Fusion Team** data
(hubs → projects → folders → designs), with details, version history,
references (uses / where-used / drawings), thumbnails, BOM, and pins.

## Layout
- `auth/` — 3-legged PKCE OAuth against APS; per-session in-memory tokens (auto-refresh). **Never persist tokens.**
- `api/` — APS clients: Manufacturing Data Model **GraphQL** (`client.go`, `queries.go`, `details.go`, `refs.go`, …). **Design activity** is GraphQL-sourced (`activity_graphql.go` → `activity_report.go`); `activity.go` keeps the shared types + `HubSlug` (the notifications feed it once used is first-party-gated — removed).
- `server/` — Go 1.25 `net/http.ServeMux`; routes in `routes.go`; handlers `handlers_*.go`; DTOs in `dto*.go`; session/auth middleware (`fls_session` cookie).
- **Local per-project stores** — features whose data is ours, not APS's. All share one posture: one JSON/JSONL file per project, atomic writes via `internal/atomicfile`, a per-project mutex, `.bak` on corruption, a future-version guard, a **schema provenance stamp** (`internal/schemameta` — createdAt/createdByVersion/updatedAt/updatedByVersion) on a versioned envelope, per-store migration registries (`internal/migrate` — v(n)→v(n+1) steps with pre-migration `.vN.bak` snapshots), and authorization delegated to `chat.Authorizer` (APS project role → capability) rather than a parallel permission system.
  - **HUB ISOLATION (security invariant — hubs are IP boundaries between clients):** every store roots under `hubs/<hubslug>/<store>/` (see `docs/hubs/STATUS.md`). The server holds one lazily-built store-set per hub; the session locks to a hub (`POST /api/session/hub`); the `requireHub` middleware wraps every data route (409 `hub_not_selected`, 403 `hub_mismatch`); handlers resolve stores from the **session**, never from client-supplied hubId. Pre-isolation layouts are unsupported (the one-time startup migration was retired after the only deployed server migrated). Never add a code path that reads another hub's profile.
  - `chat/` — append-only channel logs + the shared `Authorizer` / `Limiter`.
  - `tasks/` — `tasks.json` per project (Kanban + Gantt schedule: start/end dates, progress, dependsOn, milestone, stage). See `docs/tasks/STATUS.md`.
  - `production/` — `production.json` per project (jobs, step DAG, version-pinned documents, batches). Steps come in two kinds: a **step** carries documents, a **decision** carries colour-coded **results** and no documents, and each result is the source of its own edges (`Edge.FromResultID`). Schema v3; see `docs/production/STATUS.md`.
  - `whiteboards/` — tldraw boards: `whiteboards.json` metadata per project plus one `doc-<id>.json` per board, since a document is megabytes and is rewritten on every autosave. See `docs/whiteboards/STATUS.md`.
  - `pins/` — per-hub pin files inside the hub profile.
  - `notifications/` — a first-party in-app notification center (the app-chrome bell). Same store posture but keyed by **user** (OIDC sub), not project: one `<userkey>.json` per user under `hubs/<slug>/notifications/`, holding a capped inbox. Emitted by our own write paths — chat `@mentions` (via the `fls:user` token, parsed server-side, no APS call), task assignment, task due-soon/overdue (reconciled at inbox-fetch time, no scheduler), and production batch changes. The server is the sole author; clients read/mark-read/dismiss. See `docs/notifications/STATUS.md`.
- `backup/` — per-hub GFS backup engine (7 daily / 4 weekly / 12 monthly) with sha256 manifests carrying schema versions and hub identity (ManifestVersion 2), verify (re-hash + parse + version), and restore (pre-restore safety snapshot, secret-merge for config.json, refuses foreign-hub / pre-isolation / path-escaping manifests, restarts via the port-change rebind flow). Config is per-hub (`hubs/<slug>/backup.json`); allow-list sources only — `sessions.enc`/`session.key`/TLS keys/logs are unreachable by construction and `config.json` is captured with `client_secret` blanked. See `docs/backup/STATUS.md`.
- `web/` — React 18 + Vite + TypeScript + MUI v6 + @tanstack/react-query (+ recharts). API wrapper `src/api/client.ts`, hooks `src/api/queries.ts`. Project apps live one folder each (`src/tasks/`, `src/wiki/`, `src/chat/`, `src/production/`) and mount as tabs in `components/ProjectPanel.tsx` under the contract `({ active }: { active?: boolean })` — every tab stays mounted and gates its fetching on `active`. Their left rails share one width (`APP_RAIL_WIDTH`) and one header (`components/RailHeader.tsx` — title left, `New` button right, optional search below); add rail controls there, not per app.
  - **i18n** — i18next; catalogs `src/i18n/locales/<locale>/<namespace>.json` (en source of truth + de/fr/es/it/pt, MT-seeded; see `docs/i18n/STATUS.md`). Semantic keys only, never English-as-key; enum tokens render via `src/i18n/enums.ts`; server errors carry a `code` mapped by `src/i18n/apiError.ts`; the eslint ratchet (`npm run lint:i18n`) forbids literal strings in extracted folders. User-entered data is never translated. Grapheme-safe helpers in `src/fmt/graphemes.ts` — never `.slice()` user text or take `s[0]` for initials.
  - **Hub session** — the SPA runs locked to one hub: `HubGate` after login (remembered hub auto-relocks), switching lives in Settings → Connection with a full teardown+reload (`state/teardown.ts`). Theme mode + custom colors are per-hub (`state/hubKeys.ts`); a 409 `hub_not_selected` tears down to the gate.
  - **Settings console** — `components/settings/` master-detail dialog: Appearance (theme, language, custom colors), Connection (port, region, hub switch), Uptime, Logs, Backups, Data (disk usage, per-project deletion, cleanup). See `docs/admin/STATUS.md`.
- `config/` — `APS_CLIENT_ID` / `APS_CLIENT_SECRET` / `APS_REGION` (env or `~/.config/fusionlocalserver/config.json`). Build-time `config.Default{ClientID,Region,PublicURL}` are injected via ldflags from `.aps-client-id` / `.aps-region` / `.aps-public-url` (git-ignored); `DefaultPublicURL` bakes in the canonical OAuth callback host so the binary needs no `-public-url` flag.

## Build / test / run
```
go build ./...        # backend
go test ./...         # all unit tests (offline)
cd web && npm install && npm run build   # frontend → embedded into server/webdist via //go:embed
make run                                 # build UI + binary, serve over HTTPS (-tls is on by default)
./fusionlocalserver -tls                 # or run the built binary directly (HTTPS; self-signed cert auto-generated)
# dev: (t1) go run . -dev   (t2) cd web && npm run dev   (Vite proxies /api)
```
`server/webdist` is gitignored and embedded at compile time — **build the web before `go build`** for the UI to ship in the binary.

## Conventions
- Go: `gofmt`; handlers use `reqParam` / `s.reqCtx` / `s.token` / `s.fail` / `writeJSON`; DTOs camelCase with `fmtTime`, slices never nil.
- **IDs ride in query params, never path segments** — URNs contain `:` and `/`.
- Web: typed `request()` wrapper, react-query hooks (bump the persist `buster` in `main.tsx` when query shapes change). Realtime/per-user query keys (`chat*`, `task*`, `prod*`) are excluded from localStorage persistence — see the dehydrate filter in `main.tsx`.
- **APS calls are quota'd, so never fan out per row.** The per-minute cost quota answers a burst with 429s, and `api/client.go` deliberately does *not* retry them (a retry can't replenish a per-minute budget). Anything per-item — a classify, a thumbnail — waits for the row to near the viewport via `components/useInView.ts`; anything per-container is capped with a visible "Load all" (`ACTIVITY_CAP` / `CLASSIFY_CAP` in `Dashboards.tsx`). Never cap silently.
- **One stylesheet, one exception.** There are no CSS files except `web/src/whiteboards/whiteboard.css`, which reskins tldraw (a CSS-variable-themed component that cannot be styled through `sx`). It is scoped to `.fls-tldraw`. Everything else is MUI `sx`.
- **Visualizations are hand-drawn inline SVG** — there is no graph/chart library beyond one recharts donut, no framer-motion, and no CSS files. Motion is MUI `<Slide>` plus short (100–120 ms) `sx` transitions. `RelationGraph.tsx` (pan/zoom + bezier edges), `HistoryGraph.tsx` (lanes) and `ActivityHeatmap.tsx` (isometric) are the reference implementations.
- **No date library either.** `components/DateField.tsx` is the app's date picker (read-only field + hand-drawn month-grid popover, Monday-first everywhere); whole-day helpers live in `src/fmt/dates.ts`. Don't add `@mui/x-date-pickers` — it needs a peer date lib too.
- **Card tokens** — `fls:doc` / `fls:task` / `fls:job` / `fls:batch` / `fls:whiteboard` are compact pseudo-URL tokens stored inline in chat/wiki/task bodies and unfurled at render time. `components/RefCard.tsx` maps every scheme to its renderer; `components/reftokens.ts` splits them out of plain text; `components/RefTokenDialog.tsx` maps a token straight to its dialog. Adding a scheme means touching those three plus `chat/MessageList.tsx` (its `ChatBody` destructures the union) and `wiki/Markdown.tsx` (its `a:` override) — nowhere else. Server-side, `internal/docref` reads the `fls:doc` tokens for the **reverse** lookup — `GET /api/items/local-refs`, the Where-Used tab's local sources (tasks/chat/whiteboards/jobs/batches that reference a document). One `GetProjects` call for the accessible-project scope, then raw-bytes-prefiltered local scans; wiki pages are excluded because they live in APS, not a local store (see `docs/api.md`).
- Commit/push only when asked.

## Active work
**Production P6** (on `main`) — decisions and editing ergonomics, the most
recent wave: duplicate a job (plan only, never its runs); a run date on batch
creation via the new shared `components/DateField.tsx`; double-click rename and
chain-from-selection on the flow canvas; **decision nodes** — a second step kind
carrying colour-coded results, each routing its own edge — and per-run hidden
steps. This bumped the production store to **schema v3** with a deliberately
no-op migration (every added field's zero value is its legacy meaning; the
version moves only so an older binary refuses the file instead of silently
erasing the new fields on its next save). See `docs/production/STATUS.md`.

Previously: **Hub isolation + admin platform** (branch `admin`, merged to main):
1. **Admin platform**: full i18n (six locales, dynamic switching, error codes
   on the wire, Unicode hardening — `docs/i18n/STATUS.md`), schema hardening
   (versioned envelopes + provenance stamps + shared migration framework),
   and the Settings admin console (appearance/custom themes, connection,
   uptime, rotated logs, GFS backups with verify/restore, per-project data
   deletion + disk usage).
2. **Hub isolation** (`docs/hubs/STATUS.md`): hubs are IP boundaries — all
   local data partitions under `hubs/<hubslug>/`, the session locks to one
   hub, every data route enforces it, and backups/restore/settings are
   per-hub (pre-isolation layouts and backups are unsupported). **Any new
   feature that stores or lists local data must go through the session's
   store-set.**

Previously: **Whiteboards** — a per-project tldraw board (fifth project app)
with live `fls:` app cards and assembly expansion; tldraw is lazy-loaded and
**needs a licence key** (`VITE_TLDRAW_LICENSE_KEY` in the git-ignored
`web/.env.local`; evaluation licence expires 2026-10-29 — see
`docs/whiteboards/STATUS.md`).

Previously: **Production** on branch `Production` — a light MES / product tracker, the fourth
project app beside Tasks, Wiki and Chat. A **Job** is a graph of **Steps** carrying
version-pinned plan documents and placeholder slots; a **Batch** is a dated run
that *freezes* the plan (steps, pinned versions, placeholders are deep-copied), so
later plan edits can never rewrite what a run recorded. Documents are supplied by
browsing the hub or uploading, and every pin resolves its version server-side
(`api/production_snapshot.go`). UI in `web/src/production/`: a pan/zoom SVG flow
canvas, a list view, batches with a prove/production timeline, plus a cross-project
screen on the nav rail. See **`docs/production/STATUS.md`**.

Previously: **Design activity** (branch `feature/activity-reports`) — a per-design
**Activity tab** (`web/src/components/ActivityHeatmap.tsx`), an isometric heat map
off the GraphQL design report (`/api/activity/report?scope=design&hubId=…&id=…`).
The hub/project/folder dashboard was **removed** (the notifications feed it relied
on is first-party-gated). See **`docs/activity-reports/STATUS.md`**.
