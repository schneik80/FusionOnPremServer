# Whiteboards — status

A per-project whiteboard built on [tldraw](https://tldraw.dev): the fifth project
app beside Tasks, Wiki, Chat and Production. Branch `feat/whiteboards`.

Draw freely, and drop **live app cards** — tasks, jobs, batches, documents — onto
the canvas alongside the sketching.

## ⚠️ Licensing — read before shipping this

**tldraw's SDK is not free for production use.** Its licence permits use *in
development*; production requires a licence:

- **Commercial** — a paid licence key, obtained via tldraw's sales form.
- **Hobby** (non-commercial only) — free, but the "made with tldraw" watermark
  must remain visible on the canvas.

A key **is now supplied**. It lives in `web/.env.local`
(git-ignored — see `web/.env.example`) as `VITE_TLDRAW_LICENSE_KEY`, and
`WhiteboardCanvas` passes it to `<Tldraw licenseKey=…>` per
https://tldraw.dev/installation#License. Vite inlines it into the whiteboard
chunk at build time, so **rebuild the web app after changing it**.

The key is deliberately not committed: its host list is `["*"]`, so anyone who
copied it out of the repo could use it on any domain. It is, however, inlined
into the shipped bundle — that is inherent to a client-validated licence, not
something to try to hide.

> ### ⏰ This is an EVALUATION licence — it expires **2026-10-29**
>
> Decoded, the key is `id=JldYTJ-1, hosts=["*"], flags=16 (EVALUATION),
> expiry=2026-10-29`. While valid, `getLicenseState` returns `"licensed"`: no
> gate, and no watermark (the `WITH_WATERMARK` flag is not set).
>
> **On expiry it returns `"expired"`, which is also in
> `shouldHideEditorAfterDelay` — so the whiteboard will start silently vanishing
> again, exactly as it did before the key.** If that happens after
> 2026-10-29, this is why. Renew, or expect to rediscover the section below the
> hard way.

See https://tldraw.dev/community/license.

## Assets, CSP and the watermark

tldraw loads its fonts, icons and translations from `cdn.tldraw.com` by default.
This app ships a strict CSP (`default-src 'self'`), so every one of those
requests was blocked — the fonts never arrived (unreadable canvas text) and the
blocked translations fetch rejected inside React's commit phase, killing the
board seconds after it opened.

Fixed by resolving the assets through the bundler
(`getAssetUrlsByMetaUrl()` from `@tldraw/assets`, passed as `assetUrls`), so
Vite emits them as same-origin files. **The CSP was not weakened**, and the
whiteboard now works offline like the rest of this local-first app.

One CDN call remains and is still blocked: tldraw's **licence tracking ping**.
`getTrackType` fires it for an evaluation licence just as it did for no licence
at all (only the `license_type` parameter changes, `unlicensed` → `evaluation`),
because this page is not classified as development. That ping carries the full
page URL — which for this app includes the hub and project URNs and the
project's name. Leaking internal project names to a third party is a poor trade
for a local engineering tool, so the CSP deliberately still blocks it. The cost
is a console warning and an unhandled rejection from tldraw's tracker.

No watermark image is fetched: the key's flags don't include `WITH_WATERMARK`.

If that trade should go the other way, allow `https://cdn.tldraw.com` in
`connect-src` and `img-src` in `securityHeaders` (`server/middleware.go`) — but
see the privacy note above first.

The tracker ping is **inert**, despite the scary console output.
`LicenseManager.maybeTrack` (`@tldraw/editor/.../license/LicenseManager.js:126`)
calls `fetch(url)` and discards the promise, so the CSP rejection can never
reach the `.catch()` that sets the licence state — it only ever surfaces as an
unhandled rejection in the console. It is **not** why the board disappears; see
below.

## The board vanishes after ~5s — tldraw's licence gate, not a bug

**Resolved by supplying a licence key** (above). Kept because the symptom is so
misleading, and because it returns verbatim if the key expires or goes missing.

Symptom: the canvas mounts and works, then a few seconds later silently
disappears, with **no error, no React crash and nothing for an ErrorBoundary to
catch**. This is deliberate tldraw behaviour, not a fault in this app:

```js
// @tldraw/editor/.../license/LicenseProvider.js
function shouldHideEditorAfterDelay(s) { return s === 'expired' || s === 'unlicensed-production' }
const LICENSE_TIMEOUT = 5e3
// …after 5s: setShowEditor(false)  →  <LicenseGate/>  →  <div style={{display:'none'}}/>
```

With no key the state is `unlicensed-production` **whenever tldraw decides the
page is not development**. Its heuristic
(`LicenseManager.getIsDevelopment`) treats a page as development only if the
protocol isn't `https:`, **or** the hostname is loopback (`localhost`, `::1`,
`127.x`), **or** `NODE_ENV !== 'production'`.

