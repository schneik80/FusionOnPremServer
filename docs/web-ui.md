# Web UI

The web UI is a React/MUI single-page app served by the Go binary (embedded in
release builds). It talks to the server over the same-origin JSON API under
`/api` — see [`docs/api.md`](api.md) for the upstream data layer and
[`docs/architecture.md`](architecture.md) for the HTTP route surface.

This page is the screen-by-screen reference. For a first-run walkthrough that
starts at install and ends with a team working in the app, read
[`docs/getting-started.md`](getting-started.md).

## Signing in

On load the SPA probes `GET /api/auth/me`:

- **Loading** — a centered spinner while the probe is in flight.
- **Signed out** — the **login screen**: a "Sign in with Autodesk" button that
  navigates to `/api/auth/login`. The server redirects to Autodesk; after you
  consent it redirects back, sets your session cookie, and the app loads. If a
  sign-in attempt fails, the server returns to `/?auth_error=<reason>` and the
  login screen shows a readable message.
- **Signed in** — the hub gate, or the app if your session is already locked
  to a hub.

The signed-in user's name (or email) and a **sign-out** button sit at the right
of the header. Sign-out drops the server session, clears all client caches, and
reloads at `/`. If your session expires while browsing, the next data call gets
a 401 and the app bounces you to sign in again.

See [`docs/authentication.md`](authentication.md) for the full flow.

## Choosing a hub

The app runs **locked to one hub** per session — hubs are isolation boundaries
for all local data (chat, tasks, production, whiteboards, pins, backups; see
[`docs/hubs/STATUS.md`](hubs/STATUS.md)). After login, the **hub gate** stands
between you and the app:

- **First run** — a full-screen picker lists your hubs; select one and enter.
- **Return visits** — the last hub is remembered in this browser and re-locked
  automatically (you only see a spinner, no picker).
- **Revoked access** — if the remembered hub is no longer in your hub list, the
  picker returns with a notice.

There is no hub switcher in the header or rail. **Switching hubs lives in
Settings → Connection** and performs a full teardown and reload (caches
cleared, page reloaded on the new hub — no re-sign-in needed). The hub's name
in the header is a shortcut: clicking it opens Settings directly on the
Connection tool.

## Layout

```
┌───────────────────────────────────────────────────────────────┐
│ ▣  HubName  v1.2.3                          ☾   user   ⏏      │  header
├──┬────────────────────────────────────────────────────────────┤
│  │ Hub › Project › Folder › Item                              │  breadcrumb
│R ├─────────────┬──────────────────────────────────────────────┤
│a │ Projects /  │ Project panel (Dashboard·Production·Tasks·   │
│i │ Contents    │ Whiteboards·Wiki·Chat)  — or —  Details      │
│l │             │ panel for a selected document                │
└──┴─────────────┴──────────────────────────────────────────────┘
```

- **Header** — the locked hub's name (click to switch, via Settings), server
  version, a light/dark toggle, your name, and sign-out.
- **Left rail** — the app switchers: **Browser**, **Production**
  (cross-project), **Tasks** (cross-project), then **Pins** and **Settings**.
  All rail apps stay mounted, so switching never loses your place.
- **Browser** — a progressive drill-down: the left column cross-slides between
  the project list and folder contents; the right pane slides between the hub
  dashboard, the project panel, and a selected document's details. Panes stay
  mounted, so scroll position and state survive navigation.
- **Breadcrumb** — Hub › Project › Folder… › Item; each segment is clickable.

Navigation state is mirrored into the URL: the address bar is a shareable
permalink to the current project/folder/document (and details tab), and
back/forward work as expected.

## Browsing designs

The **Projects** column lists the hub's projects. Drilling in, the **Contents**
column lists folders and documents, with a **sort menu** (name — folders
first —, last modified, or type) and an **upload** button. Rows show type
icons, lazy thumbnails, and a pin star on hover; folders drill deeper.

At the project root the right pane is the **project panel** (see below).
Selecting a document replaces it with the **details panel**.

