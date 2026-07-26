# Activity reports — implementation status

Branch: `feature/activity-reports`. Plan: `plan.md`. Feed contract: `feed-contract.md`.

## Done (building green, unit-tested offline)

**Backend (Go)**
- `api/activity.go` — paginated Fusion Team Notifications feed client + normalization to
  `ActivityEvent` (full hub→project→folder→design hierarchy, absolute timestamps, last-actor vs owner,
  version, views/comments/likes, COMMUNITY lifecycle events). `HubSlug` derives the feed's hub slug
  from the GraphQL hub's AltID / WebURL.
- `api/activity_report.go` — `BuildReport(events, scope, id, bucket, from, to)`: scope filtering
  (hub/project/folder/design), time-bucketing (hour/day/month/year), contributor rollups, design &
  version counts, created/last-change, child breakdown, capped recent-events.
- `server/handlers_activity.go` + `server/dto_activity.go` + route — `GET /api/activity/report`
  (`hub`, `scope`, `id`, `bucket`, `from`, `to`). `ItemDTO.slug` added (hubs) so the UI gets the slug.
- Tests: `api/activity_test.go` (pagination/normalization vs real-shaped fixtures in
  `api/testdata/`), `api/activity_report_test.go` (aggregation), `server/handlers_activity_test.go`.

**Frontend (React/Vite/MUI + recharts)**
- `web/src/components/ActivityDashboard.tsx` — time-series bar chart (hour/day/month/year toggle),
  summary stat cards, contributors, drill-down breakdown (drills via feed-native child ids), recent
  activity table. Scope follows the selected hub; drills into projects→folders→designs.
- `web/src/api/{types,client,queries}.ts` — `ActivityReport` types, `api.activityReport`,
  `useActivityReport`. Nav rail gains a Browser/Activity view toggle (`NavRail`, `AppLayout`).
  Query-cache buster bumped `fls-1` → `fls-2`.

## Feed is dead for this app — pivoted to GraphQL (2026-06-23)

Live validation answered the Phase 0 open risk **NO**: the undocumented Fusion Team notifications
feed returns **HTTP 500** for our own APS app's token (nginx/text-html/empty body, no
`WWW-Authenticate`; request byte-identical to the working web-client capture except the token).
Broadening OAuth scopes made no difference — it is first-party-gated. So the feed-backed acquisition
(`api/activity.go`) is unusable here. See memory `activity-feed-token-rejected`.

**Acquisition rebuilt on the Manufacturing Data Model GraphQL** — the token that works everywhere
else in the app. `BuildReport`/DTOs/UI are unchanged (still consume `[]ActivityEvent`); only the
acquisition layer swapped.

- ✅ **Design scope** — `api/activity_graphql.go` `GetDesignActivity` (lean `item` + `itemVersions`
  query; each version → one `ActivityEvent`). `GET /api/activity/report?scope=design&hubId=<gqlHubId>&id=<lineageUrn>`.
  **Validated** 2026-06-23 against `Cylinder Body` (hub `autodesk8083`): 4 versions, 1 contributor,
  created 2026-05-18, last change 2026-05-22 — matches GraphQL source. Test: `api/activity_graphql_test.go`.
  - Note: query omits `fusionWebUrl` — MFG intermittently 500s that one leaf field, and gqlQuery's
    retry logic then discards the whole (otherwise complete) response. Lost the design deep-link for now.

## Scope capped to design-only (2026-06-23)

The hub/project/folder dashboard was **removed** — its acquisition was the first-party-gated feed
and the approach is DOA. The feature is now a per-design **Activity tab**:

- ✅ **Activity tab** (`web/src/components/ActivityHeatmap.tsx`) on designs/configured/drawings — an
  isometric heat map (inspired by isometric-contributions). Day/Week/Month/Year pick a *window* (24
  hours / 7 days / day-calendar / full-year day-calendar); a prev/next stepper walks windows, clamped
  to the design's activity span. Bar height + colour encode change count; sparse axis labels; render
  bounded 800–1200 px. Off `/api/activity/report?scope=design&hubId=…&id=…`.
- 🗑️ **Removed:** `ActivityDashboard.tsx`, the NavRail Browser/Activity toggle, `useActivityReport` /
  `api.activityReport`, the feed client (`api/activity.go` `GetActivityFeed` + wire types + tests/
  fixtures). `/api/activity/report` is design-only; `api/activity.go` keeps only shared types + `HubSlug`.

## Assembly rollup — `POST /api/activity/rollup`

Merges a design's activity with its child documents' activity server-side (`server/routes.go` →
`server/handlers_activity.go` `handleActivityRollup` → `api.RollUpDesignActivity` in
`api/activity_graphql.go`). Design scope only, like the report endpoint.

- **Body:** `{ hubId, itemId, childItemIds[] }` — the caller enumerates descendants
  (`GET /api/items/descendants`) and passes the distinct child lineage ids.
- **Caps:** body limited to 1 MB; `maxRollupChildren = 2000` (each child id becomes one APS GraphQL
  call under the user's token, so an unbounded list would be an authenticated amplifier). Fan-out runs
  with bounded concurrency (`rollupFanout = 12`) under a 5-minute timeout.
- **Hub check:** the hub rides in the request *body*, so session-hub equality is enforced in the
  handler (before any APS fan-out) rather than by `requireHub`'s query-param check.
- **Result:** parent + child events aggregated into one report, labelled as the parent design's
  (`scope=design`, `scopeId=itemId`, day buckets).

## Possible follow-ons (not planned)

- `gqlQuery` partial-data fix (return usable data on a leaf-field `UNKNOWN` error instead of retrying)
  — also fixes the Details panel's flaky `fusionWebUrl`.
- Richer per-change events (property / part-number changes) via the typed-history GraphQL query —
  currently one event per saved version (`itemVersions`).
- Milestone count via `isMilestone` across `itemVersions`.

## Run

```
make run                                  # build UI + binary, serve over HTTPS (-tls default)
# or: go build ./... && ./fusionlocalserver -tls
# or dev: (terminal 1) go run . -dev   (terminal 2) cd web && npm run dev
```
Open the app (https://<host>:8080), pick a hub, click the **Activity** rail icon.
