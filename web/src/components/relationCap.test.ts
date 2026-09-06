import { describe, expect, it } from 'vitest'
import { thumbSlots, thumbsHidden } from './relationCap'

describe('thumbSlots', () => {
  it('grants the first cap relations in order, all once loadAll', () => {
    const has = thumbSlots(30, 24, false)
    expect(has(0)).toBe(true)
    expect(has(23)).toBe(true)
    expect(has(24)).toBe(false)
    expect(thumbSlots(30, 24, true)(29)).toBe(true)
    expect(thumbSlots(10, 24, false)(9)).toBe(true)
  })
  it('reports how many are hidden', () => {
    expect(thumbsHidden(30, 24, false)).toBe(6)
    expect(thumbsHidden(30, 24, true)).toBe(0)
    expect(thumbsHidden(24, 24, false)).toBe(0)
  })
})
