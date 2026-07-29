import type { ChatChannel } from '../../chat/types'

// Channel refs are the fls:doc / fls:task / fls:whiteboard sibling (see
// doccard/docref.ts): the compact, text-safe way to address one of a
// project's chat channels.
//
// Unlike its siblings this scheme has NO inline card yet — it exists because
// pins need a stable address for a channel, and every other pinnable local
// record already had one. Making it a card as well means the four extra edits
// CLAUDE.md lists (RefCard, reftokens, chat/MessageList's ChatBody union,
// wiki/Markdown's a: override); deliberately not done here, so a channel token
// pasted into a message today stays plain text rather than half-unfurling.
//
// The channel's display name is a hint refreshed at render time, exactly as a
// task's title is: a renamed channel never leaves a stale label behind.

export interface ChannelRef {
  hubId: string // GraphQL hub id
  projectId: string // project urn (the chat store's key)
  projectName: string // display name at capture time
  channelId: string // per-project channel id
  name: string // display name at capture time (hydration refreshes it)
}

export const CHANNEL_REF_PREFIX = 'fls:channel?'

export function encodeChannelRef(ref: ChannelRef): string {
  const sp = new URLSearchParams()
  sp.set('hubId', ref.hubId)
  sp.set('projectId', ref.projectId)
  sp.set('projectName', ref.projectName)
  sp.set('channelId', ref.channelId)
  sp.set('name', ref.name)
  return CHANNEL_REF_PREFIX + sp.toString()
}

export function parseChannelRef(url: string): ChannelRef | null {
  if (!url.startsWith(CHANNEL_REF_PREFIX)) return null
  const sp = new URLSearchParams(url.slice(CHANNEL_REF_PREFIX.length))
  const projectId = sp.get('projectId') ?? ''
  const channelId = sp.get('channelId') ?? ''
  if (!projectId || !channelId) return null
  return {
    hubId: sp.get('hubId') ?? '',
    projectId,
    projectName: sp.get('projectName') ?? '',
    channelId,
    name: sp.get('name') || 'channel',
  }
}

export function channelRefFrom(
  ctx: { hubId: string; projectId: string; projectName: string },
  channel: Pick<ChatChannel, 'id' | 'name'>,
): ChannelRef {
  return {
    hubId: ctx.hubId,
    projectId: ctx.projectId,
    projectName: ctx.projectName,
    channelId: channel.id,
    name: channel.name,
  }
}
