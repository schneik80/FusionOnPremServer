> **SUPERSEDED (historical).** The notifications-feed-backed hub/project/folder dashboard this plan describes was built and then **removed** — the feed is first-party-gated (returns HTTP 500 for this app's token). Only the per-design GraphQL activity report shipped. See [STATUS.md](STATUS.md).

# Plan: Hub / Project / Folder / Design Activity Reports — built into `fusionlocalserver`

## Context

We want activity reports/dashboards at four levels of the Autodesk **Fusion Team** hierarchy —
**hub → project → folder → design** — covering: change-over-time (filterable by hour/day/month/year),
contributors, date created, last change, version count, milestone count, comments, and document
references (uses / where-used / drawings). A throwaway Python prototype (`Personal/Activity analysis/`)
proved the value by parsing a *pasted* Fusion Team activity feed, but it only modeled two levels and was
brittle.

**Target codebase (decided): `~/Source/fusionlocalserver`.** It is a Go **BFF** + **React/Vite** app
that already implements most of the hard parts, so we build the reports as a *feature inside it* rather
than greenfield:
- **Auth** (`auth/`): 3-legged PKCE OAuth against APS; per-session in-memory tokens with auto-refresh;
  scope `data:read user-profile:read` (read-only reports need nothing more). Handlers get the bearer via
  `s.token(ctx, w, r)`.
- **API client** (`api/`): shared tuned `httpClient`; GraphQL helper `gqlQuery` (retry/backoff,
  `X-Ads-Region`); **Manufacturing Data Model GraphQL** queries for the hierarchy
  (`queries.go`: `GetHubs/GetProjects/GetFolders/GetProjectItems/GetItems`), per-design **version
  history** with authors (`details.go`: `GetItemDetails` + itemVersions, `parseTime`, `fullName`), and
  **references** (`refs.go`: `GetOccurrences`=uses, `GetWhereUsed`, `GetDrawingsForDesign`,
  `GetDrawingSource`). `NavItem.Kind ∈ {hub,project,folder,design,drawing}`.
- **Server** (`server/`): Go 1.22 `net/http.ServeMux`; routes `mux.HandleFunc("GET /api/…", prot(h))`;
  handler pattern `reqParam → reqCtx → token → api.X → s.fail / writeJSON`; DTOs + mappers in `dto.go`
  (`fmtTime`, `make([]T,0,len)`); `requireAuth` session middleware (`fls_session` cookie).
- **Web** (`web/`): React 18 + Vite + TS + **MUI v6** + **@tanstack/react-query v5** (persisted to
  localStorage, buster `fls-1`) + Font Awesome. Thin typed fetch wrapper (`src/api/client.ts`,
  `credentials:'same-origin'`, 401→login), hooks in `src/api/queries.ts`, theming in `theme.ts`
  (accent `#0696d7`). No router — navigation via `NavProvider` reducer + dialog state in `AppLayout`;
  views: 3-column browser (`DetailsPanel` already has History/Properties/BOM/Uses/WhereUsed/Drawings
  tabs), plus Hubs/Pins/Settings dialogs. **No charting library yet.**

**Decisions locked:** stack = the repo's Go + React (supersedes the earlier "Node/TS greenfield"
answer); hub = Fusion Team `imallc` (`forgeId a.YnVzaW5lc3M6aW1hbGxj`); auth = existing OAuth, no scope
change. The user also asked to **consolidate our plan/docs/prototype into this repo** (see Phase 0).

---

## What's already done vs. what's new

| Capability | Status in `fusionlocalserver` | Action |
|---|---|---|
| OAuth + per-request bearer | ✅ `auth/`, `s.token()` | reuse as-is |
| Hub/project/folder/item hierarchy | ✅ `api/queries.go` (GraphQL) | reuse |
| Per-design version history + authors (→ contributors, created, last-change, version count) | ✅ `api/details.go` itemVersions | reuse |
| References: uses / where-used / drawings | ✅ `api/refs.go` | reuse |
| **Chronological activity feed** (events over time, last-actor, project, folder, version, views/likes/comments) | ❌ none | **NEW `api/activity.go`** (Notifications feed) |
| Milestone count | ❌ none | **NEW** GraphQL query in `queries.go` — spike (confirm fields) |
| Comment text | ❌ none | feed `links rel=comment` when `postCount>0` — spike |
| Activity report endpoints | ❌ none | **NEW `server/handlers_activity.go`** + routes + DTOs |
| Dashboard UI (time-series, contributors, filters) | ❌ none | **NEW `web/src/components/ActivityDashboard.tsx`** + recharts |

