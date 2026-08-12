# Getting started

A guided walkthrough from an empty machine to a team working in
fusionlocalserver. It covers **every capability the app ships**, in the order
you meet them: install → sign in → pick a hub → browse designs → the five
project apps → cross-project screens → notifications → the admin console →
backups → inviting your team.

If you only want the reference material, jump to the
[documentation map](#where-to-go-next) at the end. If something breaks, see
[`debugging.md`](debugging.md).

> Screenshots referenced below live in
> [`images/getting-started/`](images/getting-started/); that folder's
> [`README.md`](images/getting-started/README.md) is the shot list.

---

## 1. What this is

fusionlocalserver is a **single Go binary** you run on your own machine or a
LAN host. It serves a React web UI and a JSON API over your **Autodesk
Platform Services (APS) / Fusion Team** data.

Two things make it different from a shared cloud tool:

- **Every visitor signs in with their own Autodesk account.** The server never
  holds a shared credential; it proxies each person's calls under their own
  identity, so Fusion Team's own permissions decide what they can see. Tokens
  stay server-side and never reach browser JavaScript.
- **Your team's own data stays local.** Tasks, chat, production jobs,
  whiteboards and pins are stored as plain JSON files on the host that runs
  the server — not in anyone's cloud. (Wiki pages are the deliberate
  exception: they are published into Fusion Team so they travel with the
  project.)

| Lives in Fusion Team (APS) | Lives on your server |
|---|---|
| Designs, drawings, uploaded files, versions | Tasks & the Gantt schedule |
| Project membership and roles | Chat channels, threads, reactions |
| Wiki pages (published `.md` files) | Production jobs, steps, batches |
| Thumbnails, physical properties, BOM | Whiteboards, pins, notifications |

---

## 2. Before you start

You need:

