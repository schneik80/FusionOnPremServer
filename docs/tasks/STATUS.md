# Tasks — status

A per-project task manager: the first of the local project apps beside Wiki,
Chat, Production and Whiteboards. Three views over one task set — a list with
details, a Kanban board, and a Gantt schedule — plus a cross-project "my
tasks" screen on the nav rail. Projects and users stay APS-side (referenced by
URN / OIDC sub); the tasks themselves are **our** data, stored locally.

## Model

| Concept | What it is |
|---|---|
| **Task** | `t<n>` with a per-project counter, displayed as `T-<n>`. Title, description, status, priority, optional due date, optional assignee, attachments. |
| **Status** | `todo` / `inprogress` / `blocked` / `done` — doubles as the Kanban column set, in board order. |
| **Priority** | `low` / `medium` / `high` / `urgent`. |
| **Rank** | A float ordering tasks within their status column (floats leave headroom for midpoint inserts; appends land at max+1024). |
| **Schedule** | `startDate`/`endDate` (+ `progress`, `milestone`, `dependsOn`, `stage`) — the Gantt slice of a task. |
| **Attachments** | `fls:doc` / `fls:job` / `fls:batch` card tokens in `docRefs` (≤ 20), rendered as live cards. |

### Invariants (server-enforced in `tasks/store.go`)

- **The schedule is a pair.** `startDate` and `endDate` are set together or not
  at all — a task is "scheduled" iff both are present (`end >= start`,
  date-only `YYYY-MM-DD` so no timezone can shift a bar). Clearing is likewise
  atomic (`clearSchedule` drops start+end+milestone together). **`dueDate`
  stays an independent deadline** — it never moves with the schedule and
  renders as a flag on the Gantt, not as the bar's end.
- **A milestone is a point.** `milestone` requires a schedule with
  `endDate == startDate` (the create/update paths coerce the end date when
  only a start is sent).
- **Dependencies stay valid.** Every `dependsOn` entry (≤ 20) must name an
  existing task in the same project, self-references and cycles are rejected
  (iterative DFS), and deleting a task prunes it from every remaining
  `dependsOn` in the same atomic rewrite — no reader ever sees a dangling id.
- `progress` is 0–100; **0 doubles as "unset" by design.** `stage` is a free
  string (≤ 100 runes); the stage bar is always derived, never stored.
- Caps: 200-rune titles, 20 000-rune descriptions, 5 000 tasks/project,
  shifts bounded to ±3650 days.

## Layout

**Backend**
- `tasks/types.go`, `tasks/store.go` — one `tasks.json` per project under the
  session hub's profile: `hubs/<hubslug>/tasks/<sanitized-projectId>/`
  (see [hubs](../hubs/STATUS.md)). The shared local-store posture: atomic
  temp+rename writes, per-project mutex, `.bak` on corruption, future-version
  guard, schema provenance stamp (`internal/schemameta`, fileVersion 2), a
  `internal/migrate` registry (v1→v2 backfills the stamp from mtime), and
  clone + rollback on save failure. The file self-describes
  `projectId`/`hubId`/`projectName` so cross-project listings need no APS
  calls. `tasks/snapshot.go` streams every project file to the
  [backup](../backup/STATUS.md) engine under each project's mutex.
- `server/handlers_tasks.go`, `server/dto_tasks.go`, routes in
  `server/routes.go`. Authorization reuses `chat.Authorizer` — the caller's
  APS project role mapped to capabilities, no parallel permission system:
  `CapRead` to view, `CapPost` to create/edit (**any editor edits any task**,
  team-tracker semantics), `CapModerate`-or-creator to delete. The
  group-derived fallback applies unchanged (roster-unlisted users with project
  access get write, never moderate). Mutations pass a per-session op limiter
  (`taskOpLim`, burst 10).

**Frontend** — `web/src/tasks/`
- `TasksApp` — project tab (`{ active }` contract); view toggle + create
  button (disabled with the reason for read-only roles).
- `TaskListView` + `TaskDetails` — dense list with a detail pane;
  `TaskViewDialog` / `TaskEditDialog` / `QuickTaskDialog` are the shared
  view/edit/quick-create dialogs (also opened from cards elsewhere).
- `TaskKanban` — dnd-kit board (one column per status). Collision detection is
  pointer-based (`pointerWithin` + `rectIntersection`), because corner-distance
  strategies lose the middle columns on narrow windows. Drops PATCH
  status+rank optimistically.
