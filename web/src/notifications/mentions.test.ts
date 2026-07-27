import { describe, expect, it } from 'vitest'
import { encodeMention, parseMention, splitMentions } from './mentions'

describe('mention tokens', () => {
  it('round-trips through encode/parse, url-encoding the name', () => {
    const token = encodeMention({ userId: 'sub-123', name: 'Ada Lovelace' })
    expect(token).toContain('fls:user?')
    expect(token).not.toContain(' ') // the space is percent-encoded
    expect(parseMention(token)).toEqual({ userId: 'sub-123', name: 'Ada Lovelace' })
  })

  it('rejects a token without an id', () => {
    expect(parseMention('fls:user?name=NoId')).toBeNull()
    expect(parseMention('fls:doc?id=x')).toBeNull()
  })

  it('splits text around mention tokens', () => {
    const token = encodeMention({ userId: 'u1', name: 'Bob' })
    const parts = splitMentions(`hi ${token} there`)
    expect(parts).toEqual([
      { text: 'hi ' },
      { mention: { userId: 'u1', name: 'Bob' } },
      { text: ' there' },
    ])
  })

  it('returns a single text part when there are no mentions', () => {
    expect(splitMentions('plain text')).toEqual([{ text: 'plain text' }])
  })
})