**Key insight:** the only new *acquisition* is the feed; the DM "enrichment" tier the earlier plan
called for is already implemented as GraphQL. The feed is the spine (chronological events + absolute
timestamps + full hierarchy in each object); `details.go`/`refs.go` enrich a selected design.

---

## Confirmed feed contract (captured from `imallc`, 2026-06-23)

```
GET https://developer.api.autodesk.com/fusionteam/notifications/v2/hubs/{hub}/feeds/network/@me
    ?count=40&start={(page-1)*40}&page={n}      # Authorization: Bearer <APS token> (data:read)
```
- Envelope `{ startIndex, count, totalObjects, objects[], links.link{rel:"nextPage", href} }` — loop
  pages until no `nextPage`. Auth **confirmed**: standard APS Bearer token, same type the app issues.
- Object `@type:wipDioWidgetObject` (`type:DATA`) = file/version event; `@type:activityFeedDataObject`
  (`type:COMMUNITY`) = lifecycle event (e.g. *project created*, event HTML in `title.content`).
- Field → model mapping (every level present in each object):
  - hub: `hub.hubId`, `hub.name`, **`hub.forgeId`** (= APS DM hub id, bridges to GraphQL/DM)
  - project: `publishedTo.{id, publishedToName, publishedToUrl}` (Group)
  - folder: `parentFolderUrn` (`urn:adsk.wipprod:fs.folder:co.…`)
  - design: `id`/`permalinkId`, **`lineageUrn`**, `tipVersionUrn`, `displayTitle`/`fileName`, `fileType`
  - created `creationTime` (ms); last-change `lastModified`/`changeTime`/`lastActivity.time` (ms, absolute)
  - version count `version`; last-actor `lastActivity.{accountId,displayName}` (≠ `owner` → multi-contributor)
  - bonus: `views.{views,viewers}`, `postCount`, `likeCount`; drill-down `links.link[]` (`versions`,`comment`)
- Action verb: infer (`version==1`&`creationTime≈changeTime`→uploaded/created; `>1`→updated; COMMUNITY→parsed).
- Fallback if endpoint ever changes: parse the `<project-activity-feed>` DOM (relative timestamps only).

---

## Architecture (inside the repo)

**Backend — acquisition (`api/activity.go`, NEW):**
- `GetActivityFeed(ctx, token, hubID) ([]FeedEvent, error)` — REST GET loop (pattern from
  `api/thumbnail.go`: `http.NewRequestWithContext` + `Authorization: Bearer` + `X-Ads-Region`), paginate
  `start/count/page` until no `nextPage`; decode into typed structs (use `parseTime`/`fullName` helpers).
- Normalize to repo-style structs: `ActivityEvent{EntityID, EntityType, Timestamp, Actor, Action,
  VersionNumber, ProjectID, FolderURN, LineageURN, Source}` and `ActivityEntity{Type, ID, Name, …}`.

**Backend — aggregation (`api/activity.go` or `internal/activity/`, NEW):**
- Compute per-scope: time-buckets (hour/day/month/year), contributor list (distinct actors + counts +
  first/last), version count, created/last-change, references count. Scope filters reuse feed fields
  (`publishedTo.id`, `parentFolderUrn`, `lineageUrn`) — no extra calls needed for the core report.
- Enrich a selected **design** with full contributor history via `api.GetItemDetails` (itemVersions) and
  references via `api.GetWhereUsed`/`GetOccurrences`/`GetDrawingsForDesign` (already built).

**Backend — endpoints (`server/handlers_activity.go` + `routes.go` + `dto.go`, NEW):**
- `GET /api/activity/report?scope={hub|project|folder|design}&id=<id>&hub=<hubId>&bucket={hour|day|month|year}&from=&to=`
  → handler follows `handlers_refs.go` shape (`reqParam`/`reqCtx`/`s.token`/`s.fail`/`writeJSON`);
  register with `prot(...)`. DTOs (camelCase, `fmtTime`, `make([]T,0,len)`) + mapper funcs in `dto.go`.

**Frontend — dashboard (`web/`, NEW):**
- `npm i recharts` (only missing dep; pairs with MUI/TS).
- `web/src/api/{types,client,queries}.ts`: add `ActivityReport` types, `api.activityReport(...)`,
  `useActivityReport(scope,id,filters)` (react-query; bump persist buster `fls-1`→`fls-2`).
