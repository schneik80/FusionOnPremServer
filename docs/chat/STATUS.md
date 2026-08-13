# Project chat — status

Tracks `PLAN.md` (the adapted implementation plan) against what has shipped.
Spec: `centrifuge-chat-design.md`, adapted per the deviations recorded in
PLAN.md (file store instead of Postgres, stdlib SSE instead of centrifuge,
flat query-param REST, APS-backed roles).

## Shipped

| Phase | What | Where |
|---|---|---|
| 1 | Store, invariants, authz, REST, polling Chat tab | `chat/{types,store,jsonl,authz,ratelimit}.go`, `server/handlers_chat.go`, `web/src/chat/` |
| 2 | SSE hub, `/api/chat/events`, publish-on-write, reset/replay recovery | `chat/hub.go`, `server/handlers_chat_events.go` |
| 3 | Live frontend over SSE, optimistic sends, polling as fallback | `web/src/chat/useChatEvents.ts`, `cache.ts` |
| 4 | Threads UI (landed early, phase 1–3), **read cursors + unreads** (`cursors.json`, `PATCH /api/chat/read`, `GET /api/chat/unreads`, user-only `read.updated` sync across own tabs), **typing indicators** (`POST /api/chat/typing`, ephemeral id-less SSE frames), **reaction picker** with server-side emoji allowlist, unread badges (sidebar counts + Chat tab total) | `chat/cursors.go`, `chat/emoji.go`, `web/src/chat/{typing.ts,TypingIndicator.tsx}` |
| 5 | Private-channel UI (create with member picker, member management, leave), channel menu (rename/topic/archive) for moderators + creators, `GET /api/chat/members` roster, security pass | `web/src/chat/ChannelMenu.tsx`, `docs/security/CHAT-SECURITY.md`, chat fuzz targets in `server/fuzz_security_test.go` |

Event vocabulary on the wire: `message.created/updated/deleted`,
`reaction.added/removed`, `channel.created/updated/archived`,
`channel.member_added/member_removed`, `channel.activity`, `read.updated`
(user-only), `typing` (ephemeral, no SSE id), plus the named `reset` frame.

## Manual checkpoint matrix (two browsers, two Autodesk accounts)

- [ ] Phase 4: unread badge counts on sidebar + Chat tab; mark-read in one
      tab clears the badge in the same user's second tab; "N is typing…"
      appears within a ping and clears when the message lands or ~5 s pass;
      first reaction placeable via the picker; off-palette REST reaction 400s.
- [ ] Phase 5: private channel invisible to a third member (404 on direct
      access); visible to a project Administrator; adding a member makes the
      channel appear in their sidebar live; removing (or leaving) drops it
      live; rename/topic/archive gated to moderators + creator; root channel
      refuses rename/archive.
- [ ] Group-only access: a user whose project role comes solely from a group
      (not a direct member) can open Chat and post — no "you do not have
      access" error — but sees no moderation affordances.

(Phases 1–3 checkpoints were verified when those phases landed; see git
history around the `feat: project chat phase N` commits.)

## Render failures are contained per message (2026-08-12)

The chat pane had no error boundary, so an uncaught render error anywhere in
the message list unmounted the whole subtree — React tears down the entire tree
on a render throw. The symptom was a **blank panel with nothing said**, and it
split by data rather than by code: a project with no messages rendered nothing
that could throw and looked fine, while a project with history rendered every
avatar and every `fls:` card and did not. Reported first from the Fusion
palette, where a blank frame is the entire diagnostic.

`components/ErrorBoundary` gained a `compact` form (an inline row rather than a
centred panel) and is now wired in twice, deliberately at two depths:

- **Per message** (`MessageList`), keyed on `seq` — one bad message is a
  contained row and the conversation around it still reads.
- **Per card token** (`ChatBody`) — a card that cannot render costs only
  itself, so the sentence it sits in, and the other cards in the same message,
  survive. This is the layer most likely to throw on old history: a token is a
  durable reference to something that may since have been deleted, renamed or
  moved.

Both surface `error.message` on screen, which matters in the palette where
there is no console to read `componentDidCatch` output.

This contains the blast radius; it does not fix whatever throws. Note the
`fls:` renderers in `wiki/Markdown.tsx` have the same exposure and are not yet
wrapped.

## Threads become a page when the pane is too narrow (2026-08-13)

`ThreadPanel` was a fixed `width: 360, flexShrink: 0`. Fine in the desktop app;
unusable in the Fusion palette, which opens at **420 px** — opening a thread
there left the message list **60 px**. The channel rail already had a
narrow-host accommodation (`collapsibleChannels`), but the thread arrived at
full width regardless.

Below **640 px** of shared space there is no split worth having (320 px of
messages beside 320 px of thread — the width at which an attached document card
still reads, which is what 360 was originally chosen for). Under that, the
thread becomes a **page**: full width, `← Thread · #channel`, back returns to
the message list.

- `chat/chatLayout.ts` — the four numbers and the decision, pure and tested.
  `splitWidth` subtracts the open rail, because measuring the outer box alone
  would call a 900 px pane wide while a 260 px rail leaves only 640.
- `components/useElementWidth.ts` — shared `ResizeObserver` hook, callback-ref
  shaped like `useInView`. Four files still inline this observer
  (`ActivityHeatmap`, `HistoryTimeline`, `RelationGraph`, `PdfViewer`); the hook
  is written to suit them but they were not converted.
- `chat/ThreadPanel.tsx` — a `variant` prop. `panel` is now
  `flex: 0 1 360px` with a 320 floor, so above the breakpoint the thread gives
  back 40 px under pressure instead of the list absorbing the whole squeeze.
  `page` is full width with a back arrow. Body and composer are untouched.
- `chat/ChatApp.tsx` — measures its own box, hides (never unmounts) the message
  column while a page is up so scroll position survives the round trip, and
  hides the rail *toggle* so `back` has one meaning.

**Measured, not host-declared.** A `compactThreads` prop from `EmbedApp` would
have been one line, but it encodes *who is hosting* when the question is *how
wide am I*: the palette is resizable and a desktop project panel narrows with
the window. **`EmbedApp` and the add-in needed no change at all**, and the
desktop app gets the same behaviour when squeezed.

The rail itself stays visible in page mode. Hiding it would make `isCompact`
lie — it subtracts `APP_RAIL_WIDTH` from the shared space, so that has to
describe what is actually on screen, or the two feed back into each other.

Not done: no slide-in transition (MUI `Slide` needs a ref-forwarding child, and
a wrapper would have to replicate the flex sizing — cosmetic, easy to add), and
no thread permalink; `threadRoot` is still local state.

## Open questions carried forward

- **Resolved — group-only members** (PLAN.md open question 3). Users whose
  project access comes through a group aren't in `folderLevelProjectMembers`,
  so the phase-3 authorizer 403'd them out of chat entirely ("you do not have
  access"). Fixed in `chat/authz.go`: a caller whose *own token* can read the
  project roster has project access, so if they aren't individually listed
  they're treated as a **group-derived contributor** (read/post/react/edit-own/
  create-channel, never moderate). Third-party checks (private-channel
  invitees) stay strict via `IsActiveMember`. See `docs/security/CHAT-SECURITY.md`.

Unchanged from PLAN.md: OIDC `sub` vs GraphQL user id (email fallback in
place), FolderRoleEnum casing (case-insensitive match in place), HTTP/1.1
6-connection limit (use `-tls` HTTP/2 for many tabs), Vite-direct SSE
buffering (use the proxy), Windows AV on JSONL opens (retry in place), no
frontend test harness (checkpoints).
