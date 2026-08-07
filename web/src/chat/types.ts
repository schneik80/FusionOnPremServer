// TypeScript mirrors of the chat DTOs in server/dto_chat.go. Keep field
// names in sync with the json tags there.

export interface ChatChannel {
  id: string
  name: string
  topic: string
  isRoot: boolean
  isPrivate: boolean
  createdBy: string
  createdAt: string
  archivedAt?: string
  memberIds?: string[]
}

export interface ChatReaction {
  userId: string
  emoji: string
  at: string
}

export interface ChatMessage {
  seq: number
  threadRoot?: number
  authorId: string
  authorName: string
  clientMsgId: string
  body: string
  createdAt: string
  editedAt?: string
  deleted: boolean
  replyCount: number
  lastReplyAt?: string
  reactions: ChatReaction[]
  // pending is CLIENT-ONLY: true on the optimistic copy shown between
  // hitting send and the server's echo (REST response or SSE event,
  // whichever lands first — both reconcile on clientMsgId). Pending
  // messages carry a negative placeholder seq.
  pending?: boolean
}

// ChatCaps is what the signed-in user may do in this project's chat,
// derived server-side from their APS project role.
export interface ChatCaps {
  post: boolean
  createChannel: boolean
  moderate: boolean
}

// REACTION_EMOJI mirrors chat.AllowedEmoji in chat/emoji.go — the server
// refuses reaction adds outside this palette, so the picker offers exactly
// these.
export const REACTION_EMOJI = ['👍', '👎', '❤️', '😄', '🎉', '🚀', '👀', '✅', '❓', '😕']

// ChatUnread mirrors ChatUnreadDTO: one channel's unread summary for the
// signed-in user. Also the payload of the user-only read.updated event.
export interface ChatUnread {
  channelId: string
  lastReadSeq: number
  unreadCount: number
  latestSeq: number
}

export interface ChatUnreadList {
  unreads: ChatUnread[]
}

// ChatMember is one ACTIVE project member (GET /api/chat/members) — the
// candidate pool for private-channel ACLs. Mirrors the server's MemberDTO.
export interface ChatMember {
  userId: string
  name: string
  email?: string
  role: string
}

export interface ChatChannelList {
  channels: ChatChannel[]
  capabilities: ChatCaps
}

export interface ChatMessageList {
  messages: ChatMessage[]
  // latestSeq is the channel's newest seq — the polling cursor once SSE
  // recovery (phase 2/3) starts using afterSeq deltas.
  latestSeq: number
}

// ---- SSE event payloads (mirror server/dto_chat.go's ChatXxxEventDTO) ----

// ChatEvent is the {type, v, data} envelope every frame carries
// (design doc §3).
export interface ChatEvent {
  type: string
  v: number
  data: unknown
}

export interface ChatMessageEvent {
  channelId: string
  message: ChatMessage
}

export interface ChatChannelEvent {
  channel: ChatChannel
}

export interface ChatMemberEvent {
  channelId: string
  userId: string
  channel: ChatChannel
}

export interface ChatActivityEvent {
  channelId: string
  lastMessageSeq: number
}

// ChatTypingEvent is the ephemeral typing ping (no SSE id; never replayed).
export interface ChatTypingEvent {
  channelId: string
  userId: string
  name: string
}

// ChatImage mirrors server ChatImageDTO (POST /api/chat/image): the uploaded
// attachment's lineage urn + stored name, referenced by an fls:img token.
export interface ChatImage {
  itemId: string
  name: string
}
