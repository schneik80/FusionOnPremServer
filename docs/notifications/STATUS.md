# Notifications — status

A first-party, in-app **notification center**: a bell in the app chrome with an
unread-count badge that opens the caller's inbox — the things the server itself
observed about *them*. The Autodesk/Fusion notifications feed is app-gated (see
`docs/activity-reports/STATUS.md`), so nothing here is APS-sourced; every entry
is emitted by our own write paths.

Unlike the other local apps, the inbox is keyed by **user**, not project: the
bell is global across every project in the hub. It still shares the standard
local-store posture and lives inside the hub profile, so it stays per-hub —
a mention in hub A is invisible while the session is locked to hub B.

## What generates a notification

| Kind | Trigger | Target |
|---|---|---|
| `mention` | An `@name` (`fls:user` token) in a posted chat message. | Each mentioned user (private channels: members only). |
| `assigned` | A task is created assigned, or its assignee changes. | The new assignee. |
| `due_soon` | A task assigned to the caller is due within `dueSoonDays` (2). | The assignee (derived at read time). |
| `overdue` | A task assigned to the caller is past its due date. | The assignee (derived at read time). |
| `production` | A batch is created, or its status advances the run timeline. | The job's owner (`Job.CreatedBy`). |

**Chat activity beyond mentions stays out of the inbox** — general channel
traffic is surfaced by the existing per-channel unread pills, not the bell.

You are never notified about your own action (self-mention, self-assign,
changing your own job).

### Two emission modes

- **Event-sourced** (`mention`, `assigned`, `production`): the handler emits an
  inbox entry right after the store write succeeds. Best-effort — a failed emit
  is logged, never surfaced; the triggering write already committed.
- **Read-time reconciliation** (`due_soon`, `overdue`): these are *temporal* —
  no write fires when a task crosses its due date. Rather than a background
  scheduler, `reconcileTaskReminders` runs when the caller fetches their inbox:
  it scans *their own* assigned tasks (`tasks.Mine`) and emits any missing
  reminders. Idempotent on a dedupe key that folds in the due date, so a
  reminder fires at most once — but a rescheduled task earns a fresh one.

Mentions cost **no APS call** at either end: the `fls:user` token carries the
target id (parsed server-side in `notifications/mentions.go`), and a private
channel's ACL (`Channel.Members`) is local.

## Model

`notifications.Notification` (see `notifications/types.go`): `id` (`n<num>`,
per-user counter), `kind`, `hubId`/`projectId`/`projectName`, `actor` (who
caused it — nil for temporal kinds), `subject` (task title / channel name —
**user data, rendered verbatim, never translated**), `channel*`/`messageSeq`
(mention context), `dedupeKey`, `read`/`readAt`, `createdAt`.

Display text is composed **client-side** from `kind` + params so it localizes
across the six catalogs (`web/src/i18n/locales/*/notifications.json` +
`NotificationBell`'s `notifText`).

## Store posture

Standard local-store posture, keyed by the OIDC sub (falling back to lowercased
email — chat's `cursorKey` rule), one `<userkey>.json` per user under
`hubs/<slug>/notifications/`:

- versioned envelope + `schemameta` provenance stamp, atomic writes
  (`internal/atomicfile`), a per-user mutex, `.bak`-on-corruption, a
  future-version guard, and a `migrate` registry (`notifications/store.go`);
- `fileVersion` 1 (`CurrentVersion()` feeds backup verify/restore);
- inbox capped at `MaxPerUser` (500) — a flood prunes its oldest entries rather
  than refusing new ones, so the bell can never wedge;
- backups: a `StoreSource("notifications", …)` streams every `<userkey>.json`
  (`notifications/snapshot.go`); restore resets the store like the others.

The per-*project* admin tools (disk-usage table, per-project data deletion) do
**not** itemize notifications — a per-user inbox has no project dir to delete;
its bytes still roll into the hub's total.

## HTTP

| Route | Purpose |
|---|---|
| `GET /api/notifications` | The caller's inbox (newest first) + unread count. Runs the reminder reconciler. |
| `PATCH /api/notifications/read` | Mark a set of ids read → fresh unread count. |
| `POST /api/notifications/readall` | Mark the whole inbox read. |
| `DELETE /api/notifications?id=` | Dismiss one entry. |

The server is the **sole author** — clients read, mark read, and dismiss, but
never create. The inbox is the caller's own (no per-project roster check, like
`/api/tasks/mine`); the user key comes from the session identity, never the
wire, and the store from the session hub (`requireHub`), so one user can't
reach another's inbox and no hub can reach another's.

## Web

- `web/src/components/notifications/NotificationBell.tsx` — the AppBar bell +
  count pill (the `ChannelSidebar` pill precedent; no MUI `Badge` in this repo)
  + a `Popover` inbox. Rows compose localized text from `kind`, mark read on
  click, and best-effort navigate to the referenced project's tab.
- `web/src/notifications/{types,mentions}.ts` — wire types and the `fls:user`
  token (encode/parse/split), the inline sibling of the card tokens in
  `components/reftokens.ts`.
- Chat integration: `MessageComposer` gains an `@`-autocomplete over the
  project roster (`useChatMembers`) that inserts a `fls:user` token;
  `MessageList` unfurls the token to a highlighted `@Name` chip.
- Hooks `useNotifications` / `useNotificationActions` in `api/queries.ts`; the
  `notif` query-key prefix is excluded from localStorage persistence
  (`main.tsx`) — per-user realtime data.

## Tests

- `notifications/{store,mentions}_test.go` — dedupe, mark-read, prune, persist
  round-trip, mention parsing.
- `server/handlers_notifications_test.go` — reminder reconciliation (overdue /
  due-soon / far-future / not-mine, and idempotency).
- `web/src/notifications/mentions.test.ts` — token round-trip + split.

## Not done / possible follow-ups

- Editing a message to *add* a mention doesn't emit (create-only).
- Click-through lands on the project's tab, not the exact message/task/batch
  (the `ref` token is captured but not yet unfurled into a deep link).
- No realtime push — the bell polls (45 s); an SSE user-only channel could make
  it instant (the chat hub already supports user-addressed events).
