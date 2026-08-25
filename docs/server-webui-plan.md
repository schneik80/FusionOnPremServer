# fusionlocalserver — server mode + React/MUI web UI

> **Status — historical.** This was the original design plan for adding a
> `-server` mode and web UI alongside the (now-removed) Bubble Tea TUI. The web
> UI shipped, then a later refactor dropped the TUI entirely, made the server the
> sole front end (no `-server` flag), and replaced the single shared APS identity
> with per-user login. This document is kept only as a design record; it does
> **not** describe current behaviour. For that, see
> [`architecture.md`](architecture.md), [`authentication.md`](authentication.md),
> and [`web-ui.md`](web-ui.md).
>
> In particular, everything below that calls Fusion **Open / Insert** a `501`
> stub with disabled buttons is out of date: both actions shipped, they drive
> the user's running Fusion through its local MCP server, and they are offered
> on the details header, on every document card and in the Pins dialog.

## Context

fusionlocalserver is a Go TUI (Bubble Tea) that browses Autodesk Platform Services
(APS) data — Hubs → Projects → folder Contents → document Details. We want to run
the same capability as a **web app** so multiple users on the network can browse
through one shared server identity, and so the UI can be iterated on rapidly in
real web tech instead of a terminal.

The plan adds a `-server` switch that, instead of launching the TUI, starts an
HTTP server. The server reuses the existing, already-UI-agnostic `api/`, `auth/`,
`config/`, and `pins/` packages and serves a new React + MUI single-page app that
recreates the current three-column browser, plus new chrome the TUI lacks: a
global header, a fat left navigation rail (Hubs / Pins / Settings), and
lightbox-style dialogs for Pins and Settings. Light/Dark theming reuses the color
tokens from the sibling **PowerTools-Assembly** project. Keyboard navigation is
dropped in the web UI; everything is click-driven.

The codebase is well-suited to this: at the time of this plan `main.go` had no CLI
parsing (a clean insertion point — `main.go` now parses `-server`/`-addr`/`-dev`),
and the data/auth layers have zero TUI dependencies, so the server reuses them
directly.

### Decisions (confirmed with user)
- **Frontend**: React + MUI + Vite (TypeScript). MUI `AppBar`/`Drawer`/`Tabs`/`Breadcrumbs`/`Dialog` map 1:1 onto the described layout.
- **Network**: bind open on the LAN (default `0.0.0.0:8080`), **no auth gate**. Anyone who reaches it browses as the server's APS identity. (Log a visible warning at startup.)
- **Fusion Open/Insert + STEP download**: **stubbed** this iteration — disabled buttons in the UI; backend exposes `501` placeholders. Wire up later.

---

## Architecture overview

```
fusionlocalserver (single binary)
 ├─ default            -> TUI (unchanged)
 └─ -server -addr ...  -> HTTP server
        ├─ /api/*      JSON REST, wraps api/ + pins/, auth via TokenManager
        └─ /*          embedded React/MUI SPA (go:embed of server/webdist)

web/  (Vite + React + TS + MUI source)  ──vite build──> server/webdist/ (embedded)
```

The server holds one APS token (3-legged, reused from the TUI's cached
`~/.config/fusionlocalserver/tokens.json`) and proxies every data call. Region is
process-global (`api.SetRegion`), set once at startup.

---

## Part A — Go backend

### A1. `main.go` flag branch
Add stdlib `flag` parsing **before** building the TUI. New flags: `-server` (bool),
`-addr` (default `0.0.0.0:8080`), `-dev` (bool, serve UI from disk / Vite instead of
embedded). If `-server`, call `server.Run(server.Options{Addr, Dev, Config, CfgErr, Version})`
and return; otherwise the existing `tea.NewProgram(...)` path runs unchanged. Keep
the TUI's panic-log wrapper only around the TUI path.

### A2. New `server/` package
Package `server`, zero TUI deps, imports `api`/`auth`/`config`/`pins`.