- An [Autodesk account](https://accounts.autodesk.com) with access to at least
  one **Fusion Team hub** — and one for each teammate who will use the server.
- macOS 12+, Linux, or Windows 10+.
- A free TCP port on the host (default `8080`).

That is all for a released binary. Building from source additionally needs
**Go 1.25+** and **Node/npm**.

---

## 3. Install

### Homebrew (macOS / Linux)

```sh
brew install schneik80/fusionlocalserver/fusionlocalserver
```

### Release binary

```sh
VERSION=$(curl -s https://api.github.com/repos/schneik80/fusionlocalserver/releases/latest \
  | grep '"tag_name"' | cut -d'"' -f4 | tr -d v)
curl -L "https://github.com/schneik80/fusionlocalserver/releases/latest/download/fusionlocalserver-${VERSION}-darwin-arm64.tar.gz" | tar xz
sudo mv fusionlocalserver /usr/local/bin/
```

Released binaries ship the web UI embedded and a publisher client ID baked in,
so there is **nothing to configure** — skip to [First run](#4-first-run).

### From source

Register a web app at [aps.autodesk.com/myapps](https://aps.autodesk.com/myapps)
with scope `data:read user-profile:read` (add `data:write data:create` to use
wiki publishing and uploads), then:

```sh
git clone https://github.com/schneik80/fusionlocalserver
cd fusionlocalserver
echo "your-client-id" > .aps-client-id             # git-ignored
echo "https://your-host:8080" > .aps-public-url    # optional but recommended
make build
```

`make build` runs the Vite build, embeds the UI (`-tags embed_ui`) and bakes
in the client ID. See [`development.md`](development.md) for the full
configuration precedence and release pipeline.

**Callback registration is the one step people get wrong.** APS validates the
OAuth `redirect_uri` by *exact match* — no wildcards. `localhost` ≠
`127.0.0.1`, every LAN IP or hostname is distinct, and `-tls` makes the scheme
`https`. The simplest correct setup is to pick **one** stable address everyone
uses, put it in `.aps-public-url` (or pass `-public-url`), and register exactly
one callback:

```
https://your-host:8080/api/auth/callback
```

<!-- SHOT 01 -->
![Registering the callback URL on the APS app](images/getting-started/01-aps-callback.png)

---

## 4. First run

```sh
make run          # from source: build the UI + binary, then serve
# or, for an installed binary:
fusionlocalserver -tls
```

The server binds `0.0.0.0:8080` over **HTTPS** and logs every reachable URL:

```
server starting  addr=0.0.0.0:8080 tls=true ...
reachable on the LAN  url=https://localhost:8080
reachable on the LAN  url=https://192.168.1.50:8080
```

The first `-tls` run generates and caches a self-signed certificate under
`~/.config/fusionlocalserver/`. Browsers warn once per device — accept it, or
supply your own PEM pair with `-tls-cert` / `-tls-key`.

> ⚠️ **Don't disable TLS on a shared network.** Over plain HTTP the session
> cookie can't be marked `Secure`, so anyone sniffing the wire could hijack a
> session. `make run` keeps `-tls` on for exactly this reason; only override it
> (`make run TLS=`) behind a TLS-terminating proxy or for loopback testing.

### Useful flags

| Flag | Purpose |
|---|---|
| `-tls` | HTTPS (on by default via `make run`); self-signed cert auto-generated |
| `-public-url` | The canonical external base URL — makes one APS callback enough |
| `-v` | Verbose logging: per-request lines and redacted upstream API traces |
| `-dev` | Proxy the UI to the Vite dev server for hot reload |

The **listen port is changeable at runtime** from Settings → Connection; no
restart command needed.

### Running it without a terminal

`desktop/` holds small native tray apps that start, stop and monitor the
binary. The Linux tray app ships today
([`desktop/linux/`](../desktop/linux/README.md)); macOS and Windows are
planned.

---

## 5. Sign in

Open one of the logged URLs and click **Sign in with Autodesk**.

<!-- SHOT 02 -->
![The sign-in screen](images/getting-started/02-sign-in.png)

You are redirected to Autodesk, you consent, and you land back in the app. The
server exchanges the code using **PKCE**, stores the tokens in its own session
store, and hands your browser only an opaque `HttpOnly` cookie. Sessions
survive a server restart (they're kept in an encrypted `sessions.enc`), and
access tokens refresh automatically.

Your name and a sign-out button sit at the right of the header. If a login
fails, the login screen shows the reason — the most common one is a
`redirect_uri` that doesn't match your APS registration exactly.

Full detail: [`authentication.md`](authentication.md).

---

## 6. Pick your hub

The app runs **locked to one hub per session**. Hubs are treated as hard
client boundaries — every byte of local data (tasks, chat, production,
whiteboards, pins, notifications, backups, even your theme colors) is
partitioned per hub, and no API, admin or backup path can cross the partition.

<!-- SHOT 03 -->
![The hub gate listing available hubs](images/getting-started/03-hub-gate.png)

- **First run** — a full-screen picker lists your hubs. Choose one and enter.
- **Return visits** — the last hub is remembered in that browser and re-locked
  automatically; you just see a spinner.
- **Access revoked** — if the remembered hub has disappeared from your list,
  the picker returns with a notice.

There is deliberately **no hub switcher in the header**. Switching lives in
**Settings → Connection** and performs a full teardown and reload (caches
cleared, page reloaded — but no second sign-in). Clicking the hub name in the
header is a shortcut straight to that screen.

Why it's this strict: [`hubs/STATUS.md`](hubs/STATUS.md).

---

## 7. The layout

```
┌───────────────────────────────────────────────────────────────┐
│ ▣  HubName  v1.2.3                     🔔  ☾   user   ⏏       │  header
├──┬────────────────────────────────────────────────────────────┤
│  │ Hub › Project › Folder › Item                              │  breadcrumb
│R ├─────────────┬──────────────────────────────────────────────┤
│a │ Projects /  │ Project panel (Dashboard·Production·Tasks·    │
│i │ Contents    │ Whiteboards·Wiki·Chat)  — or —  Details       │
│l │             │ panel for a selected document                 │
└──┴──────────────────────────────────────────────────────────---┘
```

<!-- SHOT 04 -->
![The main three-column layout](images/getting-started/04-layout.png)

- **Header** — the locked hub's name (click to switch), the server version,
  the [notification bell](#12-the-notification-bell), a light/dark toggle,
  your name, and sign-out.
- **Left rail** — the app switchers: **Browser**, **Production**
  (cross-project), **Tasks** (cross-project), then **Pins** and **Settings**.
  Every rail app stays mounted, so switching never loses your place.
- **Breadcrumb** — Hub › Project › Folder… › Item, each segment clickable.
- **Permalinks** — navigation state is mirrored into the address bar, so the
  URL is a shareable link to the current project/folder/document (and Details
  tab), and back/forward work as expected.

---

## 8. Browsing designs

The **Projects** column lists the hub's projects; drilling in, the
**Contents** column lists folders and documents with a **sort menu** (name
with folders first, last modified, or type) and an **upload** button. Rows
carry type icons, lazily-loaded thumbnails, and a pin star on hover.

<!-- SHOT 05 -->
![Browsing a project's contents](images/getting-started/05-browser-contents.png)

Selecting a document swaps the right pane for the **details panel**: the
thumbnail, name, a lifecycle badge (WIP / Version / Released – Rev X), and
always-visible metadata (type, part number, description, material, version,
dates). For designs the type reads as "3D Design — Assembly" or "… — Part",
classified on demand.

<!-- SHOT 06 -->
![The details panel for a design](images/getting-started/06-details-panel.png)

Below that, the tabs vary by document kind:

| Tab | What it shows |
|---|---|
| **History** | Version history drawn as a horizontal **branch graph** — lanes, merges, milestone markers |
| **Activity** | An **isometric heat map** of the design's activity, with day/week/month/year windows and a prev/next stepper |
| **Properties** | Physical/mass properties — mass, volume, surface area, density, bounding box |
| **BOM** | Flat bill of materials — Component / Part № / Material / Qty |
| **Uses** | Components used by this design, as a pan/zoom relation graph |
| **Where Used** | Designs that use this component — plus **local** sources: tasks, chat messages, whiteboards, jobs and batches that reference it |
| **Drawings** | Drawings made from this design |
| **Permissions** | The project's groups and members with their effective roles, traced layer by layer down the folder path |
| **Preview** | For uploaded (non-Fusion) files: inline viewer for images, video, PDF, markdown, G-code, text and code, with a download fallback |

<!-- SHOT 07 -->
![The History branch graph](images/getting-started/07-history-graph.png)

<!-- SHOT 08 -->
![The Activity isometric heat map](images/getting-started/08-activity-heatmap.png)

<!-- SHOT 09 -->
![The Uses / Where Used relation graph](images/getting-started/09-relation-graph.png)

<!-- SHOT 10 -->
![The Permissions explorer](images/getting-started/10-permissions.png)

Nodes in Uses / Where Used / Drawings are **clickable** — selecting one
navigates the browser straight to that document. Thumbnails and physical
properties are generated asynchronously by APS; the UI polls until each
settles, then caches thumbnails server-side so repeat views are instant.

> **A note on quotas.** APS enforces a per-minute cost quota, so the app never
> fans out one call per row. Per-item work (classification, thumbnails) waits
> until the row nears the viewport, and per-container work is capped with a
> visible **Load all** button. Nothing is ever capped silently.

### Uploading files

Use the Contents column's upload button, or just **drag files from your
desktop onto the window** — a full-screen overlay names the target folder.

<!-- SHOT 11 -->
![Drag-and-drop upload with the progress footer](images/getting-started/11-upload.png)

Uploads run as **background jobs**: closing the dialog doesn't cancel them, and
a footer overlay shows aggregate progress with cancel/dismiss and a way back
into the per-file view. Version resolution happens server-side, so re-uploading
a file of the same name creates a new **version** rather than a duplicate —
matching Fusion Team's own semantics. Large files are split into signed-S3
parts. Mechanics: [`upload-to-folder.md`](upload-to-folder.md).

---

## 9. The project apps

At a project's root the right pane is a tab strip: **Dashboard · Production ·
Tasks · Whiteboards · Wiki · Chat**. Every tab stays mounted, so switching away
and back preserves scroll, drafts and selection — and your tab choice is
restored when you come back to the project root.

<!-- SHOT 12 -->
![The project panel tab strip](images/getting-started/12-project-tabs.png)

### Dashboard

Project overview widgets: a document-type donut (designs split into assemblies
and parts), people and groups with their roles, recently-modified documents,
and a roll-up activity heat map. The hub-level dashboard is a lighter landing
pane with project and pin counts.

### Tasks

Three views over one task set.

<!-- SHOT 13 -->
![The Kanban board](images/getting-started/13-tasks-kanban.png)

- **List** — dense rows with a details pane.
- **Board** — a Kanban column per status (`todo` / `in progress` / `blocked` /
  `done`); drag cards between columns to change status, or within a column to
  reorder.
- **Schedule** — a hand-drawn SVG **Gantt chart** with a backlog rail of
  unscheduled tasks.

<!-- SHOT 14 -->
![The Gantt schedule with dependencies and a stage bar](images/getting-started/14-tasks-gantt.png)

In the Gantt you can drag a bar to move it, drag its edges to resize, drag the
progress knob, drag from one bar to another to create a **finish-to-start
dependency** (click an arrow to remove it), and drag a backlog row onto the
chart to schedule it. Escape cancels any drag. Give several tasks the same
**stage** and a derived stage bar appears above them — dragging it moves every
child atomically, and its span and progress roll up automatically.

Tasks carry status, priority, assignees, due dates, stages, dependencies and
attached document references. A **due date is a deadline, not a plan** — it
renders as a flag and never moves when you reschedule a bar. Milestones are
zero-length bars drawn as diamonds. Creation and editing are disabled, with
the reason shown, for read-only project roles.

Reference: [`tasks/STATUS.md`](tasks/STATUS.md).

### Wiki

A project markdown wiki whose published pages are real `.md` files in a
**Wiki folder in Fusion Team** — so they travel with the project, are governed
by its permissions, and are versioned by Fusion like any other file.

<!-- SHOT 15 -->
![The wiki split-pane editor](images/getting-started/15-wiki-editor.png)

Editing is two-tier. **Drafts** autosave to your browser as you type and never
leave your device until you publish; **pages** are what's in Fusion Team. The
sidebar merges both and marks each entry `draft`, `published`, `modified`,
`behind` (someone else published a newer version) or `conflict` (both moved).
Publishing a stale page is refused with a confirm-and-overwrite prompt rather
than silently forking it, and the `behind`/`conflict` banners offer the inverse
— pull the live version into your draft.

The editor is a split-pane CodeMirror markdown editor with a formatting
toolbar, image embedding (upload, browse the hub, or paste a URL with live
preview), tables, task lists, fenced-code highlighting, heading anchors and an
"On this page" table of contents in the reader.

Reference: [`wiki/STATUS.md`](wiki/STATUS.md).

### Chat

Per-project channels — public and private — with threads, emoji reactions,
typing indicators, per-channel unread state, rename/topic/archive for
moderators, and a member picker for private channels.

<!-- SHOT 16 -->
![A chat channel with a thread open](images/getting-started/16-chat.png)

Messages stream live over **SSE**, with polling as a fallback if the stream
drops. Enter sends, Shift+Enter adds a newline. Typing `@` opens an
autocomplete over the project roster and inserts a **mention**, which lands in
that person's [notification bell](#12-the-notification-bell).

Access is derived from your **APS project role** — there is no parallel
permission system to maintain:

| APS project role | What you can do in the local apps |
|---|---|
| Viewer, Reader | Read |
| Editor | Post, react, edit your own, create channels, create/edit tasks and jobs |
| Manager, Administrator | All of the above plus moderate: rename/archive channels, delete others' content |

Someone whose access comes only through a **group** (so they aren't listed
individually on the project roster) still gets contributor rights — read and
post, never moderate. Anything else is denied by default.

Reference: [`chat/STATUS.md`](chat/STATUS.md),
[`security/CHAT-SECURITY.md`](security/CHAT-SECURITY.md).

### Production

A light **MES / product tracker**. It exists to answer one question paper
travellers always lose: *which document version did this run actually use?*

<!-- SHOT 17 -->
![The production flow canvas](images/getting-started/17-production-flow.png)

- A **Job** is the as-planned process: a graph of **Steps**, laid out on a
  pan/zoom canvas you drag into shape (positions persist). Each step carries
  version-**pinned** plan documents and **placeholder** slots for documents
  that only exist per run.
- A **Batch** is a dated as-run instance. Creating one **freezes** the plan —
  steps, pinned versions and placeholders are deep-copied — so later edits to
  the job can never rewrite, hide or retroactively re-score a run that already
  happened.
- **Fulfillments** fill a batch's placeholders, and **as-run artifacts** record
  what actually happened (say, NC code modified at the machine).

<!-- SHOT 18 -->
![The batch timeline with prove and production lanes](images/getting-started/18-production-batches.png)

Pins are exact and resolved **server-side**, so a client can never assert a
version it didn't get. Documents come from browsing the hub or from an upload;
production files its own uploads under `<project>/Jobs/<job>/<batch>/`,
creating and reusing folders as needed. The Batches view is a two-lane
timeline — prove-out runs above, production runs below — with per-step frozen
documents and a completeness bar.

Reference: [`production/STATUS.md`](production/STATUS.md).

### Whiteboards

Per-project [tldraw](https://tldraw.dev) boards: a rail of boards (create,
rename, delete) beside a full drawing canvas that autosaves, with **live
simultaneous editing** between everyone on the same board.

<!-- SHOT 19 -->
![A whiteboard with live app cards](images/getting-started/19-whiteboard.png)

Beyond sketching, toolbar actions drop **live `fls:` app cards** — documents,
tasks, jobs, batches — onto the canvas, and **assembly expansion** places a
picked assembly as a card tree (root on top, children fanned below, connected
with arrows).

> **Licence key required.** tldraw is not free for production use. Put your key
> in `web/.env.local` as `VITE_TLDRAW_LICENSE_KEY` and rebuild the web app;
> the shipped evaluation key **expires 2026-10-29**, after which the canvas
> starts silently disappearing a few seconds after it loads. That symptom is
> tldraw's licence gate, not a bug — see
> [`whiteboards/STATUS.md`](whiteboards/STATUS.md).

---

## 10. Cross-project screens

Two rail apps aggregate across every project in the hub:

<!-- SHOT 20 -->
![The cross-project My Tasks screen](images/getting-started/20-my-tasks.png)

- **Tasks** — every task assigned to or created by you, with search and
  status/priority filters, over the same details pane as the project app.
- **Production** — runs in flight (planned and running batches) first, then
  every job you own; clicking anything opens a read-only detail dialog.

---

## 11. Pins

The star on any project, folder or document row bookmarks it. The same star
also appears on the things the server owns itself — a whiteboard in the board
rail, a task's header, a job or a batch header, and the channel header in
Chat — so the record you actually work in is bookmarkable, not just the
document it came from.

The rail's **Pins** dialog lists your pins grouped by kind — projects,
folders, documents, whiteboards, tasks, production, channels — with navigate
and remove actions. Navigating a whiteboard or a channel takes you to it;
a task, job or batch opens its detail dialog over the list, so you can work
through several without reopening the dialog each time. A pinned Fusion
document also carries **Open in Fusion** — and **Insert** if it is a 3D
design — so a pin is a shortcut into the desktop client, not just into this
browser. Pins are per hub and stored on the server, so they follow you between
browsers.

---

## 12. The notification bell

The bell in the header is your single inbox for everything the server observed
about *you*, across every project in the hub.

<!-- SHOT 21 -->
![The notification bell inbox](images/getting-started/21-notifications.png)

| Kind | Fires when |
|---|---|
| **Mention** | Someone `@`-mentions you in a chat message |
| **Assigned** | A task is created assigned to you, or reassigned to you |
| **Due soon** | A task assigned to you is due within two days |
| **Overdue** | A task assigned to you is past its due date |
| **Production** | A batch on a job you own is created, or its status advances |
| **Chat unread** | A channel you participate in has messages past your read cursor |

You are never notified about your own actions. Chat unreads are computed live
from your read cursors, so opening the channel makes the row disappear — which
is also why they can't be dismissed individually. Clicking a row marks it read
and navigates to the relevant project tab. The bell polls every 45 seconds.

Reference: [`notifications/STATUS.md`](notifications/STATUS.md).

---

## 13. `fls:` cards

Four compact tokens — `fls:doc`, `fls:task`, `fls:job`, `fls:batch` — can live
inline in chat messages, wiki pages and task descriptions. At render time each
one unfurls into a live card with a thumbnail, title and current status, and
links to the thing it names.

<!-- SHOT 22 -->
![An fls: card unfurled in a chat message](images/getting-started/22-fls-card.png)

They are inserted from the chat composer, the wiki toolbar and the task and
production dialogs; the same cards are what whiteboards place on the canvas.
In markdown they're written as ordinary links, so a page still reads sensibly
in any other renderer. Server-side, the same tokens power the **Where Used**
tab's local sources — the reverse lookup of everything that references a
document.

Click a card to select it and a small action bar appears under it. On a
document card that is **Navigate** (move the browser to it), **Details** (the
card turns over to show its metadata), and — for a Fusion document — **Open**,
**Insert** and **Archive**, exactly as on the details header. An uploaded file
gets a plain **Download** instead, because only it has a file to hand back. On
a production card, Archive gives you the *pinned* version the card's badge
names, not whatever the design has become since.

---

## 14. The settings console

The rail's Settings button opens a master-detail admin console with six tools.

<!-- SHOT 23 -->
![The settings console](images/getting-started/23-settings.png)

- **Appearance** — theme preference (light / dark / system), the UI
  **language** (English, Deutsch, Français, Español, Italiano, Português —
  switching live, no reload), and custom theme colors per mode. Theme and
  colors are stored **per hub**, deliberately, so two clients' hubs can look
  different at a glance; the language is global to the browser. User-entered
  data is never translated. ([`i18n/STATUS.md`](i18n/STATUS.md))
- **Connection** — the session's hub lock (the only place hubs are switched),
  the listen **port** (changing it rebinds the server in place and the page
  reconnects on the new port), the read-only APS region, and build info.
- **Uptime** — start time, uptime, requests served, app and Go versions, log
  path and size.
- **Backups** — the per-hub backup console. See below.
- **Logs** — the server's log, viewable and downloadable in place.
- **Data** — disk usage per store and per project, per-project deletion of
  local app data behind a typed confirmation, and a stale-artifact cleanup.
  Deleting here **never touches Fusion documents** — only this server's local
  data.

> **Every authenticated session is an admin.** That's the settled posture for a
> single-user local server: there are no separate admin roles, and destructive
> operations are protected by typed confirmations instead. On a shared LAN
> deployment, anyone who can sign in can read logs, delete local project data
> and trigger a restore. Plan your deployment accordingly.

Reference: [`admin/STATUS.md`](admin/STATUS.md).

---

## 15. Set up backups

Do this on day one. Everything in Fusion Team is Autodesk's problem; everything
in the local stores is yours.

<!-- SHOT 24 -->
![The backups tool with a snapshot table](images/getting-started/24-backups.png)

1. Open **Settings → Backups** and pick a destination folder (the picker
   browses the server's filesystem, directories only).
2. Set a daily time — default `03:30` — and enable the schedule.
3. Click **Run now** to take a manual snapshot and confirm it lands.

The engine keeps **7 daily / 4 weekly / 12 monthly** snapshots per hub, plus
`manual/` and `pre-restore/` tiers that are never pruned. Each snapshot carries
a `manifest.json` with a SHA-256 and schema version per file and the hub's
identity.

- **Verify** re-hashes every file, re-parses it, and checks that nothing was
  written by a newer build than the one running.
- **Restore** validates everything before writing a single byte, takes an
  automatic pre-restore safety snapshot of your current data, requires you to
  type the snapshot's timestamp to confirm, and then restarts the listener
  while the page shows a reconnect screen.

What's backed up is an allow-list: the four project stores, pins, and a copy of
`config.json` with `client_secret` blanked. Session tokens, TLS keys and logs
are unreachable by construction — no backup source reads them. A restore
refuses any snapshot stamped for a different hub.

The backup destination is trusted storage: snapshots are plain files, so the
manifest hashes detect corruption, not tampering by someone who can write
there. If the server is stopped at the scheduled time, that run is skipped —
there is deliberately no catch-up at startup.

Reference: [`backup/STATUS.md`](backup/STATUS.md).

---

## 16. Bring your team in

1. **Pick one stable address** — a hostname or a static IP — and run the server
   with `-public-url` set to it (or bake it into `.aps-public-url` at build
   time). Register that one callback on the APS app.
2. **Keep `-tls` on.** The session cookie is only marked `Secure` over HTTPS.
3. **Send everyone the URL.** Each person clicks *Sign in with Autodesk* and
   authenticates as themselves — there is no user list to manage here.
4. **Their access is their Fusion Team access.** Add or remove people in Fusion
   Team, and this server follows: project role decides read/post/moderate in
   tasks, chat and production, and Fusion permissions decide what they see in
   the browser and the wiki.
5. **Everyone picks the same hub** at the gate. Anyone who picks a different
   hub is working in a completely separate partition of local data — that's the
   point of hub isolation, but it does mean a teammate who "can't see the
   tasks" has usually locked to the wrong hub.

For non-US hubs, set the region the server queries — `APS_REGION=EMEA` or
`APS_REGION=AUS` (default US). This is process-global, not per user; see
[`development.md`](development.md) for configuration precedence.

---

## 17. When something goes wrong

| Symptom | Likely cause |
|---|---|
| Login bounces back with an error | The `redirect_uri` doesn't exactly match your APS registration — `localhost` ≠ `127.0.0.1`, and `-tls` makes it `https` |
| Browser warns about the certificate | Expected on the first visit per device with the self-signed cert; accept it, or supply your own PEM pair |
| A teammate sees no tasks/chat | They locked to a different hub — Settings → Connection |
| "Hub not selected" and the app resets | The session's hub lock expired or was cleared; the gate returns and you re-pick |
| Whiteboard vanishes a few seconds after opening | The tldraw licence key is missing or expired — see [`whiteboards/STATUS.md`](whiteboards/STATUS.md) |
| A list stops short with a "Load all" button | Intentional: APS quota protection, never a silent cap |
| Chat feels laggy over many tabs | HTTP/1.1's 6-connection limit; run with `-tls` so the browser uses HTTP/2 |

Run with `-v` for per-request lines and redacted upstream traces; read the log
in Settings → Logs or at `~/.config/fusionlocalserver/server.log` (rotated at
10 MB, three compressed generations kept).

[`debugging.md`](debugging.md) covers what to capture and how to file a bug.

---

## Where to go next

| Doc | What it covers |
|---|---|
| [`web-ui.md`](web-ui.md) | The full UI reference, screen by screen |
| [`architecture.md`](architecture.md) | C4 diagrams, package layout, request/session flow, stores |
| [`authentication.md`](authentication.md) | Per-user OAuth (PKCE), sessions, cookies, token refresh |
| [`hubs/STATUS.md`](hubs/STATUS.md) | Hub isolation: the security model and its invariants |
| [`tasks/STATUS.md`](tasks/STATUS.md) | Tasks: Kanban, the Gantt schedule model, cross-project view |
| [`wiki/STATUS.md`](wiki/STATUS.md) | Wiki: drafts, publishing to Fusion Team, images, conflicts |
| [`chat/STATUS.md`](chat/STATUS.md) | Chat: channels, threads, SSE, authorization |
| [`production/STATUS.md`](production/STATUS.md) | Production: jobs, steps, version pinning, batches |
| [`whiteboards/STATUS.md`](whiteboards/STATUS.md) | Whiteboards: tldraw integration, app cards, the licence key |
| [`notifications/STATUS.md`](notifications/STATUS.md) | The notification bell and what emits into it |
| [`permissions/STATUS.md`](permissions/STATUS.md) | The Permissions tab and the MDM permission model |
| [`activity-reports/STATUS.md`](activity-reports/STATUS.md) | Design activity: the GraphQL source and the heat map |
| [`backup/STATUS.md`](backup/STATUS.md) | Backups: GFS rotation, manifests, verify, restore |
| [`admin/STATUS.md`](admin/STATUS.md) | The settings console and data management |
| [`i18n/STATUS.md`](i18n/STATUS.md) | Localization: locales, conventions, adding strings |
| [`upload-to-folder.md`](upload-to-folder.md) | The APS upload sequence, end to end |
| [`api.md`](api.md) | The APS GraphQL client: queries, retries, debug logging |
| [`development.md`](development.md) | Building from source, configuration, releases, dependencies |
| [`testing.md`](testing.md) | Test strategy and how to run or extend the suite |
| [`debugging.md`](debugging.md) | Logging, `-v`, and reporting a bug |
