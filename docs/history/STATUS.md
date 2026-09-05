# History view — implementation status

The **History** tab of the details panel (`web/src/components/history/`), reworked
from a single left-to-right strip into a stack of day rows.

## Why it changed

The old `HistoryGraph.tsx` gave every save its own column, 38 px apart, on one
horizontal strip with up to four lanes (saves / milestones / releases / public
shares) and angled date tags underneath. Two problems compounded as a design
aged:

- **It got long.** A few hundred saves is a strip several thousand pixels wide,
  scrolling sideways inside a panel that shows perhaps twenty columns at once.
- **It had no sense of time or of people.** Column *n+1* sat 38 px right of
  column *n* whether the next save came ninety seconds or two years later, and
  the author appeared only on hover.

## What it is now

A vertical stack of **day rows, newest at the top**, banded with alternating
backgrounds, separated by elapsed-time labels, and split into **one track per
author** with an identity avatar in a sticky left gutter.

Two x mappings share that skeleton, switched by a checkbox in the header:

| | Day view (default) | Thread across days (checkbox) |
|---|---|---|
| x | clock time, 00:00 → 24:00, same scale in every row | position in the whole history, `COL_GAP` (38 px) per save |
| empty time | not drawn — the gap label carries it | consumes no width; days butt up against each other |
| axis | hour gridlines, labels at 00/06/12/18/24 | none; a dashed seam marks each day boundary |
| thread | — | a polyline through every save in order, across rows |
| scrolling | fits the panel; grows downward and page-scrolls with the tab | both axes, inside a height-bounded box, with the gutter frozen left |

Thread view is deliberately the *old strip refolded* — same 38 px pitch, same
chronological order — so the toggle reads as "unfold this" rather than "draw
something else". It is off by default and not persisted.

## Files

**Frontend**
- `web/src/components/history/historyLayout.ts` — **pure** layout maths: day
  bucketing, per-author tracks, the declutter relaxation, both x mappings,
  `layoutStack`, and every spacing constant. No React, no MUI, no DOM.
  `historyLayout.test.ts` covers it (36 cases) — the repo has no jsdom, so this
  split is the only way the geometry gets tested at all.
- `history/HistoryTimeline.tsx` — the `ResizeObserver`, bucketing, the thread
  checkbox, the scroll container, the row cap, the count caption, the legend.
- `history/DayRow.tsx` — one day: sticky header + sticky avatar gutter + the
  plot SVG (rails, dots, hour axis).
- `history/ThreadOverlay.tsx` — thread mode only: one absolutely-positioned SVG
  over the whole stack carrying the polyline and the day seams.
- `history/GapLabel.tsx` — the elapsed-time band between two rows.
- `history/VersionTooltip.tsx` — the hover card (moved out of the old file).
- `history/ChangeTooltip.tsx` — the hover card for an edit that made no version.
- `web/src/i18n/enums.ts` — `historyChangeLabel` (+ `enums:historyChange.*`).
- `web/src/components/userColor.ts` — the identity-colour hash (see below).
- `web/src/fmt/dates.ts` — `calendarBreakdown(from, to)`.
- `web/src/fmt/index.ts` — `fmtDuration(breakdown)` + an `Intl.ListFormat` cache.
- 🗑️ **Removed:** `web/src/components/HistoryGraph.tsx`.

**Backend**
- `api/details.go` — the GraphQL selection asks for
  `createdBy { id userName firstName lastName }`; `apiUser.fullName()` falls back
  to `userName`; `versionRowFields` / `itemVersionsNextQuery` /
  `itemWithVersions` page the version list (shared with
  `api/activity_graphql.go`); `api.VersionSummary.CreatedByID`.
- `api/history.go` + `api/client.go` (`graphqlEndpointV3`, `gqlQueryV3`) +
  `server/handlers_nav.go` (`handleItemHistory`, `GET /api/items/history`) —
  the non-save history, the app's only v3 query (below).
- `api/probe_history.go` + `GET /api/debug/history-probe` — the `-v`-only
  schema probe that found it.
- `server/dto.go` — `VersionDTO.CreatedByID` → `createdById` (omitempty).
- Tests: `api/details_test.go` (asserts the id round-trips *and* that the query
  text still selects it), new `server/dto_test.go`.

## Design decisions

**Fixed 24-hour axis, not per-day autoscale.** Compressing each row to its own
first→last save would avoid all overlap, but the same clock time would land at a
different x on every row and a two-save day would look like a full day's work.
A fixed axis makes rows comparable; overlap is handled by relaxation instead.