This app is served over **HTTPS on a LAN hostname** (`.aps-public-url`, e.g.
`https://ryzen-nobara.local:8080`) from a production Vite build, so all three
are false and tldraw classifies a single-user local tool as a production
deployment. Reaching the same server at `https://localhost:8080` flips the
loopback clause and the gate never fires — **but** the OAuth callback host must
match the APS app registration, so changing the host breaks login unless that
callback is registered too (see the redirect_uri gotcha).

There was no code fix for this — only a licence key (now supplied), running over
loopback where that genuinely is hobby/development use, or dropping the feature.
Note the loopback route has its own catch: the OAuth callback host is baked from
`.aps-public-url`, and APS fails login when it doesn't match the registration.

## Model

| Concept | What it is |
|---|---|
| **Whiteboard** | A named board in a project. `w<n>`, listed newest-first. |
| **Document** | The tldraw document (shapes, pages, bindings) for one board. Stored opaquely — the server never parses tldraw's schema. |
| **fls-card shape** | A custom tldraw shape whose only state is an `fls:` token. |

### Storage shape, and why it differs from its siblings

Tasks and Production keep a project's whole feature state in one JSON file. That
does not work here: a tldraw document is megabytes of shapes and is rewritten on
every autosave. So whiteboards split:

```
<config>/whiteboards/<sanitized-projectId>/
  whiteboards.json      metadata only — names, timestamps, sizes
  doc-w1.json           one tldraw document per board
  doc-w2.json
```

Listing boards therefore never touches a document, and saving a board rewrites
only that board. Both files are written atomically (temp + rename) — the
difference between a whiteboard and a truncated whiteboard.

### Cards are references, not screenshots

The `fls-card` shape stores an `fls:doc` / `fls:task` / `fls:job` / `fls:batch`
token and renders it through the shared `components/RefCard.tsx`. A card on a
board is the *live* task or batch, re-hydrated on every render — rename the task
and the board follows. It also means any future card scheme works here for free,
since `RefCard` is the single place tokens map to renderers.

Cards are placed from the canvas toolbar, reusing the existing pickers
(`AttachTaskDialog`, `ProductionRefDialog`, `HubBrowserDialog`) — the same
dialogs chat, the wiki and task details use, so "insert a card" behaves
identically everywhere.

Pointer events on a card are off until it is the only selected shape: otherwise
the card's own click targets would swallow the drag that moves it. One click
selects, the next interacts.

## Layout

**Backend**
- `whiteboards/types.go`, `whiteboards/store.go` — the store described above.
  Caps: 200 boards/project, 24 MiB per document.
- `server/handlers_whiteboards.go`, `dto_whiteboards.go`, routes in `routes.go`.
  Authorization reuses `chat.Authorizer` (`CapRead` view, `CapPost` edit,
  `CapModerate`-or-creator delete), like every other project app.

**Frontend** — `web/src/whiteboards/`
- `WhiteboardsApp` — project tab: board rail (create / rename on double-click /
  delete) + the selected board's canvas.
- `WhiteboardCanvas` — loads the document once, autosaves on a 1.5s debounce,
  and flushes on unmount. **Lazy-loaded**: tldraw is ~1.7 MB, so it is code-split
  out of the entry bundle and only fetched when the tab is opened.