### Details panel

A compact header shows the document's thumbnail, name, a lifecycle badge
(WIP / Version / Released – Rev X), a refresh action, and the always-visible
metadata (type, part number, description, material, version, dates). The type
reads as a friendly label and, for designs, appends the classification —
"3D Design — Assembly", "… — Part". Below it, tabs vary by document kind:

| Tab | Shows |
|-----|-------|
| **History** | Version history as a horizontal branch graph (lanes, milestone markers) |
| **Activity** | An isometric heat map of design activity over time |
| **Properties** | Physical/mass properties — mass, volume, surface area, density, bounding box |
| **BOM** | Flat bill of materials — Component / Part № / Material / Qty (occurrence count) |
| **Uses** | Components used by this design (or a drawing's source design), as a pan/zoom relation graph |
| **Where Used** | Designs that use this component, same graph |
| **Drawings** | Drawings made from this design |
| **Permissions** | The project's groups and roles; expand a group to list members (member listing needs hub-admin access, otherwise a "no permission" note is shown) |
| **Preview** | For uploaded (non-Fusion) files: an inline viewer — images, video, PDF, markdown, G-code, text/code — with a download fallback |

Designs get the full set; configured designs get History / Activity /
Properties / BOM / Permissions; drawings get History / Activity / Uses /
Permissions; uploaded files lead with Preview. Nodes in **Uses / Where Used /
Drawings** are clickable — selecting one navigates the browser straight to
that document.

Thumbnails and physical properties are generated asynchronously by APS; the UI
polls until each settles. Thumbnails are cached server-side and streamed
same-origin, so repeat views are instant.

## Project apps

At a project's root, the right pane is a tab strip: **Dashboard · Production ·
Tasks · Whiteboards · Wiki · Chat**. Every tab stays mounted — switching away
and back preserves scroll, drafts, and selection. Inside a folder only the
Dashboard remains (the other apps belong to the project, not a folder); your
tab choice is restored when you return to the root.

### Dashboard

Project overview widgets: a document-type donut (designs split into
assemblies/parts, classified lazily and capped with a visible "Load all"),
people & groups with roles, recently-modified documents, and a roll-up
activity heat map. The hub-level dashboard is a lighter landing pane with
project and pin counts.

### Tasks

A project task manager with three views over the same data:

- **List** — dense rows with a details pane.
- **Board** — a Kanban column per status; drag cards between columns to change
  status or within a column to reorder.
- **Schedule** — a Gantt chart with a backlog rail of unscheduled tasks (drag
  one onto the chart to schedule it). Drag a bar to move it, drag its edges to
  resize, drag bar-to-bar to link a dependency (Escape cancels a drag). Stage
  grouping bars, milestone diamonds, dependency arrows, progress fills, and a
  day/week/month zoom round it out.

Tasks carry status, priority, assignees, due dates, stages, dependencies, and
attached document references. Creation and editing are disabled (with the
reason) for read-only project roles.

### Wiki

A project wiki stored in the project's Fusion folder. The sidebar merges
published pages with local drafts (autosaved to this browser as you type, with
"behind"/"conflict" markers when the published page moves ahead of a draft).
The editor is a split-pane markdown editor with a formatting toolbar, image
embedding (from a URL with live preview, or from documents in the hub), and a
publish action that uploads the page to APS as a new version.

### Chat

Per-project channels (plus private channels and archiving), a message
timeline, **threads**, emoji **reactions**, typing indicators, and per-channel
unread counts (the Chat tab itself badges the total). Updates stream live over
SSE, with polling as a fallback while the stream is down. Enter sends;
Shift+Enter inserts a newline. Messages can carry `fls:` card tokens (below).
Posting rights follow your project role. See
[`docs/chat/STATUS.md`](chat/STATUS.md).

### Production

A light MES / product tracker. A **Job** is a graph of steps carrying
version-pinned plan documents and placeholder slots; a **Batch** is a dated
run that freezes the plan, so later edits never rewrite what a run recorded.
The job rail sits left; the selected job offers three views — an interactive
pan/zoom **flow canvas** of the step graph, a plain **list**, and **batches**
with a two-lane timeline (prove-out runs on top, production runs below).
Documents are pinned by browsing the hub or uploading.

A job can be **duplicated** from its header — the whole plan, none of the runs.
On the canvas, a palette picks whether **+** adds a work **step** or a
**decision**; with a step selected, the new node lands to its right already
connected, and double-clicking any node renames it in place. A decision is a
rounded diamond carrying named, colour-coded **results**, each of which routes
its own outgoing edge in that result's colour — so a traveller that forks
(pass / rework / scrap) is drawn as it actually runs. In a batch, steps can be
**hidden** — typically the branch that run didn't take — and the run header
toggles them back into view. See
[`docs/production/STATUS.md`](production/STATUS.md).

### Whiteboards

Per-project tldraw boards: a rail of boards (create, rename, delete) beside a
full drawing canvas with autosave. Toolbar actions drop live **`fls:` app
cards** onto the canvas — documents, tasks, jobs/batches — and **assembly
expansion** places a picked assembly as a card tree (root on top, children
fanned below, connected with arrows). See
[`docs/whiteboards/STATUS.md`](whiteboards/STATUS.md).

## Cross-project screens

Two rail apps aggregate across every project in the hub:

- **Tasks** — every task assigned to or created by you, with search and
  status/priority filters, and the same details pane as the project app.
- **Production** — runs in flight (planned/running batches) first, plus every
  job you own; clicking anything opens a read-only detail dialog.

## Pins

The star on any project, folder, or document row bookmarks it. The rail's
**Pins** dialog lists pins grouped by kind (projects / folders / documents)
with navigate and remove actions. Pins are scoped per hub and persisted on the
server.

## Uploads

The Contents column's upload button opens the upload dialog — a drop target
plus file browser — targeting the current folder. You can also drag files from
your desktop straight onto the window; a full-screen overlay names the target
folder. Uploads run as **background jobs**: closing the dialog doesn't stop
them, and a footer overlay shows aggregate progress with cancel/dismiss and a
button back into the per-file job view. Version resolution happens
server-side, so re-uploading a file creates a new version.

## `fls:` cards

Compact pseudo-URL tokens — `fls:doc`, `fls:task`, `fls:job`, `fls:batch` —
can live inline in chat messages, wiki pages, and task bodies. At render time
each token unfurls into a rich card (thumbnail, title, status) that links to
the thing it names. The same cards are what whiteboards place on the canvas.

## Settings console

The rail's Settings button opens a master-detail admin console with six tools:

- **Appearance** — theme preference (light / dark / system), the UI
  **language** (English, German, French, Spanish, Italian, Portuguese — see
  [`docs/i18n/STATUS.md`](i18n/STATUS.md)), and custom theme colors per mode.
- **Connection** — the session's hub lock (the only place hubs are switched),
  the listen **port** (changing it rebinds the server in place and the page
  reconnects on the new port; read-only in `-dev` mode), the read-only APS
  region, and build info.
- **Uptime** — live process status: start time, uptime, requests served, app
  and Go versions.
- **Backups** — the per-hub GFS backup schedule (folder, daily time; the
  server keeps 7 daily / 4 weekly / 12 monthly), run-now, and per-snapshot
  **verify** (integrity re-check) and **restore** (with an automatic
  pre-restore safety snapshot and reconnect).
- **Logs** — the server's rotated log files, viewable in place.
- **Data** — disk usage of the local per-project stores (chat, tasks,
  production, whiteboards), per-project typed-confirm deletion of that local
  data (never touches Fusion documents), and a stale-artifact cleanup.

## Theming and language

The header toggle flips light/dark; Settings → Appearance adds the "system"
preference and custom color overrides. Theme mode and custom colors are stored
**per hub** — a deliberate cue so different clients' hubs can look different
at a glance. The language choice is global to the browser and switches the
whole UI live; user-entered data (names, messages, pages) is never translated.