**Milestones decorate the dot in place — the ring means "marked", the fill
means "released".** Vertical space now belongs to days and people, so the old
milestone/release/share *lanes* became dot treatments on the author's own
track: a save is a grey dot; a milestone is that same grey dot wearing the
accent ring; a release is the accent ring with the dot filled accent; a public
share is an outer ring in the secondary colour. This is the PowerTools
vocabulary (its commit c919351): the earlier scheme — accent-filled milestone,
darker-accent release — asked the reader to tell two similar blues apart at
14 px. One step to learn instead of two hues, and a release still reads as the
heavier of the two. The legend swatches take `halo` / `ring` as colours, not
flags, because a milestone's ring colour is not its dot colour. Nothing is
lost, and no row height is spent on `revision`/`publicShare`, which still have
no API source (see Known gaps).

**Tracks key on the APS user id.** Grouping by display name merges two people
who share one and splits one person across a rename. The id is a one-field
GraphQL change; the name remains the fallback when it does not resolve.

**One avatar circle, app-wide.** `components/UserAvatar.tsx` is the single
avatar: it takes `(id, name, size)`, hashes the id (falling back to the name) to
a colour, and renders grapheme-safe initials. It replaced six near-duplicate
implementations — chat messages, task assignees, hub contributors, dashboard
members, the permissions explorer, and the history tracks — each of which had
its own initials logic and a default grey background. The signed-in user's
circle in the title bar is the same component, so a person is the same colour
from the app bar to a chat message to a history track. The one circle that is
**not** this component is the whiteboard presence stack: its colour comes from
tldraw's own peer assignment and has to match that peer's live cursor.

**Identity colour is the app's one non-theme colour.** `userColor.ts` hashes the
author key (DJB2) to `hue = hash % 360` at fixed saturation/lightness. Every
other colour in the app comes from `useTheme()` because Settings → Appearance
retheme s the accents per hub; here the requirement is the opposite — the same
person must read as the same colour whatever the accent is, and hues derived
from one accent collapse into each other past three or four people. The
exception covers the avatar disc **and its track rail**; dots stay
theme-coloured so the milestone/release markers keep their meaning. Text on the
disc goes through `theme.palette.getContrastText()` rather than a hardcoded
white — HSL lightness is not perceptual, and white-on-yellow at L=48% fails
contrast while white-on-blue at the same lightness passes.

**Declutter is a two-pass relaxation.** Forward pass pushes overlapping dots
right to `MIN_DOT_GAP`; a back pass pulls the run off the right wall so a
cluster at 23:59 spreads left instead of clipping. Where a dot moved more than
3 px, a hairline marks its true time — the dot is where it can be drawn, the
hairline is where it happened, and the tooltip always carries the exact
timestamp. Inert in thread mode, where the index axis already guarantees the gap.

**Fixed header and gap-band heights.** `layoutStack` computes each row's y
arithmetically so `ThreadOverlay` can place a cross-row polyline without
measuring the DOM. That is why gap labels are `noWrap` with a constant height
per tier rather than sized to their text.

**A render cap, not a data cap.** The versions arrive with the details payload,
so there is no APS fan-out to throttle — but 300 day rows × ~30 SVG nodes
re-render on every resize. `DAY_ROWS_CAP = 60` bounds that, with a visible
"Show all N days" button and a caption that always reports the true total.
Likewise `TRACKS_PER_DAY_CAP = 6`: past six authors in one day the tail merges
into one `+N` overflow track, and every dot still renders with its own author.
Nothing is capped silently.

**Locale bug fixed on the way through.** The old file called
`d.toLocaleString()` directly, so History dates followed the *browser* locale
rather than the picked app language. Everything now goes through
`web/src/fmt/index.ts`.

**Thread view is height-bounded, and the gutter is a frozen column.** It first
shipped as a scrolling container with no height, which meant the box was as tall
as its content — on a long history, thousands of pixels tall, with its
horizontal scrollbar sitting at the very bottom. Scrolled anywhere near the top
there was no scrollbar on screen at all, so the axis could not be panned except
from the end of the history. Bounding the height (`flex: 1`, `MIN_VIEW_H`,
`overflow: 'auto'`) puts both bars on the edges of something visible, and on one
box, so the themed scrollbar corner applies.

The author gutter is `position: sticky; left: 0`, so horizontal scrolling never
takes a track's face away from it — however far right you scroll, every row
still says who saved. It carries `zIndex: 1`, which puts it above the thread
overlay (positioned but z-auto), so the polyline passes *under* the avatars
rather than across them, and an opaque background so dots slide beneath it. In
thread view it also takes a right border, to read as a frozen pane; day view
omits it, since nothing scrolls under it there and the rule would be decoration.