- `cardshape.tsx` — the `fls-card` ShapeUtil.
- `whiteboard.css` — **the only stylesheet in this app.** Everything else is
  styled through MUI `sx`, but tldraw is themed via CSS variables and can only
  be reskinned from CSS. It is scoped to `.fls-tldraw` so nothing leaks, and
  only overrides presentation (Montserrat, the `#0696d7` accent, 6px radii).
  The light/dark scheme is driven from the app's colour mode, not tldraw's.

## API

```
GET    /api/whiteboards      ?projectId              list + capabilities
POST   /api/whiteboards      ?projectId              {hubId,projectName,name}
PATCH  /api/whiteboards      ?projectId&boardId      rename
DELETE /api/whiteboards      ?projectId&boardId      moderator or creator
GET    /api/whiteboards/doc     ?projectId&boardId              the document, or null if unsaved
PUT    /api/whiteboards/doc     ?projectId&boardId&baseRev[&force=1]   replace the document (autosave)
GET    /api/whiteboards/events  ?projectId&boardId              awareness stream (SSE)
```

The document endpoints carry their own much larger body cap (24 MiB) than the
64 KiB used everywhere else, and pass the payload through opaquely — the store
checks it is JSON and within the cap, and nothing parses tldraw's schema.

### Revisions: no more silent clobbering

`Board.DocRev` counts document saves. The GET advertises it as a weak `ETag`
(`W/"7"`); the PUT carries it back as `baseRev`, and a save based on a revision
the board has already moved past is refused with **409 `whiteboard_stale`**
instead of overwriting whoever saved in between. `force=1` is the user's
acknowledged "overwrite anyway" after being shown the conflict — never a retry
path, because retrying a refused save is exactly what would discard the other
person's work.

The check lives in `Store.SaveSnapshot`, under the project lock that also
serialises the write; a handler-side compare would leave a window between
reading the revision and writing. `DocRev` rides the existing file version with
`omitempty` rather than bumping it (the tasks schedule-fields precedent), so an
older binary can still read the file. If an older build rewrites it the field
drops to 0, which is safe: a client holding a higher revision then gets a clean
conflict rather than a silent overwrite.

The document PUT also has its own rate limiter (`whiteboardDocLim`) — it was
previously the one write path in the app with none, and a 24 MiB unmetered body
is a free way to thrash the disk.

### The canvas moves without resizing — and what that broke

Every project tab lives inside a MUI `<Slide>` that keeps panes mounted and
moves them with `transform: translateX` (`ProjectPanel.tsx`), and a board mounts
as soon as it is selected — including while its tab is parked off-screen. So the
canvas routinely initialises somewhere other than where it ends up.

tldraw's `MinimapManager` caches its `getBoundingClientRect()` and refreshes it
from a **ResizeObserver**, which fires on size changes only. A transform changes
the rect's `x` and nothing else, so the cached origin kept the off-screen value
forever. The minimap maps a pointer to a page point with `clientX - cachedX`, so
every click landed far away and the clamp then pinned the camera at one extreme
and refused to pan sideways at all — the reported symptom, horizontal because
the Slide travels on X.

`web/src/whiteboards/canvasGeometry.tsx` watches the canvas wrapper's full
rectangle — position included — using a ResizeObserver plus the events that
accompany a move (window resize, scroll in the capture phase, and
`transitionend`, which is how the Slide announces it has arrived; transition
events bubble to the window, so one listener covers any ancestor). When the
rectangle changes it remounts the minimap through
`<Tldraw components={{ Minimap }}>`, which is the supported way to make the
manager re-measure — there is no API to poke its cached rect.

The same signal drives **zoom-to-fit on first open**. Three things must be true
before the view can be fitted — the editor has mounted, the document has loaded,
and the canvas is on screen — and they do not arrive in a fixed order, so each
one attempts the fit and whichever is last performs it. The fit is capped at
100%: zooming out to show a whole board is the point, magnifying a board holding
one small shape to maximum zoom is not.

### Awareness stream

`GET /api/whiteboards/events` is per **board**, with its own `sse.Hub`
(`server/whiteboard_hub.go`) rather than an event type on chat's stream: a busy
board sharing chat's 512-entry ring would evict chat's events and hand every
project subscriber a spurious resync. It carries:

- `doc.changed {rev, by}` — durable. A canvas that is behind learns immediately
  instead of finding out when its own next save is refused.