| File | Responsibility |
|---|---|
| `server.go` | `Run(Options) error`, `Server` struct, region setup, `pins.MigrateLegacy()`, token bootstrap, `http.Server` wiring, graceful shutdown (`signal.NotifyContext` → `Shutdown`). |
| `token.go` | Concurrency-safe `TokenManager` (see A3). |
| `routes.go` | `(*Server).routes() http.Handler` — register `/api/*` first, static catch-all last; middleware chain. |
| `handlers_nav.go` | hubs, projects, project-contents, folder-contents, item-details, item-location. |
| `handlers_refs.go` | uses, where-used, drawings, classify. |
| `handlers_pins.go` | pins list/add/remove (per-hub; guard Load→mutate→Save with a mutex). |
| `handlers_stub.go` | `501` placeholders for Fusion open/insert + STEP download. |
| `dto.go` | JSON DTOs + mappers from `api.*` (the api structs have no json tags). |
| `respond.go` | `writeJSON`, `writeError`, status mapping. |
| `middleware.go` | request logging, panic recovery, dev CORS. |
| `static.go` | SPA fallback + dev disk/proxy mode; gets the SPA FS from `embeddedFS()`. |
| `static_embed.go` / `static_stub.go` | build-tag split: `embed_ui` embeds `webdist` via `go:embed`; the default tag serves an in-memory "not built" stub so plain `go build` needs no `webdist/`. |
| `webdist/` | `vite build` output; embedded only by `-tags embed_ui`. Entirely gitignored — nothing committed here. |
| `settings.go` | `server.json` (port) load/save, separate from `config.json`. |
| `handlers_settings.go` | `POST /api/settings/port` — persist port + trigger listener rebind. |

### A3. `TokenManager` (token.go) — the critical concurrency piece
Reuses existing `auth` funcs (confirmed signatures): `auth.LoadTokens() (*TokenData, error)`
(returns `(nil,nil)` if absent), `auth.Login(ctx, clientID, clientSecret)`,
`auth.Refresh(ctx, clientID, clientSecret, refreshToken)` (both internally call
`SaveTokens`), and `(*TokenData).Valid()` (nil-safe, 30s skew).

- `Bootstrap(ctx)` — runs once at startup, **before** serving:
  load cache → if `Valid()` use it → else if refresh token present, `Refresh` →
  else run interactive `auth.Login` (opens browser on the **server host**, binds
  `127.0.0.1:7879` transiently, then releases before `:8080` serving). Fail fast if
  no client_id configured.
- `Token(ctx) (string, error)` — hot path, called by every handler. Single
  `sync.Mutex` guards check-and-refresh so concurrent requests trigger **at most one**
  refresh per expiry boundary (APS rotates the refresh token on use; a double-refresh
  race would brick the cache — the mutex prevents this). Holding the lock across the
  ~hourly refresh round-trip is intentional and correct.
- No in-handler browser login: if the token expires with no refresh token mid-run,
  return `401` and require an operator restart.
- Optional: background goroutine that proactively refreshes ~1 min before expiry so
  the hourly stall never lands on a user request; `Token()` stays the fallback.

### A4. REST API route table
All under `/api/`, JSON in/out. **IDs are GraphQL URNs (contain `:` `/`) → pass as
query params, never path segments.** Region is process-global (not a param). Every
handler wraps `r.Context()` in a ~30s timeout and passes it to `tm.Token(ctx)` and the
`api.*` call. Uniform error envelope `{"error": "..."}`.

