import { describe, expect, it } from 'vitest'
import { channelRefFrom, encodeChannelRef, parseChannelRef } from './chanref'
import { splitRefTokens } from '../reftokens'

// fls:channel is the addressing scheme a pinned chat channel is stored under.
// Unlike its siblings it is deliberately NOT a card yet, so the two things
// worth pinning down are that it round-trips exactly (a pin that can't parse
// its own token is an unopenable bookmark) and that it stays invisible to the
// chat body splitter — a half-unfurled token in a message would be worse than
// plain text.

const ctx = { hubId: 'h1', projectId: 'p1', projectName: 'Alpha' }

describe('channel refs', () => {
  it('round-trips a channel', () => {
    const token = encodeChannelRef(
      channelRefFrom(ctx, { id: 'c2', name: 'manufacturing' }),
    )
    const back = parseChannelRef(token)
    expect(back).toEqual({
      hubId: 'h1',
      projectId: 'p1',
      projectName: 'Alpha',
      channelId: 'c2',
      name: 'manufacturing',
    })
  })

  it('round-trips names and projects that need percent-encoding', () => {
    const token = encodeChannelRef({
      hubId: 'h1',
      projectId: 'urn:adsk.wipprod:dm.lineage:AbC-123',
      projectName: 'Alpha & Co',
      channelId: 'c9',
      name: 'Räder / Planung #2',
    })
    const back = parseChannelRef(token)
    expect(back?.name).toBe('Räder / Planung #2')
    expect(back?.projectName).toBe('Alpha & Co')
    expect(back?.projectId).toBe('urn:adsk.wipprod:dm.lineage:AbC-123')
  })

  it('rejects another scheme and a token missing its ids', () => {
    expect(parseChannelRef('fls:task?projectId=p1&taskId=t1')).toBeNull()
    expect(parseChannelRef('fls:channel?projectId=p1')).toBeNull()
    expect(parseChannelRef('fls:channel?channelId=c1')).toBeNull()
  })

  it('is not unfurled by the chat body splitter', () => {
    const token = encodeChannelRef(channelRefFrom(ctx, { id: 'c2', name: 'general' }))
    expect(splitRefTokens(token)).toEqual([{ text: token }])
  })
})