- `web/src/components/ActivityDashboard.tsx`: time-series chart (filter hour/day/month/year), contributor
  list/bar, per-level breakdown, references + milestones + comments panels; MUI `Paper`/`Stack`, theme
  tokens for colors; reuse `Column` for metric cards.
- Navigation: add a **"Activity"** button to `NavRail.tsx` and a `currentView:'browser'|'dashboard'`
  field to the `NavProvider` reducer; `AppLayout` renders `<ActivityDashboard/>` vs `<BrowserColumns/>`
  (recommended over a dialog, since the dashboard is a primary view). Scope follows current nav
  selection (hub/project/folder/design).

---

## Phased roadmap

**Phase 0 — Consolidate + verify env.** Move our artifacts into the repo (user-requested):
copy this plan → `fusionlocalserver/docs/activity-reports/plan.md`; copy the feed schema notes +
capability table into the same folder; copy `Personal/Activity analysis/*`, the two pasted `*RC*…md`
feeds, and the captured feed JSON → `docs/activity-reports/reference/` as fixtures/prior art; add a
`project` memory pointing future sessions at this repo + the feed endpoint. Confirm the app builds/runs
(`go build`, `cd web && npm i && npm run build`) and that **our app's** 3-legged token reaches the feed
(read-only probe; captured sample used the web client's `client_id`).

**Phase 1 — Feed acquisition.** Implement `api/activity.go` (paginated fetch + typed structs +
normalization). Unit-test against the captured fixture JSON (`api/testdata/activity_feed.json`) — assert
event count, hierarchy extraction, timestamps, version numbers, COMMUNITY parsing.

**Phase 2 — Aggregation + model.** Time-bucketing, contributor rollups, counts; scope filters; merge in
`details.go`/`refs.go` enrichment for a selected design. Table-tested.

**Phase 3 — Endpoints.** `handlers_activity.go` + routes + DTOs; handler test following
`handlers_*_test.go` patterns.

**Phase 4 — Dashboard.** recharts + `ActivityDashboard.tsx` + api/queries wiring + NavRail/view nav +
hour/day/month/year filters.

**Phase 5 — Enrichment spikes.** Milestone count via Manufacturing Data Model GraphQL (add query in
`queries.go`; confirm fields exist); comment text via feed `rel=comment` when `postCount>0`.

**Phase 6 — Validate end-to-end** against `imallc`.

---

## Verification

- `go test ./...` (feed parser against fixture; aggregation; handler tests).
- `cd web && npm run build` (tsc + vite) clean.
- Run `./fusionlocalserver -tls` (serves HTTPS), sign in, open the Activity view; confirm the
  time-series filters and contributor list render for hub/project/folder/design scopes.
- **Ground-truth cross-check:** for a known design (e.g. `_CEBIT_LASTPENDEL_FINAL`, feed shows `version:10`)
  confirm version count, contributors (note "Kevin Schneider" vs "Kevin Schneider IMA"), created and
  last-change dates match the Fusion Team web UI.

## Security

Read-only reports; existing `data:read` scope suffices — no auth changes. Tokens stay in-memory per
session (existing model); **never persist tokens** to disk/repo/memory; treat pasted tokens as secrets
and keep them out of commits/fixtures (scrub the captured JSON of any token before saving).

## Open items (resolve during work)

- Confirm our app's `client_id` token is accepted by the (undocumented) feed endpoint — Phase 0 probe
  (expected yes; it's a standard APS-gateway Bearer call).
- Milestone fields in the Manufacturing Data Model GraphQL schema — Phase 5 spike.
- Whether project/folder-scoped feed endpoints exist (`feeds/<type>/<scope>`); otherwise filter the
  network feed by `publishedTo.id` / `parentFolderUrn` / `lineageUrn` (works today).
- Dashboard as top-level view (recommended) vs dialog — confirmed default: top-level view + NavRail button.

## Key reuse points (named)

`auth`/`server`: `s.token`, `reqParam`, `s.reqCtx`, `s.fail`, `writeJSON`, `fmtTime`, `prot`, `requireAuth`,
`routes.go`. `api`: `httpClient`, `SetRegion`/`X-Ads-Region`, REST pattern in `thumbnail.go`, `parseTime`
+ `fullName` (`details.go`), `GetItemDetails`/itemVersions, `GetWhereUsed`/`GetOccurrences`/
`GetDrawingsForDesign`, `NavItem`. `web`: `src/api/client.ts` (`request`,`qs`), `src/api/queries.ts`
(`useQuery`), `theme.ts` tokens, `NavProvider`, `Column`, dialog pattern (`PinsDialog`).
