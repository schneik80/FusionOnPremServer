# APS quota budget — STATUS

The app's one shared answer to Autodesk Platform Services rate limits:
every APS call goes through `internal/apsbudget`, a process-wide points
budget with a priority queue, a cooldown driven by real 429s, and a
seconds-only coalescing cache. The SPA learns about slow mode from headers
on the traffic it already makes and shows one banner.

## The facts this rests on

- **Quota**: 6000 query points per minute **per app client_id** on the
  Manufacturing Data Model GraphQL API (v2 and v3 share it), so every user
  of this server — and any other client on the same client_id, such as the
  Fusion add-in — draws from one bucket. A single query may not exceed
  **1000 points** (HTTP 400, deterministic; never retried). Data Management
  and OSS REST endpoints meter *requests* per minute per endpoint, so they
  form a separate lane.
- **A 429 body** is a GraphQL error whose message reads
  `Query point value per minute quota exceeded with point value 231 and
  remaining quota 69` (also seen as `point value of 26`; the parser accepts
  both). Both numbers are exact and are the only budget
  telemetry MDM exposes today; `extensions.pointValue` (already on the AEC
  Data Model) is read opportunistically when it arrives. Platform 429s may
  carry `Retry-After`.
- **Costs — measured, per route.** 1854 real 429s in the server log (to
  2026-09-07) name the rejected query's exact cost. `api/cost.go` carries
  those as `Measured`; the static model (`10×Roots + Fixed +
  (RowFields+1)×Limit`, calibrated on the drawings query) is only the
  fallback for an op that has never been rejected. What the numbers show:

  | route | op | measured |
  |---|---|---|
  | items/classify | ClassifyAndThumbnail | 26 |
  | items/thumbnail | GetThumbnail | 20 |
  | activity/report (50 versions) | DesignActivity | 122 |
  | folders/contents | GetItems | 261 |
  | projects | GetProjects | 366 |
  | hubs (a list of 4) | GetHubs | 311 |
  | items/uses (50 rows) | GetOccurrences | 916 |
  | items/where-used (50 rows) | GetWhereUsed | 666 |
  | items/drawings | GetDrawingsForDesign | 576 |
  | items/bom | AllOccurrences | 266 |
  | items/details | GetItemDetails | 211 (short history) – 436 |
  | items/location | LocateItem | 11–14 |
  | items/properties | GetPhysicalProperties | 28 |
  | custom-properties | GetCustomProperties | 15 |
  | a 7-member roster | PM | 66 |
  | wiki (hub DM id) | HubDMID | 11 |

  Two rules fall out. **Most connections are charged per returned row**
  (an activity report with 50 versions is 122, not the 633 the model gave;
  details is 211 with a short history and 436 with a long one), so the
  transport reconciles every paged call by the `results` rows it actually
  carried (`OpCost.ActualForRows`, `countResults`) and the bucket is only
  charged what the gateway charges. **A few are charged on the requested
  page** — four hubs cost 311 every time — and are flagged `ByLimit`. A
  paged op's 429 observation is recorded but never replaces its full-page
  admission estimate (one short page says nothing about the next long one);
  a fixed op's observation wins outright.
- Since 2026-08-17 MDM points are metered and billed, so points are money
  as well as throttle.

## Design (`internal/apsbudget`)

- **Bucket** (`bucket.go`): capacity 6000 points — the whole quota, refilled
  continuously at 100/s. It started at 80 %, and a conservative bucket on
  top of conservative estimates starved user-blocking calls into 504s in the
  first real run; headroom for background work comes from the reserve
  floors instead. May go negative after an under-estimate. Resynced
  *downwards only* from the `remaining quota` a 429 reports.
