# Permalinks — shareable URLs for navigable state

> **STATUS: largely shipped.** URL state is implemented. `web/src/state/navUrl.ts`
> holds the pure serializers (`navToSearch` / `searchToNav` / `shouldPush`);
> `web/src/state/nav.tsx` hydrates from `location.search` at startup, writes the
> URL on nav changes (push vs replace via `shouldPush`), and handles `popstate` —
> the URL sync is inlined in `nav.tsx` rather than a separate `useSyncNavToUrl.ts`.
> Supported params: `app=browser|tasks|production` (production was added after
> this plan), `hub`, `proj`, `f` (repeated), `sel`, and `dtab` (the Details tab).
> **Not done:** `ptab` — the Project-panel tab is not URL-addressable.
> The text below is the original plan, kept as history.

## Context (as written, pre-implementation)

Today the app has **no URL state**. Navigation lives entirely in an in-memory
reducer (`web/src/state/nav.tsx`), and the only thing that survives a reload is
the last-used hub (`fls.lastHub` in localStorage). So a reload drops you back at
the hub's project list, the browser back/forward buttons do nothing, and there
is no way to send someone "open *this* document on *this* tab."

This plan adds **shareable permalinks**: every navigable location — hub →
project → folder → document, which Details/Project tab is open, and which
top-level app (browser vs tasks) — is reflected in the URL, and a cold load of
that URL reconstructs the location. Refresh-stays-put and working back/forward
fall out of the same mechanism.

## Goals / non-goals

**Goals**
- URL reflects: `app` (browser|tasks), `hubId`, `project`, `folderStack`,
  `selected` document, the Project-panel tab (dashboard|tasks|wiki|chat), and
  the Details tab (preview|history|…).
- Cold-load hydration: pasting a permalink lands on the right place + tab.
- Back/forward and refresh work.
- No new backend endpoints; no router library.

**Non-goals (v1)**
- Pretty path URLs (`/hubs/…/projects/…`). Query-param state only — the URL
  path stays `/`.
- Permalinks into per-user Tasks (`/api/tasks/mine` is caller-scoped; a task
  card already has the `fls:task` in-app link). A future phase can add a
  `task=<projectId>:<taskId>` param that opens the `TaskViewDialog`.
- Deep-linking chat to a specific message/channel.

## Why query-params, not a router

- **IDs are URNs** (contain `:` and `/`); the API layer already carries them as
  query params via `qs()`. URLs do the same with `encodeURIComponent`, so the
  path never contains a URN.
- The SPA is served by a **`/` catch-all** (`server/routes.go`; `/api/` is the
  only other branch). Because query-param routing keeps the path at `/`, the
  server needs **no SPA-fallback change** and we avoid adding `react-router`.
- All nav mutations already funnel through **one reducer**, so URL↔state sync
  has a single choke point instead of being sprinkled across components.

## URL schema

Single search string on `/`. Absent params mean "not set" (e.g. no `sel` = no
document selected).

```
/?app=browser
 &hub=<hubId>
 &proj=<projectId>~<projectName>
 &f=<folderId>~<folderName>          (repeatable, in drill order; f=…&f=…)
 &sel=<itemId>~<name>~<kind>          (the selected document)
 &ptab=dashboard|tasks|wiki|chat      (project-panel tab)
 &dtab=preview|history|activity|…     (details tab; only meaningful with sel)
```

- `~name` suffixes are display hints so the breadcrumb/label paints instantly on
  cold load **without** an extra fetch; ids drive correctness. Names can go
  stale (rename) — cosmetic only, refreshed once the real item loads.