| Method | Path | Query | Wraps |
|---|---|---|---|
| GET | `/api/meta` | — | `{version, region, fusionEnabled:false, stepEnabled:false, port, portConfigurable}` |
| GET | `/api/hubs` | — | `api.GetHubs` |
| GET | `/api/projects` | `hubId` | `api.GetProjects` |
| GET | `/api/projects/contents` | `projectId` | `api.GetFolders` + `api.GetProjectItems` (concurrent; returns `{folders,items}`) |
| GET | `/api/folders/contents` | `hubId,folderId` | `api.GetItems` |
| GET | `/api/items/details` | `hubId,itemId` | `api.GetItemDetails` |
| GET | `/api/items/location` | `hubId,itemId` | `api.GetItemLocation` |
| GET | `/api/items/uses` | `cvId` *or* `hubId,drawingItemId` | `api.GetOccurrences` / drawing-source |
| GET | `/api/items/where-used` | `cvId` | `api.GetWhereUsed` |
| GET | `/api/items/drawings` | `hubId,designItemId` | `api.GetDrawingsForDesign` |
| GET | `/api/items/classify` | `cvId` | `api.ClassifyAndThumbnail` (per-row async refine; `classifySem` caps at 8; also warms the thumbnail cache off the same round trip) |
| GET | `/api/items/thumbnail` | `cvId` | `api.GetThumbnail` (status + signed URL; polled while PENDING) — *implemented* |
| GET | `/api/items/thumbnail/image` | `cvId` | same-origin PNG proxy — streams cached bytes (warmed by classify) instead of the cross-origin APS signed URL — *implemented* |
| GET | `/api/items/properties` | `cvId` | `api.GetPhysicalProperties` (mass/geometry, v2; polled while computing) — *implemented* |
| GET | `/api/pins` | `hubId` | `pins.Load` |
| POST | `/api/pins` | `hubId` | validate `pins.IsPinnable` → `Load`+`Add`+`Save`; body carries `id,name,kind,project_id,project_alt_id,folder_path` so the bookmark stays navigable (mirrors `ui/app.go` pin capture) |
| DELETE | `/api/pins` | `hubId,id` | `Load`+`Remove`+`Save` |
| POST | `/api/fusion/open`, `/api/step/download` | — | `501` stub |

DTOs in `dto.go` mirror `api.NavItem`/`ItemDetails`/`VersionSummary`/`ComponentRef`/
`DrawingRef`/`ItemLocation`/`FolderRef` with explicit camelCase `json:` tags. Carry
`componentVersionId` and `subtype` on items so the frontend can drive classify/uses
and the inline `· asm`/`· part`/`· dwg` type tags. Reuse `pins.Pin` (already a JSON
type) for pin responses.

### A5. Static embedding (build-tag split)
The `//go:embed all:webdist` lives in `static_embed.go` behind `//go:build embed_ui`
(the `all:` prefix embeds `_`/`.`-prefixed asset files). `static_stub.go` (`!embed_ui`)
supplies an in-memory `index.html` stub instead, so plain `go build` / `go test` / `go vet`
compile with **no** `server/webdist/` present and **nothing committed** there.
`static.go` is tag-agnostic: it gets the SPA FS from `embeddedFS()`, serves it with SPA
fallback (unknown non-`/api` path → `index.html`), and in `-dev` reverse-proxies the Vite
dev server (`:5173`) for HMR. `make build`/`install` run `vite build` then
`go build -tags embed_ui`; the whole `server/webdist/` tree is gitignored build output.
This removes the old committed-placeholder dance (the placeholder and the build output
were the same path, so every build dirtied the tree and had to be reverted before commit).

### A6. Logging (middleware.go)
Use stdlib **`log/slog`** (text handler → stdout; no new deps). Request middleware logs
`method, path, query, status, bytes, dur_ms, remote`. Lifecycle logs: startup
(addr/version/region/dev), auth events (cache load / refresh / interactive login /
each refresh with `expires_at`), handler errors, shutdown. Panic-recovery middleware
wraps all handlers → `500` JSON. Emit a visible WARN that the server is bound to a
non-loopback address with no auth gate.

---

## Part B — React/MUI frontend (`web/`)

Vite + React + TypeScript + MUI. Source in `web/`; `vite.config.ts` sets
`build.outDir: '../server/webdist'`, `emptyOutDir: true`.