- **Scheduler** (`scheduler.go`): strict priority heap, FIFO within a
  class. `P0` user-blocking (navigation, details, tab opens), `P1`
  in-viewport per-row work (classify, thumbnail), `P2` background
  (prefetch, warm, upload/archive jobs, debug). `MaxInFlight` 12 GraphQL /
  6 REST. **Reserve floors** P0 0 %, P1 10 %, P2 35 % of capacity must
  remain *after* a take, so a click always finds something left. **Maximum
  queue wait** P0 = the request's own deadline, P1 10 s, P2 2 s; a request
  whose expected wait (cooldown, or the refill its cost needs **after the
  work already queued ahead of it at its priority or higher**) exceeds that
  is refused *immediately* with a typed `RateLimitError{Queued:true,
  RetryAfter}` — a real countdown — and one that is starved while queued is
  refused the same way just *before* its deadline, so the client sees a 429
  with Retry-After and never a 504. A slot is held for exactly one HTTP
  round trip; a paginated walk takes one per page.
- **Cooldown**: a real 429 puts its lane into cooldown for `Retry-After`,
  else 20 s, escalating ×1.5 on a repeat within a minute up to 60 s. A P0
  waits it out (bounded by its deadline); P1/P2 are refused with the
  countdown.
- **Coalescing cache** (`coalesce.go`): singleflight + LRU (1024 entries /
  32 MiB), TTL per operation from the registry — 15 s listings/details, 30 s
  scope (hubs, projects, members), 5 s per-row probes, 12 h the hub DM id.
  **The key always carries the subject (OIDC sub)** unless the op is
  registered `Shared`; two users of one hub see different projects, folders
  and members, and serving one user's bytes to another would be a leak.
  `Shared` is only for ACL-free facts of a hub the caller is already locked
  to (thumbnail status, the DM id). A subject-less context (CLI probes,
  tests) is never coalesced. `WithFresh` skips a stored hit for explicit
  refreshes; the chat authorizer reads rosters fresh because it *is* the
  roster cache and a revocation must not wait a coalescing TTL. Our own
  writes (wiki publish/rename/restore, a finished upload) invalidate the
  author's entries.
- **Fan-out bound** (`api/fanout.go`): one shared semaphore (12) for the
  descendants walk, the activity roll-up and the permissions path — across
  every request, not per call. Lock order: fan-out slot → scheduler slot,
  never the reverse. A walk that skips per-node errors **aborts on a
  RateLimitError**: a silently shorter tree is the "never cap silently"
  violation and every further node would spend a quota that is gone.
- **Typed errors** (`errors.go`): `RateLimitError{RetryAfter, Lane,
  PointValue, Remaining, Queued}` and `QueryTooComplexError`. `server/
  respond.go` maps them by `errors.As`, never by string; `s.fail` answers
  429 with a `Retry-After` header and `retryAfterMs` in the envelope and
  logs at Warn (upstream) or Info (locally queued) — a rate limit is
  expected behaviour in slow mode, not a fault.

## Wire contract (for the SPA)

| Direction | Where | Meaning |
|---|---|---|
| request | `X-FLS-Priority: 0\|1\|2` | Overrides the route table (`server/priority.go`). Exists to *demote* prefetch and to *promote* a row the user clicked. |
| request | `?p=0\|1\|2` on image URLs | Same, for `<img>` requests that cannot carry headers. |
| response | `X-FLS-Throttle: ok\|slow\|cooldown` | On every authenticated response, state at request start. |
| response | `X-FLS-Throttle-Until: <unix ms>` | Only when not `ok`. |
| 429 | `Retry-After: <s>` + `{code:"rate_limited", retryAfterMs}` | How long to wait. |
| poll | `GET /api/quota` | Level, cooldowns, points used last minute, queue depth per class, cache counters. Poll only while the header says something other than `ok`. |
| debug | `GET /api/debug/quota-costs` (`-v`) | Every registered op: estimate, measured, what the gateway charged. |