**A pan/zoom canvas was tried and reverted.** Modelling thread view on
`RelationGraph`/`JobCanvas` — one transform over the whole stack, zoom-to-fit,
level-of-detail dropping labels below 0.35 scale — worked mechanically but not
in use: at the scale needed to fit a long history (~0.06) the result is not
readable enough to navigate by, and the zoom becomes a thing to fight rather
than a way through. Scrolling a bounded box with a frozen gutter is the simpler
answer and the one that stuck. Worth knowing before proposing it again.

**Tooltip thumbnails are hover-delayed, on purpose.** A version thumbnail is an
ungated per-item APS call, and only the **tip** component version is ever
pre-warmed (by classify, off the browse row). Every historical version is
therefore cold: one GraphQL round trip for the signed URL, then one image fetch.
With no delay, sweeping the cursor across a busy day row fired a pair of
requests per dot passed over, and the per-minute cost quota answers a burst like
that with 429s — which the `<img>`'s `onError` turns into a silently missing
thumbnail. `enterDelay`/`enterNextDelay` of 400 ms means only a dot the reader
rests on spends quota, matching how every other thumbnail in the app is gated
(`useInView`, or a visible cap).

The tooltip also now names *why* a preview is absent, because there are two
different causes and they need different fixes:

- **"No preview for this version"** — the version has no
  `rootComponentVersionId` at all. `itemVersions` resolves
  `rootComponentVersion` to null for older/unmigrated saves, so there is nothing
  to ask APS for. Nothing to fix client-side.
- **"Preview could not be loaded"** — the id exists but the fetch came back
  empty: MFGDM never generated that thumbnail (`FAILED`), is still generating it
  (`PENDING`), or the request was rate-limited.

Which one dominates in a given hub is worth checking before doing more work
here — the second is worth chasing, the first is not.

## Known gaps

- ~~**`itemVersions` is not paginated**~~ — fixed (2026-09-04). `api/details.go`
  pages through `allPages` at 50 a page with the item on the first page only
  (`itemWithVersions`), and `api/activity_graphql.go` shares the same
  `versionRowFields` selection and page query. `allPages` now refuses a cursor
  that never ends (`maxPages`) with an error rather than a silent stop.
- **Other changes need a Collaborative Editing hub** — v3 has no data for an
  older hub; the toggle's caption says so. See "Other changes" below.
- **`publicShare` has no API source** — reserved, always false; its legend
  entry and outer ring are written and will light up the day a source is wired
  in (PowerTools reads it from the desktop `DataFile.sharedLink`; neither v2
  nor v3 exposes it). `revision` *is* sourced now — from the v3 history, see
  "Releases and milestone names" below.
- **Hue collisions.** `hash % 360` can put two authors a few degrees apart;
  initials and the avatar tooltip disambiguate. A golden-angle stride would
  spread hues better.
- **The thread draws over its dots.** The polyline is on top of the rows because
  the row bands would otherwise hide it. It is thin and semi-transparent, so it
  reads as passing behind, but a true under-the-dot pass would need banding to
  move into the overlay.
- **The `toLocaleString` locale bug still exists** in
  `production/BatchTimeline.tsx` and `hub/HubRiver.tsx` — same one-line fix,
  out of scope here.

## Other changes — the edits that made no version

**Show other changes**, the second checkbox in the header, adds the history
entries that produced no version: `PropertiesUpdatedHistoryChange`,
`ComponentPrimaryHistoryChange`, `ComponentPartNumberHistoryChange`,
`VersionCreatedHistoryChange` (a milestone), and the rest of the
`HistoryChange` family — each with its **own author**. It came from the
PowerTools add-in's Document History (built from this view): on its test design
15 of 42 history entries were non-saves and **two of nine contributors had
never saved a version at all**, so a saves-only history credited the design to
seven people.

What it does:
- **Same rows, same tracks.** Changes go through `toEvents` → `bucketByDay`
  exactly like saves (`HistoryEvent = {kind:'save'} & VersionSummary |
  {kind:'change'} & HistoryChange`), so a change lands on its author's existing
  track (by id) or opens a new one, on its own local calendar day, in the same
  `index` sequence — thread view threads them too. Every dot has a stable
  `key` (`v<number>` / `c<index>`), which is also the hover state.
- **A small open ring** (`CHANGE_R = NODE_R - 2.5`) stroked in the track's rail
  colour — the author's identity colour — with paper fill, so it reads as a
  lighter event on the same rail and can never be mistaken for a save.
