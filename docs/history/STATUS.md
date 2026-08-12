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
| scrolling | fits the panel; no horizontal scroll | scrolls both ways for a long history |

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
- `web/src/components/userColor.ts` — the identity-colour hash (see below).
- `web/src/fmt/dates.ts` — `calendarBreakdown(from, to)`.
- `web/src/fmt/index.ts` — `fmtDuration(breakdown)` + an `Intl.ListFormat` cache.
- 🗑️ **Removed:** `web/src/components/HistoryGraph.tsx`.

**Backend**
- `api/details.go` — the GraphQL selection now asks for `createdBy { id … }`;
  `apiUser.ID`, `api.VersionSummary.CreatedByID`.
- `server/dto.go` — `VersionDTO.CreatedByID` → `createdById` (omitempty).
- Tests: `api/details_test.go` (asserts the id round-trips *and* that the query
  text still selects it), new `server/dto_test.go`.

## Design decisions

**Fixed 24-hour axis, not per-day autoscale.** Compressing each row to its own
first→last save would avoid all overlap, but the same clock time would land at a
different x on every row and a two-save day would look like a full day's work.
A fixed axis makes rows comparable; overlap is handled by relaxation instead.

**Milestones decorate the dot in place.** Vertical space now belongs to days and
people, so the old milestone/release/share *lanes* became dot treatments on the
author's own track: milestone = accent fill + halo, release = darker accent +
halo, public share = an outer rust-orange ring. Nothing is lost, and no row
height is spent on `revision`/`publicShare`, which still have no API source.

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

- **`itemVersions` is not paginated** (`api/details.go`). Long histories are
  already truncated by APS's page before the UI sees them, and nothing says so.
  A day-by-day view makes this far more visible than the old strip did. The fix
  is to route the versions query through the existing `allPages` helper
  (`api/queries.go`) the way `api/bom.go` does. `api/activity_graphql.go` has
  the identical gap.
- **`revision` and `publicShare` have no API source** — reserved fields, always
  empty/false. Their legend entries and dot decorations are written and will
  light up the day a source is wired in.
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
   thread skips no version, seams land between days, and the gutter avatars and
   day headers stay pinned while scrolling right.
9. Both scrollbars are the themed thin bar and the bottom-right corner is
   transparent, not an OS grey square (`theme.ts`). Check Chromium **and**
   Firefox — Firefox honours only the standard `scrollbar-color` properties.
10. A design with >60 days shows "Show all N days" and the caption still reports
    the true total.

Multi-author days need a second APS account saving the same design on the same
calendar day; failing that, the deterministic coverage is `historyLayout.test.ts`
(1/2/7 authors, id-present and id-absent).
