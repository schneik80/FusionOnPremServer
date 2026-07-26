> **SUPERSEDED (historical).** This was the original design brief; the shipped system uses a JSONL file store, stdlib SSE, and flat /api/chat/* routes instead of Postgres + centrifuge. See [PLAN.md](PLAN.md) for the adapted plan and [STATUS.md](STATUS.md) for what shipped.

# Project-Scoped Threaded Chat — Centrifuge Embedded Design (v2)

Chat is a **content region (tab)** inside a project, alongside Dashboard and Wiki.
All chat data hangs off your existing project + permission model:

```
Project ──< Channels (one auto-created "root" channel, users add more)
              └──< Messages (top-level)
                     └──< Thread replies (one level deep)
```

Go backend embeds `centrifuge` as the realtime transport; your server stays the
source of truth (REST → Postgres → publish). The frontend keeps one Centrifuge
connection for the whole app and subscribes/unsubscribes as the user moves
between projects and channels.

```
┌──────────────────────────────┐   REST (write/read)  ┌──────────────────────────┐
│ TS Frontend                  │ ───────────────────► │ Go Server                │
│  Project shell               │                      │  ├─ HTTP API             │
│   ├─ Dashboard tab           │                      │  ├─ centrifuge.Node      │
│   ├─ Wiki tab                │ ◄─── WebSocket ───── │  ├─ Project authz (yours)│
│   └─ Chat tab (this design)  │  (events, recovery)  │  └─ Postgres             │
└──────────────────────────────┘                      └──────────────────────────┘
```

---

## 1. Permission model — derive from project membership

Chat introduces **no separate membership table**. Access to a project's chat is
exactly your existing `project_members` (or equivalent) row, with chat
capabilities mapped from project roles:

| Capability                          | viewer | member | admin |
|-------------------------------------|:------:|:------:|:-----:|
| Read channels / threads             |   ✓    |   ✓    |   ✓   |
| Post messages & thread replies      |        |   ✓    |   ✓   |
| React, edit/delete **own** messages |        |   ✓    |   ✓   |
| Create channels                     |        |   ✓    |   ✓   |
| Rename/archive channels             |        |        |   ✓   |
| Delete others' messages             |        |        |   ✓   |

Adjust names to your actual role enum — the point is one function:

```go
// chat capability check, backed by your existing project permissions
func (a *Authz) Can(ctx context.Context, userID, projectID int64, cap ChatCap) bool
```

**Private channels add a second layer on top.** Access resolves in two steps:

```
canAccessChannel(user, channel) =
    ProjectAuthz.Can(user, channel.project_id, ChatCapRead)   -- layer 1: project
    AND (NOT channel.is_private
         OR EXISTS channel_members(channel.id, user.id)       -- layer 2: channel ACL
         OR ProjectAuthz.Can(user, project, ChatCapModerate)) -- project admins see all
```

Public channels (the default, including root) never touch `channel_members`.
Whether project admins can see private channels they're not in is a policy
knob — the clause above says yes; drop it if you want true opacity.

Removing a user from a project revokes chat instantly: REST checks fail, and
their live subscriptions are torn down (see §4, disconnect-on-removal).
Removing a user from a private channel does the same at channel scope
(delete the `channel_members` row + `node.Unsubscribe` on that one channel).

---

## 2. Postgres schema

`rooms` from v1 becomes `channels`, owned by a project. Everything else carries
over with `channel_id` in place of `room_id`.

```sql
CREATE TABLE channels (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id  BIGINT NOT NULL REFERENCES projects(id),
    name        TEXT NOT NULL,                -- "general", "design-reviews", ...
    topic       TEXT,
    is_root     BOOLEAN NOT NULL DEFAULT FALSE,
    is_private  BOOLEAN NOT NULL DEFAULT FALSE,  -- root channel is never private
    created_by  BIGINT REFERENCES users(id),  -- NULL for the auto-created root
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,

    UNIQUE (project_id, name)
);
-- exactly one root channel per project
CREATE UNIQUE INDEX idx_channels_one_root
    ON channels (project_id) WHERE is_root;
-- root channel can never be private
ALTER TABLE channels ADD CONSTRAINT chk_root_not_private
    CHECK (NOT (is_root AND is_private));

-- Channel-level ACL. Rows exist ONLY for private channels; public channels
-- have no rows here and fall through to project membership. This keeps the
-- common path free of extra joins while giving the authorizer a hook point.
CREATE TABLE channel_members (
    channel_id BIGINT NOT NULL REFERENCES channels(id),
    user_id    BIGINT NOT NULL REFERENCES users(id),
    role       TEXT NOT NULL DEFAULT 'member',   -- member | owner
    added_by   BIGINT REFERENCES users(id),
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX idx_channel_members_user ON channel_members (user_id);

CREATE TABLE messages (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel_id     BIGINT NOT NULL REFERENCES channels(id),
    thread_root_id BIGINT REFERENCES messages(id),  -- NULL = top-level
    author_id      BIGINT NOT NULL REFERENCES users(id),
    client_msg_id  UUID NOT NULL,                   -- idempotency / optimistic UI
    body           TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    edited_at      TIMESTAMPTZ,
    deleted_at     TIMESTAMPTZ,                     -- soft delete
    reply_count    INT NOT NULL DEFAULT 0,          -- maintained on roots only
    last_reply_at  TIMESTAMPTZ,

    UNIQUE (channel_id, client_msg_id)
);

CREATE INDEX idx_messages_channel_timeline
    ON messages (channel_id, id DESC) WHERE thread_root_id IS NULL;
CREATE INDEX idx_messages_thread
    ON messages (thread_root_id, id) WHERE thread_root_id IS NOT NULL;

-- per-user, per-channel unread cursor (project membership already exists elsewhere)
CREATE TABLE channel_read_cursors (
    channel_id           BIGINT NOT NULL REFERENCES channels(id),
    user_id              BIGINT NOT NULL REFERENCES users(id),
    last_read_message_id BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (channel_id, user_id)
);

CREATE TABLE message_reactions (
    message_id BIGINT NOT NULL REFERENCES messages(id),
    user_id    BIGINT NOT NULL REFERENCES users(id),
    emoji      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id, emoji)
);
```

**Root channel creation** happens in the same transaction as project creation
(or via a backfill migration for existing projects):

```sql
INSERT INTO channels (project_id, name, is_root) VALUES ($1, 'general', TRUE);
```

**One-level threading** enforced at insert, same trick as v1 (reply's root must
be top-level and in the same channel):

```sql
INSERT INTO messages (channel_id, thread_root_id, author_id, client_msg_id, body)
SELECT $1, m.id, $3, $4, $5
FROM messages m
WHERE m.id = $2 AND m.channel_id = $1 AND m.thread_root_id IS NULL
RETURNING *;   -- 0 rows => invalid thread root => 400
-- then in the same tx:
UPDATE messages SET reply_count = reply_count + 1, last_reply_at = now() WHERE id = $2;
```

---

## 3. Centrifuge channel namespace & event contract

Names encode the hierarchy so the subscribe handler can authorize with one parse:

| Centrifuge channel              | Purpose                                                      | History |
|---------------------------------|--------------------------------------------------------------|---------|
| `proj:{pid}`                    | Project-level chat meta: channel created/renamed/archived, member role changes. The chat tab's **channel list** stays live via this. | small (50 / 10 min) |
| `proj:{pid}:chan:{cid}`         | Durable channel events: messages, edits, deletes, reactions  | 300 / 10 min |
| `proj:{pid}:chan:{cid}:eph`     | Typing indicators                                            | none    |
| `user:{uid}`                    | Cross-project personal: mentions, unread sync across tabs/devices, "you were added to project X" | small |

### Event envelope

```json
{ "type": "message.created", "v": 1, "data": { ... } }
```

On `proj:{pid}:chan:{cid}`:

| Type               | data                                                        |
|--------------------|-------------------------------------------------------------|
| `message.created`  | full message row (`thread_root_id` set ⇒ it's a thread reply) |
| `message.updated`  | `{ id, body, edited_at }`                                   |
| `message.deleted`  | `{ id, thread_root_id }`                                    |
| `reaction.added` / `reaction.removed` | `{ message_id, user_id, emoji }`         |

On `proj:{pid}`:

| Type               | data                                        |
|--------------------|---------------------------------------------|
| `channel.created`  | full channel row                            |
| `channel.updated`  | `{ id, name, topic }`                       |
| `channel.archived` | `{ id }`                                    |
| `channel.activity` | `{ channel_id, last_message_id }` — lets the sidebar bold channels with unseen activity without subscribing to every channel |

**Privacy-aware routing:** events about a *private* channel (`channel.created`,
`channel.updated`, `channel.activity`, membership changes) are **not** published
on `proj:{pid}` — that would leak the channel's existence to non-members.
They go to each member's `user:{uid}` channel instead (`channel.member_added`
/ `channel.member_removed` also arrive there, which is how a newly added user's
sidebar learns to show the channel, and a removed user's sidebar drops it).
Message events are unaffected: only members can subscribe to
`proj:{pid}:chan:{cid}` in the first place.

On `user:{uid}`: `mention` `{ project_id, channel_id, message_id }`,
`read.updated` `{ channel_id, last_read_message_id }`, `project.added` /
`project.removed`, and for private channels: `channel.created` /
`channel.member_added` / `channel.member_removed` / `channel.activity`.

Client rendering rule is unchanged from v1: `message.created` with null
`thread_root_id` appends to the channel timeline; otherwise it bumps the root's
thread badge and appends to the thread drawer if open.

---

## 4. Go server integration

### Subscribe authorization — one parser, your project authz

```go
var chanRe = regexp.MustCompile(`^proj:(\d+)(?::chan:(\d+))?(?::eph)?$`)

client.OnSubscribe(func(e centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
    userID := mustUserID(client)

    // personal channel: only your own
    if e.Channel == "user:"+client.UserID() {
        cb(centrifuge.SubscribeReply{Options: centrifuge.SubscribeOptions{
            EnableRecovery: true, HistorySize: 50, HistoryTTL: 10 * time.Minute,
        }}, nil)
        return
    }

    m := chanRe.FindStringSubmatch(e.Channel)
    if m == nil {
        cb(centrifuge.SubscribeReply{}, centrifuge.ErrorPermissionDenied)
        return
    }
    projectID, _ := strconv.ParseInt(m[1], 10, 64)

    if !s.authz.Can(ctx, userID, projectID, ChatCapRead) {   // ← your project permissions
        cb(centrifuge.SubscribeReply{}, centrifuge.ErrorPermissionDenied)
        return
    }
    if m[2] != "" { // channel-level: project ownership + private ACL in one query
        channelID, _ := strconv.ParseInt(m[2], 10, 64)
        if !s.store.CanAccessChannel(ctx, userID, channelID, projectID) {
            cb(centrifuge.SubscribeReply{}, centrifuge.ErrorPermissionDenied)
            return
        }
    }

    eph := strings.HasSuffix(e.Channel, ":eph")
    cb(centrifuge.SubscribeReply{Options: centrifuge.SubscribeOptions{
        EnableRecovery: !eph,
        HistorySize:    ifelse(eph, 0, 300),
        HistoryTTL:     ifelse(eph, 0, 10*time.Minute),
    }}, nil)
})
```

Client-side publishes remain restricted to `:eph` channels (typing only), and
only for users with `ChatCapPost`.

`CanAccessChannel` is a single query implementing the two-layer rule:

```sql
SELECT EXISTS (
    SELECT 1 FROM channels c
    WHERE c.id = $2 AND c.project_id = $3 AND c.archived_at IS NULL
      AND (NOT c.is_private
           OR EXISTS (SELECT 1 FROM channel_members cm
                      WHERE cm.channel_id = c.id AND cm.user_id = $1))
);
-- caller has already verified project-level ChatCapRead (and, if desired,
-- short-circuited for project admins before running this)
```

### Disconnect on project removal

When a user is removed from a project (or downgraded), after the DB write:

```go
// tear down live subscriptions for that user across all node instances
s.node.Unsubscribe(userIDStr, fmt.Sprintf("proj:%d", projectID))
for _, cid := range s.store.ChannelIDs(ctx, projectID) {
    s.node.Unsubscribe(userIDStr, fmt.Sprintf("proj:%d:chan:%d", projectID, cid))
}
s.publishUser(userID, Event{Type: "project.removed", Data: map[string]any{"project_id": projectID}})
```

### REST surface (nested under your existing project routes)

```
GET    /api/projects/{pid}/channels                       → channels visible to caller
                                                            (public + private-where-member) + unread counts
POST   /api/projects/{pid}/channels  {name, topic, is_private?, member_ids?}
                                                          → ChatCapCreateChannel; creator becomes owner
PATCH  /api/projects/{pid}/channels/{cid}  {name, topic}  → ChatCapManageChannel or channel owner
DELETE /api/projects/{pid}/channels/{cid}   (archive; root channel: 400)

POST   /api/projects/{pid}/channels/{cid}/members  {user_id}   → owner/admin; target must be a project member
DELETE /api/projects/{pid}/channels/{cid}/members/{uid}        → owner/admin, or self (leave)

GET    /api/projects/{pid}/channels/{cid}/messages?before={id}&limit=50
POST   /api/projects/{pid}/channels/{cid}/messages  {body, client_msg_id, thread_root_id?}
GET    /api/projects/{pid}/channels/{cid}/threads/{rootID}?after={id}
PATCH  /api/projects/{pid}/channels/{cid}/read      {last_read_message_id}

GET    /api/me/chat/unreads   → [{project_id, unread_channels, mention_count}]  (badges on project/tab nav)
```

Every handler front-doors through the same `authz.Can(...)` used by Dashboard
and Wiki — chat is just another content region behind project permissions.

### Write path (unchanged in spirit from v1)

```go
func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
    pid, cid := pathInt64(r, "pid"), pathInt64(r, "cid")
    userID := auth.MustUser(r)
    in := decode[CreateMessageIn](r)

    if !s.authz.Can(r.Context(), userID, pid, ChatCapPost) ||
       !s.store.ChannelInProject(r.Context(), cid, pid) {
        httpError(w, 403); return
    }

    msg, err := s.store.InsertMessage(r.Context(), cid, userID, in)
    if errors.Is(err, store.ErrDuplicateClientMsgID) {
        writeJSON(w, 200, s.store.GetByClientMsgID(r.Context(), cid, in.ClientMsgID)); return
    }

    s.publish(fmt.Sprintf("proj:%d:chan:%d", pid, cid), Event{Type: "message.created", V: 1, Data: msg})
    s.publish(fmt.Sprintf("proj:%d", pid), Event{Type: "channel.activity",
        Data: map[string]any{"channel_id": cid, "last_message_id": msg.ID}})
    go s.fanOutMentions(pid, msg)

    writeJSON(w, 201, msg)
}
```

---

## 5. TypeScript client — chat as a tab

One app-wide Centrifuge connection (created at login, not per tab), so mention
badges on the project nav work even while the user is on Dashboard or Wiki.
Subscriptions are scoped to what's visible:

| UI state                     | Active subscriptions                                  |
|------------------------------|-------------------------------------------------------|
| Logged in, anywhere          | `user:{me}`                                           |
| Inside project P (any tab)   | + `proj:{P}` (keeps channel list & activity badges warm) |
| Chat tab, channel C open     | + `proj:{P}:chan:{C}` and `...:eph`                   |

```ts
// app-level singleton
export const cf = new Centrifuge(WS_URL, { getToken: () => api.getRealtimeToken() });
cf.connect();

// entering a project (any tab)
export function useProjectRealtime(pid: number) {
  useEffect(() => {
    const sub = cf.newSubscription(`proj:${pid}`);
    sub.on("publication", ({ data }) => projectStore.applyMeta(pid, data as ChatEvent));
    sub.subscribe();
    return () => { sub.unsubscribe(); cf.removeSubscription(sub); };
  }, [pid]);
}

// chat tab, active channel
export function useChannel(pid: number, cid: number) {
  useEffect(() => {
    const sub = cf.newSubscription(`proj:${pid}:chan:${cid}`);
    sub.on("publication", ({ data }) => chatStore.apply(cid, data as ChatEvent));
    sub.on("subscribed", (ctx) => {
      if (ctx.wasRecovering && !ctx.recovered) chatStore.refetch(pid, cid); // gap too big
    });
    sub.subscribe();
    return () => { sub.unsubscribe(); cf.removeSubscription(sub); };
  }, [pid, cid]);
}
```

Sends stay on REST with optimistic UI keyed on `client_msg_id` (dedupe when the
echoed WS event arrives), exactly as v1:

```ts
async function send(pid: number, cid: number, body: string, threadRootId?: number) {
  const clientMsgId = crypto.randomUUID();
  chatStore.appendPending(cid, { clientMsgId, body, threadRootId });
  const msg = await api.post(`/projects/${pid}/channels/${cid}/messages`, {
    body, client_msg_id: clientMsgId, thread_root_id: threadRootId,
  });
  chatStore.confirm(cid, clientMsgId, msg);
}
```

Chat tab layout: channel sidebar (from `GET /channels`, kept live by `proj:{pid}`
events) → message timeline (top-level only, thread badges from `reply_count`) →
thread drawer (opens on badge click, fetches `/threads/{rootID}`, live via the
same channel subscription filtered on `thread_root_id`).

---

## 6. Scaling levers (unchanged from v1, restated for the new namespace)

- **Multiple Go instances:** swap in `centrifuge.RedisBroker`; the
  `proj:*` naming needs no changes.
- **Hot channels:** split thread replies onto `proj:{pid}:chan:{cid}:thread:{root}`
  subscribed only while a drawer is open; the channel then carries counters only.
- **Presence:** enable on `proj:{pid}` for an online-members list per project.

## Build order

1. `channels` + `messages` schema, root-channel creation hook, REST endpoints
   gated by your existing project authz (test with polling)
2. Embed centrifuge.Node; namespace parser + subscribe authorization; publish on write
3. App-level connection + `proj:{pid}` subscription (live channel list & activity badges)
4. Channel view: timeline, optimistic sends, reconnect recovery
5. Threads UI (badges + drawer), then read cursors/unreads on tab & project nav,
   reactions, typing, mentions
6. Removal/downgrade handling: `node.Unsubscribe` + `project.removed` event
7. Private channels: ship the schema (`is_private`, `channel_members`) and the
   `CanAccessChannel` check from day one, but expose the create-private UI and
   member management last — everything upstream already routes through the
   two-layer authorizer, so turning it on is additive