- **The row header counts what is on it:** "N saves", "M changes", or
  "N saves · M changes" (`rowTally`; `HistoryDay.saves` / `.changes`), and the
  row's `aria-label` carries the same tally.
- **Hover card** (`ChangeTooltip.tsx`): the change label, the recorded detail
  ("Estimated Cost: 100"), the exact local time, the author. No version number,
  no thumbnail — resting on one spends no quota.
- **Labels:** the server ships the raw `__typename`; `historyChangeLabel`
  (`i18n/enums.ts`, catalog `enums:historyChange.*`) names the nine known
  types and de-camel-cases anything else (`humanizeChangeType`). An unknown
  type still says something truthful rather than vanishing.
- **Legend** entry "Other changes" only while the toggle is on and a change
  exists. **Off by default, not persisted** — same as the thread checkbox.

### Releases and milestone names — from the same history

v2 knows only `isMilestone`. The v3 history knows more: a
`VersionCreatedHistoryChange` is a **milestone** and its `description` is the
milestone's name ("Milestone V2", "Item Update", or what the user typed); a
`RevisionCreatedHistoryChange` is a **release** and its `description` is the
revision label ("1", "A", "Rev B"), and it has **no author**. Both are stamped
with the exact timestamp of the save they mark (to the millisecond, in every
row seen on 2026-09-04), and there is one `ModelWrittenHistoryChange` per
version. So:

- `api/history.go` returns `Saves` — every `*Written` row, newest first — with
  each marker folded onto the save at its instant (else the newest save at or
  before it). A user-typed milestone name is a release (`isReleaseName`, the
  PowerTools rule: not "Milestone …", not "Item Update"); a release row's label
  is always one. Marker rows are **not** also shown as change rings: the ring
  / fill on the dot is their representation, and drawing them twice put a
  second marker on the same track at the same instant (seen on a real document
  on 2026-09-05). This is a deliberate departure from the PowerTools palette,
  whose milestone data comes from the desktop API rather than these rows.
- `applyHistoryMarkers` (`historyLayout.ts`) joins `saves` to `versions` **by
  position**, newest first, and only when the lengths agree — a marker on the
  wrong version is worse than none. It sets `isMilestone`, `milestoneName` and
  `revision`; the dot's ring and fill, the legend, and the hover card ("Release
  1", "Milestone Item Update") follow from those.
- **Consequence for fetching:** the history is now needed for the dots, not
  only the rings, so `useItemHistory` runs **whenever the History tab is
  mounted** — one v3 call per document viewed on that tab, beside the details
  call. The checkbox only decides whether the rings are drawn. (The tab is
  the default for designs; DetailsPanel mounts only the active tab, so a
  document viewed on another tab costs nothing here.) The probe found
  `VersionCreatedHistoryChange.version` and `RevisionCreatedHistoryChange
  .revision` exist; their sub-fields (`v3_version_type` / `v3_revision_type`
  candidates) could one day name the version directly instead of by position.

### Where the data comes from — v2 and v3 mixed

Saves come from v2 `itemVersions` as before. The non-save history is **not in
the v2 schema at all** — probed live on 2026-09-04 against a 65-version
design: `Cannot query field "history"` on `DesignItem` and on `Component`, no
`model` on the `Query` root, `__type` null for both `Model` and
`HistoryChange`. It *is* in the **v3** ("Collaborative Editing") schema, where
`history` hangs straight off the item at the same `item(hubId, itemId)` root —
the `feat/v3api` branch verified that live (`origin/feat/v3api:api/details.go`).
So this app stays a v2 app and reaches for v3 for exactly one query:

- `api/client.go` — `graphqlEndpointV3`
  (`https://developer.api.autodesk.com/mfg/v3/graphql/public`), `gqlQueryV3`,
  and `gqlQueryAt`; one transport, same token, same retry / 429 posture.
  `allPagesAt` is `allPages` against an explicit endpoint.