- `TasksScreen` — the cross-project rail screen (`GET /api/tasks/mine`) with
  status/priority filters and search.
- **Gantt** — `gantt/`, a hand-rolled SVG chart (no chart library, per the
  repo convention): one `overflow:auto` container with CSS-sticky header and
  label column — no JS scroll syncing.
  - `ganttMath.ts` — pure geometry (dates ↔ pixels, row layout, arrow routing,
    calendar bands; unit-tested without a DOM). Dates parse to LOCAL midnight
    to dodge the classic gantt off-by-one. Day/week/month zoom units.
  - `GanttBar.tsx` — `GanttTaskBar` (rounded bar with progress fill, milestone
    diamond when `endDate == startDate`, due-date flag, overdue stroke) and
    `GanttStageBar`, the **derived** aggregate over a stage's children: its
    span and duration-weighted progress roll-up come from `rowModel`, are
    read-only, and are never stored on any task.
  - `useGanttDrag.ts` — pointer-event drag state machine: move, resize either
    end, drag the progress knob, drag from the link handle to create a
    dependency, and drag a backlog row onto the chart to schedule it. Previews
    are local state layered over query data so the 15 s poll can't stomp an
    in-flight drag; commits are optimistic with invalidate-on-error.
  - Dragging a **stage bar** moves every child together via
    `POST /api/tasks/shift` — one atomic store rewrite instead of N PATCHes
    (which would burst the op limiter and could partially fail). All-or-
    nothing: one unknown or unscheduled task rejects the whole shift, and due
    dates deliberately do not move (deadlines are not plans).
  - `GanttArrows.tsx` — finish-to-start dependency arrows (orthogonal
    segments); click to remove when writable. `BacklogRail.tsx` lists
    unscheduled tasks beside the chart.

**Cross-linking** — `components/taskcard/`
- `fls:task` tokens (`taskref.ts`) unfurl into a `TaskCard` wherever tokens
  render (chat, wiki, whiteboards, production batch refs), hydrated live via
  `GET /api/tasks/get`. `AttachTaskDialog` inserts them from the chat
  composer, the wiki toolbar and production.

## API

All IDs are query params (URNs contain `:`/`/`). `taskId` is the per-project
id (`t<n>`).

```
GET    /api/tasks        ?projectId            list + capabilities {write,moderate}
POST   /api/tasks        ?projectId            {hubId,projectName,title,…,schedule fields}
PATCH  /api/tasks        ?projectId&taskId     partial; clearAssignee/clearDueDate/clearSchedule explicit
DELETE /api/tasks        ?projectId&taskId     moderator or creator
GET    /api/tasks/get    ?projectId&taskId     one task (fls:task card hydration)
GET    /api/tasks/mine                         caller's tasks across every project (no roster checks)
POST   /api/tasks/shift  ?projectId            {taskIds,days} — atomic Gantt stage-bar drag
```

Every task DTO carries its project identity (`projectId`/`hubId`/
`projectName`); `docRefs`/`dependsOn` are always `[]`, never null. Patch
semantics: absent = unchanged; clearing optional fields is explicit because
JSON null-vs-absent isn't worth distinguishing on the wire.

## Residual risks / known gaps

- **`/api/tasks/mine` skips per-project roster checks by design** (N projects
  would mean N APS calls): a user removed from a project keeps seeing titles
  of their old tasks until those are edited or deleted. Every mutation still
  goes through per-project write authz.
- Delete is a **hard delete** (no tombstone) — dangling `fls:task` tokens in
  chat/wiki render as a designed "task not found" card.
- The schedule fields ride on the envelope as `omitempty` additions: an older
  binary opening a newer file loads fine, but its next rewrite silently drops
  the schedule fields (accepted residual, noted in `tasks/types.go`).
- `progress: 0` is indistinguishable from "no progress recorded".
- No per-task change history/audit trail; `updatedAt` is the only trace.

## Verifying

```
go test ./tasks/... ./server/...     # store: CRUD, ranks, schedule validation,
                                     # dependsOn cycles, shift, Mine, migration,
                                     # corruption + future-version recovery
cd web && npm run test && npm run build   # vitest covers ganttMath + hubKeys
```

End-to-end (needs APS login): create tasks in each status → drag cards across
Kanban columns and within one → schedule two tasks with a dependency in the
Gantt → drag/resize a bar, drag the progress knob, link two bars → give both
a stage and drag the stage bar (one `POST /shift`) → confirm a read-only
project member sees the board without edit affordances → check "My tasks" on
the rail from another project.
