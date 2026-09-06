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
  remaining quota 69`. Both numbers are exact and are the only budget
  telemetry MDM exposes today; `extensions.pointValue` (already on the AEC
  Data Model) is read opportunistically when it arrives. Platform 429s may
  carry `Retry-After`.
- **Cost model — calibrated, not documented.** The docs say "1 per object
  field × page size". Three real measurements for `GetDrawingsForDesign`
  (`api/refs.go`: 50×50 = 23066, 10×10 = 1026, 10×5 ≈ 510) are 3–5× higher.
  The model in `api/cost.go` — every selected field costs 1 per row it is
  evaluated on, plus 1 per row, nested pages charged again per parent row —
  reproduces all three within 0.01 %:
  `Estimate = 10×Roots + Fixed + (RowFields+1)×Limit`. Under it
  `GetItemDetails` is ~656 points, a design opened on History ≈ 1100, ten
  `fls:doc` cards ≈ 7000 (more than a minute of quota). That is why the
  429s happened.
- Since 2026-08-17 MDM points are metered and billed, so points are money
  as well as throttle.

## Design (`internal/apsbudget`)

- **Bucket** (`bucket.go`): capacity 4800 points (80 % of 6000), refilled
  continuously at 80/s. May go negative after an under-estimate. Resynced
  *downwards only* from the `remaining quota` a 429 reports (minus the 1200
  headroom we never model as ours).
- **Scheduler** (`scheduler.go`): strict priority heap, FIFO within a
  class. `P0` user-blocking (navigation, details, tab opens), `P1`
  in-viewport per-row work (classify, thumbnail), `P2` background
  (prefetch, warm, upload/archive jobs, debug). `MaxInFlight` 12 GraphQL /
  6 REST. **Reserve floors** P0 0 %, P1 10 %, P2 35 % of capacity must
  remain *after* a take, so a click always finds something left. **Maximum
  queue wait** P0 = the request's own deadline, P1 10 s, P2 2 s; a request
  whose expected wait exceeds that is refused *immediately* with a typed
  `RateLimitError{Queued:true, RetryAfter}` — a real countdown — instead of
  parking until a 504. A slot is held for exactly one HTTP round trip.
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
`items/classify`, `items/thumbnail[/image]`, `items/drawing/preview`,
`hub/overview`, `items/local-refs`, `activity/report`,
`items/descendants`, `activity/rollup`; P2 for `/api/debug/*` and the
background jobs (`uploads.go`, `archives.go`, thumbnail warm), which set it
on their own contexts.

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
  them but does not drive them yet (Phase 3).