- `api/history.go` — `GetItemHistoryChanges`: twin queries through
  `allPagesAt` at **50 a page** (PowerTools: "Pagination limit 100 exceeds
  maximum allowed value of 50" for this list), fragments for `DesignItem` /
  `ConfiguredDesignItem` / `DrawingItem`, `author { id userName firstName
  lastName }`. Rows whose typename ends in `WrittenHistoryChange` are **dropped**
  — those are the saves, and saves already come from `itemVersions` with
  numbers and thumbnails. Sorted newest-first; the API's order is not relied on.
- `GET /api/items/history?hubId=&itemId=` (`server/handlers_nav.go`
  `handleItemHistory`, `protHub`) → `ItemHistoryDTO{ changes }` (never nil).
- `useItemHistory(hubId, itemId)` — one call per document viewed on the
  History tab (see above for why it is not behind the checkbox). With the
  toggle on the caption reads "N versions · M other changes" (or "No other
  changes", "Loading…", or a warning-coloured "could not be loaded for this
  document"). The failure is inline and small on purpose: the saves-only
  history is still right, only the rings and release fills are missing.

**Verified on the 2026-09-04 probe:** the hub is Collaborative Editing
(`hubDataVersion` 2.0.0); the v3 `author.id` **is** the same id space as v2
`createdBy.id` (`X6MHRWZ3VKGH` on both), so one person is one track; a
`limit: 100` on `history` does not error — it returns `item: null`, which
would read as an empty history, so the page stays at 50; the full
`HistoryChange` family is the ten types in `enums:historyChange` plus the
`*Written` saves. **Known limit:** v3 resolves only on Collaborative Editing
hubs; on an older hub the caption shows the failure every time and the dots
carry only the v2 milestone flag. The probe (`api/probe_history.go`,
`GET /api/debug/history-probe`, `-v` only, the clock dev button next to the
bug) carries the v2 evidence and the v3 candidates the query relies on.

## Verification

```sh
gofmt -l . && go vet ./... && go test ./...
cd web && npm test && npm run lint:i18n && npm run build
```

Eyeball, in light **and** dark and at two panel widths:

1. Newest day at top; alternating bands legible in both modes.
2. Day view: 12:00 is the same screen x in every row; resizing keeps rows aligned
   and dots **circular**. Oval dots mean the `ResizeObserver` wiring regressed
   into a `preserveAspectRatio` scale — that is why the SVG is drawn at measured
   pixel width rather than with `viewBox` + `width="100%"`.
3. Row heights do not change when the window widens.
4. Gap labels read correctly at 1 day / 3 days / 1 week / 2 months / >1 year, and
   switch language live. A stale English "3 months" after switching to German
   means the `Intl.ListFormat` cache was not registered in the `languageChanged`
   flush in `fmt/index.ts`.
5. A busy day: dots visibly separated, order preserved, nothing clipped at 24:00.
6. Hover: tooltip reachable from off-dot, exact local time, thumbnail loads or
   degrades, the card's author disc matches the gutter avatar.
7. Milestone halo, release halo and share ring are distinguishable; the legend
   lists only what occurs.
8. Checkbox off by default. On: the staircase runs bottom-left → top-right, the
   thread skips no version, and seams land between days. **Both scrollbars are
   reachable from anywhere in the history** — that is the bug that motivated the
   bounded height; scroll to the top and the horizontal bar is still there.
9. Scroll right: the gutter avatars and day headers stay pinned, the thread
   passes *under* the gutter rather than over it, and no dot shows through it.
   Both bars are the themed thin bar and the bottom-right corner is transparent,
   not an OS grey square (`theme.ts`). Check Chromium **and** Firefox — Firefox
   honours only the standard `scrollbar-color` properties.
10. A design with >60 days shows "Show all N days" and the caption still reports
    the true total.
11. With `-v`, a design of ≤50 versions costs **one** GraphQL request and a
    longer one costs one per 50 with nothing truncated; a version by a profile
    with no first/last name shows the `userName` instead of a blank.
12. **History call.** Opening a design on the History tab makes one
    `/api/items/history` request (the v3 endpoint in the `-v` log) beside the
    details call; opening it on another tab makes none. A released version
    shows the accent-filled dot with the accent ring and "Release 1" on hover;
    a milestone the grey dot with the ring and "Milestone Item Update"; the
    legend lists Releases / Milestones only when they occur.
13. **Other changes.** Toggle on: the caption reads "N versions · M other
    changes", the legend gains "Other changes", rings are smaller than dots and
    stroked in their rail's colour, and hover shows label / detail / time /
    author with no thumbnail. Someone who only edited properties has a track
    and an avatar of their own; someone who also saved has **one** track. A
    milestone or release shows **once** — on its dot, never also as a ring on
    the track. Thread view threads rings
    and dots in one sequence. On a non-CE hub the caption turns
    warning-coloured and nothing else changes. Reload: the toggle is off again.

Multi-author days need a second APS account saving the same design on the same
calendar day; failing that, the deterministic coverage is `historyLayout.test.ts`
(1/2/7 authors, id-present and id-absent).
