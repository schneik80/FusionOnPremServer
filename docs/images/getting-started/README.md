# Screenshot placeholders — Getting started guide

The images referenced by [`docs/getting-started.md`](../../getting-started.md)
are **not committed yet**. Each `<!-- SHOT nn -->` comment in that guide sits
directly above the `![…](images/getting-started/nn-name.png)` reference it
belongs to; drop the matching file in here and the guide renders complete.

## Conventions

- **Format** PNG. **Width** 1600 px (2× for a ~800 px content column); the
  guide scales them down, so don't ship anything narrower than 1200 px.
- **Theme** light mode, default color tokens, English UI — so the shots match
  the guide's wording and stay legible on both GitHub themes.
- **Chrome** capture the app's own window content, not the OS window frame or
  the browser toolbar, unless the shot is specifically about the URL.
- **Redaction** these go in a public repo. Use a demo hub and demo project
  names, blur or replace real people's names, avatars and email addresses, and
  never capture a hub id, project URN, session cookie or APS client ID.
- **Naming** `nn-slug.png`, matching the table below exactly.

## Shot list

| # | File | What to capture |
|---|---|---|
| 01 | `01-aps-callback.png` | The APS "My Apps" page for a registered web app, showing the Callback URL field filled with `https://your-host:8080/api/auth/callback`. Redact the client ID and secret. |
| 02 | `02-sign-in.png` | The signed-out login screen with the "Sign in with Autodesk" button. |
| 03 | `03-hub-gate.png` | The full-screen hub gate after login, listing two or more hubs. |
| 04 | `04-layout.png` | The whole app at a project root: header (hub name, bell, theme toggle, user), left rail, Projects/Contents column, project panel, breadcrumb. This is the orientation shot — everything visible at once. |
| 05 | `05-browser-contents.png` | The Contents column inside a folder: type icons, thumbnails, a hovered row showing the pin star, with the sort menu open. |
| 06 | `06-details-panel.png` | A selected design's details panel: thumbnail, name, lifecycle badge, metadata block, and the tab strip below it. |
| 07 | `07-history-graph.png` | The History tab — the horizontal branch graph with several versions, ideally including a branch and a milestone marker. |
| 08 | `08-activity-heatmap.png` | The Activity tab — the isometric heat map with a visible spread of activity and the day/week/month/year toggle. |
| 09 | `09-relation-graph.png` | The Uses or Where Used tab — the pan/zoom relation graph with bezier edges and several nodes. |
| 10 | `10-permissions.png` | The Permissions tab — the "With access" list showing groups and members by role, with the path-layers spine beside it. Redact real names. |
| 11 | `11-upload.png` | An upload in progress: the drop overlay naming the target folder, or the footer progress overlay with a couple of files running. |
| 12 | `12-project-tabs.png` | The project panel tab strip (Dashboard · Production · Tasks · Whiteboards · Wiki · Chat) with the Dashboard open, showing the donut and the recently-modified widget. |
| 13 | `13-tasks-kanban.png` | The Tasks Board view: all four status columns populated, ideally mid-drag with a card lifted. |
| 14 | `14-tasks-gantt.png` | The Tasks Schedule view: several bars with progress fills, at least one dependency arrow, a milestone diamond, a derived stage bar, and the backlog rail. |
| 15 | `15-wiki-editor.png` | The wiki split-pane editor: markdown on the left, rendered preview on the right, toolbar visible, and the sidebar showing a mix of published pages and drafts with their status markers. |
| 16 | `16-chat.png` | A chat channel with the message timeline, a reaction or two, an open thread panel, and the channel sidebar showing unread state. Use demo accounts. |
| 17 | `17-production-flow.png` | A job's Flow view: the pan/zoom canvas with several connected steps, at least one carrying a pinned document chip with its version badge. |
| 18 | `18-production-batches.png` | The Batches view: the two-lane timeline (prove above, production below) with several batches and a completeness bar. |
| 19 | `19-whiteboard.png` | A whiteboard with freehand sketching alongside live `fls:` app cards — ideally an expanded assembly card tree with its arrows. |
| 20 | `20-my-tasks.png` | The cross-project My Tasks rail screen with the filter chips and the details pane open on one task. |
| 21 | `21-notifications.png` | The header bell with an unread count, popover open, showing a mix of kinds (a mention, an assignment, an overdue task, a chat unread). |
| 22 | `22-fls-card.png` | A chat message or wiki page with an unfurled `fls:doc` and `fls:task` card visible inline. |
| 23 | `23-settings.png` | The settings console master-detail dialog with the tool list on the left and Appearance open on the right (theme, language picker, custom colors). |
| 24 | `24-backups.png` | Settings → Backups: the schedule config plus the snapshot table with daily/weekly/monthly rows and the verify/restore actions. |

## Optional extras

Not referenced by the guide today, but worth capturing if the tour grows:

- The hub-level dashboard landing pane.
- The Preview tab on an uploaded non-Fusion file (PDF or G-code).
- Settings → Data, showing per-project disk usage.
- The reconnect screen shown during a port change or restore.