- `peers {peers:[…]}` — ephemeral (no SSE id, never ringed), republished when
  the roster changes. Presence is only true right now; a replayed copy would
  assert that someone is present who left.

Identity and colour are stamped server-side from the session, so a cursor can't
be labelled with a colleague's name and the same person reads the same colour
in everyone's view. There is no per-frame visibility rule — a board has no ACL
of its own, so the endpoint's `CapRead` check plus the 25 s keepalive
revocation tick is the whole entitlement story.

The generic ring/replay/reset machinery now lives in **`internal/sse`**, shared
with chat. It makes no authorization decision: visibility rides through it as
an opaque type parameter and the owning feature decides. Chat's `Hub` is a thin
wrapper carrying `Entitled`.

## Known gaps / next

- **No simultaneous editing yet.** Two people can now have a board open safely
  — neither can overwrite the other silently, and each sees the other arrive
  and save — but they still take turns: a conflict is resolved by loading
  theirs or keeping yours, not by merging. Live co-editing (patch sync + live
  cursors) is the next increment, and the pieces it needs are already in place:
  a persisted revision, a per-board SSE room, and presence.
- Documents are stored whole on every save; there is no incremental diffing.
  This is what live co-editing replaces: clients would exchange tldraw
  `RecordsDiff` patches against a server-held record map, which a Go server can
  apply without understanding tldraw's schema (a diff is set/set/delete on a
  map of opaque JSON records).
- **`fls-card` measure writes are a latent hazard for that work.**
  `cardshape.tsx` writes its measured `w/h` back to the document, and the
  measurement depends on font rendering, so two clients on different displays
  can disagree by a pixel or two. The tolerance is now ±3 px (`MEASURE_SLOP`)
  and read-only viewers skip the write entirely, which stops the disagreement
  from becoming a write-per-client. Under patch sync it would need a further
  guard — suppressing the measure write briefly after a remote patch touches
  that shape — or the two clients would ping-pong forever.
- No board thumbnails in the list.
- No cross-project "my whiteboards" screen (the store's self-describing project
  file supports adding one, as with tasks/production).
- The tldraw skin is deliberately light-touch — brand colours, type and radii.
  Deeper chrome restyling is possible but couples us to tldraw's internal class
  names.

## Verifying

```
go build ./... && go test ./...      # store tests: CRUD, snapshot round-trip,
                                     # document deleted with its board, caps,
                                     # corruption + future-version recovery
cd web && npx tsc --noEmit && npm run build
```

End-to-end (needs APS login): open a project → Whiteboards → create a board →
draw → place a task, a job/batch and a document card → reload and confirm the
board and its cards return → rename and delete a board → confirm a read-only
project member gets a non-editable canvas.

**Opening a board** — a board with content opens fitted to it (zoomed out to
show everything, never magnified past 100%); an empty board opens at the
origin at 100%. Open a board while the Whiteboards tab is NOT the active one
(select it, switch tabs, switch back) and confirm it is still fitted, then
drag the minimap and confirm the view pans both left and right and follows the
pointer — that combination is the regression this guards (see below).

**Styling** — select a frame and confirm the style panel offers a Color row,
that changing it repaints the frame's border and heading, and that it survives
a reload (a board saved before frame colours were enabled must still open).
Select a drawn shape and confirm the stroke row reads "Stroke" with a "Sketch"
first option, and the Fill overflow reads Hatched / Filled / Lined. Switch
language in Settings → Appearance and confirm tldraw's own menus follow the app
rather than the browser.

**Two people on one board** (two browser profiles, or a normal and a private
window):

1. Open the same board in both. Each header shows the other's avatar; closing
   one window removes it from the other within a moment.
2. Draw in A and wait for its autosave. B's banner says A saved and B's saving
   pauses — B is not left to discover it later.
3. In B choose "Load theirs": B's canvas becomes A's, saving resumes.
4. Repeat, and in B choose "Keep mine": B's version wins, and now **A** is the
   stale one and gets the banner on its next save.
5. Confirm a read-only member sees both the peer list and the change banner but
   never saves (watch the network tab for the absence of a PUT).