- `app=tasks` carries no other params (the Tasks screen is hub-independent).
- Everything `encodeURIComponent`-encoded; `~` is the field separator (URNs
  don't contain it).

**Compact form for documents:** when `sel` is present we can *optionally* omit
`proj`/`f` and reconstruct them from `sel` alone via `api.itemLocation` (see
Hydration). v1 keeps them explicit for a self-describing URL + instant paint;
the compact form is a later size optimization.

## Mechanism

### 1. Serialize (state → URL)
A `useSyncNavToUrl()` hook subscribes to nav state and, on change, builds the
search string and calls `history.replaceState` (same-place moves) or
`pushState` (navigations that should add a back-stack entry — project/folder/doc
changes). Debounced/guarded so it doesn't fight the hydration pass.

Rule of thumb: **push** on `selectProject` / `enterFolder` / `selectItem` /
`setApp` / `gotoFolder`; **replace** on tab switches. Implemented by comparing
the incoming state to the last-serialized snapshot.

### 2. Hydrate (URL → state) on cold load
`NavProvider` reads `window.location.search` once at startup and dispatches a
new `hydrate` action. Resolution:

- `hub` → set directly (name filled from `useHubs` when it loads).
- `proj`/`f`/`sel` with `~name` hints → build `Item` objects immediately from
  the hints so the UI paints, using `goto.ts`'s exact Item shape
  (`{id,name,kind,altId?,isContainer}`).
- **Correctness backstop:** if `sel` is present, kick `api.itemLocation(hub,
  sel)` (the resolver `useGoToDocument` already uses) and reconcile the resolved
  `projectAltId` / folder path into state — this fills `project.altId` (needed
  for wiki/contents) and corrects any stale names.
- `ptab`/`dtab` seed the (now lifted) tab state.

### 3. Back/forward
A `popstate` listener re-runs hydration from the new `location.search`. Guard
the serialize pass so the popstate-driven state change doesn't immediately
re-push.

## Two local tab states to lift

Both are currently component-local and must become URL-addressable:

- **`ProjectPanel` tab** (`web/src/components/ProjectPanel.tsx:29`,
  `useState<ProjectTab>`) → read initial value from `nav` (`projectTab`) and
  write back on change. Add `projectTab` to `NavState` + a `setProjectTab`
  action.
- **`DetailsPanel` tab** (`web/src/components/DetailsPanel.tsx:154`,
  `useState<TabKey>` seeded from `nav.selectedTab`) → already half-wired through
  `nav.selectedTab`; make the tab write back to `nav.selectedTab` on change so
  the URL stays current (today `selectedTab` is write-once at jump time).

## Persistence interplay

- **URL wins over `fls.lastHub`.** In `AppLayout`, the "restore last hub" effect
  must run **only when the URL carried no `hub`** — otherwise a shared link gets
  overwritten by the recipient's last hub. Gate the existing effect on "no hub
  in the initial URL."
- The react-query cache persistence (`QUERY_CACHE_KEY`, buster `fls-9`) is
  unaffected; hydration reuses those warm caches for names/locations.
- No new buster bump (query *shapes* don't change).

## Access / failure handling

A permalink only works for a recipient with APS auth + access to that
hub/project. Hydration must degrade gracefully:
- Not signed in → the existing `Gate`/login flow runs first, then hydrate.
- `itemLocation` 404/403 (no access, or item deleted) → fall back to the
  highest location that *did* resolve (project root, or hub) and surface a
  small "couldn't open that document" notice rather than a blank screen.
- Unknown/renamed folder ids → keep the id-driven navigation; the name hint just
  refreshes.

## Phases

**Phase 1 — core browser state (the bulk of the value).** ✅ *Shipped.*
`app` + `hub` + `proj` + `f*` + `sel` serialized/hydrated; `itemLocation`
backstop; push/replace rules; `popstate`; gate the last-hub restore. Ships
refresh-stays-put, back/forward, and shareable document/folder links.

**Phase 2 — tabs.** ◐ *Partial: `dtab` (Details tab) shipped; `ptab` (Project-panel tab) is not URL-addressable.*
Lift `ProjectPanel` and `DetailsPanel` tab state into nav; add `ptab`/`dtab`.

**Phase 3 (optional) — polish.** ✗ *Not done.*
Compact document URLs (drop `proj`/`f`, reconstruct from `sel`); a "Copy link"
affordance in the breadcrumb/details header; `task=` param opening
`TaskViewDialog`.

## Files

- `web/src/state/nav.tsx` — add `projectTab` to `NavState`, `hydrate` +
  `setProjectTab` actions, make `selectedTab` writeable; export a serializer +
  parser (`navToSearch` / `searchToNavState`).
- `web/src/state/navUrl.ts` *(new)* — pure encode/decode helpers
  (`~`-delimited fields, `encodeURIComponent`) + the push-vs-replace decision.
- `web/src/state/useSyncNavToUrl.ts` *(new)* — effect that writes the URL on
  state change and installs the `popstate` listener.
- `web/src/components/AppLayout.tsx` — gate the last-hub restore on "no hub in
  URL"; mount the sync hook.
- `web/src/components/ProjectPanel.tsx` — source the tab from
  `nav.projectTab` / write via `setProjectTab`.
- `web/src/components/DetailsPanel.tsx` — write tab changes back to
  `nav.selectedTab`.
- `web/src/state/goto.ts` — reuse as-is (its Item shape + `itemLocation` usage
  is the hydration template; optionally factor the "location → Items" mapping
  into a shared helper both `goto` and hydrate call).

No backend changes.

## Verification

- **Unit:** `navUrl.ts` round-trips (state → search → state) for hub-only,
  project, nested folder, selected-doc-with-tab, and `app=tasks`; URN encoding
  survives; unknown params ignored.
- **Manual E2E (`make run`):**
  1. Drill hub → project → folder → document, open History tab → the URL grows
     at each step; copy it, open in a new tab → lands on the same document +
     History.
  2. Back/forward walks the drill path; refresh stays put.
  3. Switch Project tab to Wiki/Tasks → `ptab` updates; reload holds the tab.
  4. `app=tasks` link opens the Tasks screen directly.
  5. Paste a document link while signed into a *different* hub → URL wins over
     `fls.lastHub`; it still resolves.
  6. Paste a link to a document you can't access → graceful fallback + notice,
     no blank screen.
  7. Paste a link to a deleted item → falls back to project root.

## Risks / gotchas

- **Serialize/hydrate feedback loop** — the URL write must not re-trigger a
  state change that re-writes the URL. Guard with a "last serialized" snapshot
  and a hydration-in-progress flag.
- **`project.altId`** is required by wiki/contents but isn't in the URL; the
  `itemLocation` backstop must fill it before those tabs fetch (gate wiki/tasks
  fetching on `altId` present — they already keep tabs mounted).
- **Folder-only permalinks** (no `sel`) rely on the `~name` hints since there's
  no folder-ancestry endpoint; ids stay correct, names may be stale until the
  contents load. Adding a real ancestry resolver is a later option.
- **`selectHub` resets state** (`...initialState`); ensure hydration dispatches
  a single atomic `hydrate` rather than a sequence of actions that each reset.
- **StrictMode double-effect** in dev — hydration/popstate wiring must be
  idempotent.
