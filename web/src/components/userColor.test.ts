import { describe, expect, it } from 'vitest'
import { hashKey, userColor, userHue } from './userColor'

describe('userColor', () => {
  it('is deterministic — the same key always yields the same colour', () => {
    expect(userColor('user-ada')).toBe(userColor('user-ada'))
    expect(hashKey('user-ada')).toBe(hashKey('user-ada'))
  })

  it('keeps the hue inside the colour wheel', () => {
    const keys = ['', 'a', 'user-ada', 'Grace Hopper', 'x'.repeat(200), '🙂 Ada', '同僚']
    for (const k of keys) {
      const h = userHue(k)
      expect(h, k).toBeGreaterThanOrEqual(0)
      expect(h, k).toBeLessThan(360)
      expect(Number.isInteger(h), k).toBe(true)
    }
  })

  it('separates distinct users', () => {
    expect(userHue('user-ada')).not.toBe(userHue('user-grace'))
  })

  it('emits a well-formed hsl() string', () => {
    expect(userColor('user-ada')).toMatch(/^hsl\(\d{1,3}, \d{1,3}%, \d{1,3}%\)$/)
  })

  it('handles the empty key without throwing', () => {
    expect(() => userColor('')).not.toThrow()
    expect(userHue('')).toBe(hashKey('') % 360)
  })
})
