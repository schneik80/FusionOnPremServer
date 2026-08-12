import { describe, expect, it } from 'vitest'
import { avatarInitials, avatarKey } from './UserAvatar'
import { userHue } from './userColor'

describe('avatarKey', () => {
  it('prefers the id, falls back to the name, then to empty', () => {
    expect(avatarKey('u1', 'Ada Lovelace')).toBe('u1')
    expect(avatarKey(undefined, 'Ada Lovelace')).toBe('Ada Lovelace')
    expect(avatarKey(null, null)).toBe('')
    expect(avatarKey('', 'Ada')).toBe('Ada')
  })

  it('gives one person the same colour across every surface', () => {
    // The whole point of consolidating: chat, tasks and history all hash the
    // same key, so the same author is the same hue everywhere.
    expect(userHue(avatarKey('u1', 'Ada Lovelace'))).toBe(userHue(avatarKey('u1', 'A. Lovelace')))
  })
})

describe('avatarInitials', () => {
  it('takes the first grapheme of up to two words', () => {
    expect(avatarInitials('Ada Lovelace')).toBe('AL')
    expect(avatarInitials('Ada')).toBe('A')
    expect(avatarInitials('Ada Byron King Lovelace')).toBe('AB')
  })

  it('splits an email so a bare address is not one letter', () => {
    expect(avatarInitials('ada@example.com')).toBe('AE')
  })

  it('is grapheme-safe, not code-unit-safe', () => {
    // '🙂'[0] is half a surrogate pair; firstGrapheme keeps it whole.
    expect(avatarInitials('🙂 Ada')).toBe('🙂A')
    expect(avatarInitials('José García')).toBe('JG')
  })

  it('falls back to a question mark for nothing usable', () => {
    for (const s of ['', '   ', undefined, null]) {
      expect(avatarInitials(s), String(s)).toBe('?')
    }
  })
})