Route defaults: everything is P0 unless listed — P1 for
`items/classify`, `items/thumbnail[/image]`, `items/drawing/preview`; P2
for `/api/debug/*` and the background jobs (`uploads.go`, `archives.go`,
thumbnail warm), which set it on their own contexts. The SPA demotes the
aggregates it is not waiting on (hub overview, roll-up, descendants, the
dashboard's permissions path) through the header; a tab's own content is
never demoted by route.

## Calibrating

1. Run with `-v`, browse normally.
2. `GET /api/debug/quota-costs`: `estimate` is the static model, `observed`
   what the gateway actually charged (`source` = `429` from a real quota
   message, `pointValue` from `extensions` once MDM ships it, `probe` from
   `GET /api/debug/cost-probe`).
3. Every real 429 is logged at Warn with `pointValue`/`remaining`; those
   numbers are exact for the rejected query.
4. Commit measured values into `Measured` in `api/cost.go`; they win over
   the estimate.

An over-estimate shows as unused capacity in `/api/quota`; an
under-estimate shows as a real 429, whose message resyncs the bucket and
corrects the op at runtime. Both self-correct; the table closes the loop
faster.

## Tuning knobs

`apsbudget.DefaultConfig()` — capacity, refill, in-flight caps, reserve
floors, max queue waits, cooldown defaults. Change them there, not per
handler: APS meters the app, so the budget is one object.

## What the query trimming changed (Phase 3)

- **`GET /api/items/summary`** (`api.GetItemSummary`, ~40 points): the item
  alone for every `fls:doc` card and the production snapshot; the details
  panel keeps the full query because it renders the version list.
- **One occurrence walk** (`api/occurrences.go`, ops `AllOccurrences*`):
  the BOM and the descendant enumeration derive from the same paginated
  `allOccurrences` query, so they share coalesced pages; the breadth-first
  walk that issued one paginated query per node (up to 20 000) is gone, and
  so is its skip-on-error — a rate limit fails the call.
- **Roll-up**: a child contributes version events only, through the lean
  `ChildActivity` op (~180 points at 20 per page against ~630); the server
  merges the first 24 children and reports `childrenIncluded` /
  `childrenTotal`, which the Activity tab shows with **Load all** (`all=1`).
- **Permissions**: the project dashboard asks for `layers=leaf` — the
  deepest layer only, two calls instead of two per ancestor; the explorer
  still asks for all. `folderId` is capped at 16 and a failed layer is
  flagged, never empty.
- **Locate**: one `LocateItem` query nests `parentFolder` eight levels deep
  (one round trip for any real tree, the walk continues only past that).
- **Hub DM id**: captured on the session at hub selection
  (`selectedHubAltID`, persisted, empty → GraphQL fallback); wiki, browse
  and upload requests read it from there.

## Open questions / not done

- Other clients on the same client_id (the Fusion add-in, a TUI) are
  invisible spend; the 429 resync is the only feedback.
- Single process only: two servers on one client_id each think they own
  80 % of the quota.
- Signed-URL byte fetches (OSS/S3) are not routed through the scheduler —
  they cost no points and `warmSem` already bounds them; `MaxIdleConnsPerHost`
  in `api/client.go` is the real cap there.
- `server/handlers_drawingpreview.go` keeps its own unbounded byte cache;
  worth moving onto the bounded pattern.
- Page sizes are still hardcoded in the query texts; the registry records
  them but does not drive them.
- The planned `GET /api/debug/cost-probe` (run every op at `limit: 1`, and
  read the exact cost from the per-query-cap validation error by inflating
  the query with aliases) is not built: the query texts live inside their
  functions, not in an enumerable registry. `Measured` is fed by the Warn
  log of real 429s and by `extensions.pointValue` once MDM ships it;
  `/api/debug/quota-costs` shows both against the estimate.
- Where-used still fetches one row per parent *version* and dedupes after;
  an item-level where-used, if the schema has one, is unprobed.
- A versions fetch is still paid twice when the Details and Activity tabs
  are both opened on one item (coalesced only within 30 s).