### B1. Theming (Light/Dark, from PowerTools-Assembly)
Seed an MUI theme via `createTheme` for both `mode: 'light'` and `mode: 'dark'`, using
the PowerTools-Assembly palette (`/home/schneik/Source/PowerTools-Assembly/commands/assemblybuilder/resources/html/index.html`):
accent `#0696d7`; dark bg `#2A3442` / panels `#323E50` / text `#ffffff` / secondary
`#a0aec0`; light bg `#f4f4f4` / panels `#ffffff` / text `#333333`. Typography:
Montserrat (fallback Helvetica/Arial). Wrap app in `<ThemeProvider>` + `<CssBaseline>`;
persist mode choice in `localStorage`, default to `prefers-color-scheme`.

### B2. Iconography
Font Awesome (open source) via `@fortawesome/react-fontawesome` + free solid/brands
icon packages. Use for the left-rail icons (hubs/pins/settings), tab glyphs, pin star,
breadcrumb separators, type tags.

### B3. Layout components
- `AppLayout` — MUI `AppBar` (global header: app name, version, hub indicator, theme toggle) + permanent left `Drawer` (fat nav rail: Hubs, Pins, Settings icon buttons) + main region.
- `BreadcrumbBar` — MUI `Breadcrumbs` below header, above columns: Hub › Project › Folder… › Document; segments clickable (dispatch navigate), last (document) inert.
- `BrowserColumns` — three-pane region: `ProjectsColumn` | `ContentsColumn` | `DetailsPanel`. CSS grid/flex (~35% details, nav columns split the rest). Click selects; selecting a container loads the next column. Loading spinners per column.
- `DetailsPanel` — a header with the item name, **always-visible metadata** beside a **thumbnail**, then MUI `Tabs`. *As implemented* the metadata moved out of a tab into the header, so the tabs are **History / Properties / Uses / Where Used / Drawings**: designs get all five; configured designs get History + Properties; drawings get History + Uses; everything else is History only. Lazy-fetch + cache per item; spinner while loading. The thumbnail loads from the same-origin `/api/items/thumbnail/image` proxy.
- **Pins lightbox** — MUI `Dialog` opened from the left rail Pins icon: stub for now — render the pin list grouped by kind (fetch `/api/pins?hubId=`), star icon, Navigate/Remove actions; Open/Insert buttons disabled.
- **Settings lightbox** — MUI `Dialog` from the Settings icon: stub — theme (Light/Dark/System), region display (read-only from `/api/meta`), version/about. Stubbed controls clearly marked.
- **Hub switcher** — opened from Hubs rail icon (Dialog or Menu): list `/api/hubs`, select → reload projects + pins for that hub.

### B4. Data layer
Thin typed `api` client (`fetch`) with TS interfaces mirroring the Go DTOs. Use
React Query (TanStack Query) or simple hooks for fetching/caching/loading state.
After a folder's contents load, fire `/api/items/classify` per design row to upgrade
icons to assembly/part (mirrors TUI async refinement). All actions click-driven; no
keyboard nav.

---

## Part C — Build system

- Makefile: `web` target (`cd web && npm install && npm run build`); `build` and `install`
  depend on `web` and compile with `-tags embed_ui`; keep existing
  `CLIENT_ID`/`REGION`/`VERSION` ldflags.
- `run` target: `build` then `./fusionlocalserver -server` (binds `0.0.0.0:8080`; startup logs
  the reachable LAN URLs). `make run ARGS="-addr 0.0.0.0:9000"` to override.
- `dev` target stays Go-only and **untagged** (serves the stub UI); pair
  `./fusionlocalserver -server -dev` with `cd web && npm run dev` for HMR.
- `.gitignore`: ignore the whole `server/webdist/` tree (pure build output); nothing is
  committed there. Verify no broad `**/dist` glob misbehaves.
- If releases use `.goreleaser.yaml`, the build hook must run `make web` and pass
  `-tags embed_ui` to the Go build.

---

