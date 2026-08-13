import { describe, expect, it } from 'vitest'
import { APP_RAIL_WIDTH } from '../components/Column'
import { isCompact, MESSAGES_MIN, splitWidth, SPLIT_MIN, THREAD_MIN } from './chatLayout'

describe('splitWidth', () => {
  it('is the whole pane when the rail is closed', () => {
    expect(splitWidth(900, false)).toBe(900)
  })

  it('subtracts the rail when it is open', () => {
    expect(splitWidth(900, true)).toBe(900 - APP_RAIL_WIDTH)
  })

  it('never goes negative on a pane narrower than the rail', () => {
    expect(splitWidth(100, true)).toBe(0)
  })
})

describe('isCompact', () => {
  it('flips exactly at SPLIT_MIN', () => {
    expect(isCompact(SPLIT_MIN, false)).toBe(false)
    expect(isCompact(SPLIT_MIN - 1, false)).toBe(true)
  })

  it('calls the Fusion palette compact at its default width', () => {
    // The palette opens at 420px with the rail collapsed.
    expect(isCompact(420, false)).toBe(true)
  })

  it('calls a desktop pane roomy', () => {
    expect(isCompact(1200, false)).toBe(false)
  })

  it('an open rail can push a wide pane into compact', () => {
    // 900 - 260 = 640... which is exactly SPLIT_MIN, so still a split;
    // one pixel less is not.
    expect(isCompact(SPLIT_MIN + APP_RAIL_WIDTH, true)).toBe(false)
    expect(isCompact(SPLIT_MIN + APP_RAIL_WIDTH - 1, true)).toBe(true)
  })

  it('treats an unmeasured pane as roomy rather than flashing a page layout', () => {
    expect(isCompact(0, false)).toBe(false)
    expect(isCompact(-1, false)).toBe(false)
  })
})

describe('the floors are coherent', () => {
  it('SPLIT_MIN is exactly the two minimums side by side', () => {
    expect(SPLIT_MIN).toBe(MESSAGES_MIN + THREAD_MIN)
  })

  it('a pane at SPLIT_MIN can seat both at their floor', () => {
    expect(splitWidth(SPLIT_MIN, false)).toBeGreaterThanOrEqual(MESSAGES_MIN + THREAD_MIN)
  })
})