## Implementation sequencing
1. `server/token.go` (TokenManager) — testable against a fake `tokens.json`.
2. `server/dto.go` + `respond.go` — pure mapping.
3. `server/static.go` + committed `webdist/index.html` placeholder (package compiles).
4. `server/handlers_*.go` + `routes.go` + `middleware.go`.
5. `server/server.go` (`Run`, shutdown, logging).
6. `main.go` flag branch.
7. Makefile `web` target.
8. Scaffold `web/` Vite+React+TS+MUI project; theme; layout shell; wire to `/api`;
   then Pins/Settings lightbox stubs.

---

## Verification
- **Backend, no UI**: `go build` (uses placeholder) then `./fusionlocalserver -server`;
  watch stdout for startup + auth-bootstrap logs; `curl localhost:8080/api/meta`,
  `/api/hubs`, `/api/projects?hubId=...`, `/api/projects/contents?projectId=...`,
  `/api/items/details?hubId=...&itemId=...`, pins POST/GET/DELETE. Confirm request log
  lines (method/path/status/dur). Confirm token refresh logs after expiry (or force-expire
  the cache). `go vet ./...` and `go test -race ./...` (existing `make check`).
- **Full app**: `make build` (runs `vite build` → embeds → ldflags client_id) then
  `./fusionlocalserver -server`; open `http://<host>:8080` in a browser. Verify: header +
  left rail render; hub switch loads projects; three-column drill-down; breadcrumb
  clicks navigate; details tabs lazy-load (Uses/Where Used/Drawings); type tags refine
  to asm/part; Light/Dark toggle; Pins and Settings dialogs open from the rail (stub
  content); Fusion/STEP buttons disabled. Confirm reachable from a second machine on
  the LAN (open-network decision) and that the no-auth warning is logged.
- **TUI regression**: `./fusionlocalserver` (no flag) still launches the TUI unchanged.

## Follow-ups / TODO
- **Thumbnail image proxy (fallback).** *Done.* Rather than handing the APS
  **signed URL** straight to the browser `<img>` (a cross-origin load from
  Autodesk's CDN), the server now exposes `GET /api/items/thumbnail/image?cvId=…`:
  a handler that fetches the signed URL and streams the PNG bytes back same-origin
  (reusing the `DownloadFile` no-bearer pattern — signed URLs are self-authenticated,
  so the user token is NOT attached). Bytes are held in a bounded shared cache
  (`server/thumbcache.go`), warmed in the background off the per-row classify probe,
  so repeat views and other LAN clients are served from memory. The frontend points
  `<img>` at the proxy.
- **Physical/mass properties.** *Done.* `GET /api/items/properties?cvId=…`
  (`api.GetPhysicalProperties`, v2) backs the Details panel's Properties tab;
  generation is async, so the web UI polls while it computes.

## Critical files
- `main.go` — add `-server`/`-addr`/`-dev` flag branch.
- `auth/oauth.go`, `auth/tokens.go` — `Login`/`Refresh`/`LoadTokens`/`TokenData.Valid` wrapped by TokenManager.
- `api/client.go` (+ `queries.go`, `details.go`, `refs.go`, `locate.go`, `classify.go`) — shared `httpClient`, `SetRegion`, wrapped funcs/types.
- `pins/pins.go` — slice-functional pins API the pins endpoints wrap.
- `Makefile` — `web` build target before `go build`, preserve ldflags.
- New: `server/*.go` (incl. `static_embed.go`/`static_stub.go` build-tag split,
  `settings.go`, `handlers_settings.go`), `web/` (Vite project). `server/webdist/` is
  gitignored build output — nothing committed there.

## Notes to verify during implementation
- Exact name of the drawing-source fetch func used by the polymorphic Uses tab (the TUI's Uses tab is `GetOccurrences` for designs, drawing-source for drawings) — confirm in `api/refs.go`.
- Confirm `pins` exported names (`Load`/`Save`/`Add`/`Remove`/`IsPinnable`/`MigrateLegacy`) and `Pin`/`FolderRef` JSON tags before wiring DTOs.
