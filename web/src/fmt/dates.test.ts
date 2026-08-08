import { describe, expect, it } from 'vitest'
import { addDays, addMonths, daysBetween, fmtDay, isDay, mondayOf, monthGrid, parseDay, sameDay } from './dates'

describe('parseDay / fmtDay', () => {
  it('round-trips a day string', () => {
    expect(fmtDay(parseDay('2026-08-08'))).toBe('2026-08-08')
  })

  it('parses to LOCAL midnight, not UTC', () => {
    // The bug this guards: new Date('2026-08-08') is UTC midnight, which is
    // 7 Aug in every negative-offset zone.
    const d = parseDay('2026-08-08')
    expect(d.getDate()).toBe(8)
    expect(d.getHours()).toBe(0)
  })

  it('pads single-digit months and days', () => {
    expect(fmtDay(new Date(2026, 0, 5))).toBe('2026-01-05')
  })
})

describe('addDays / addMonths', () => {
  it('crosses a month boundary', () => {
    expect(fmtDay(addDays(parseDay('2026-01-31'), 1))).toBe('2026-02-01')
  })

  it('clamps the day when the target month is shorter', () => {
    expect(fmtDay(addMonths(parseDay('2026-01-31'), 1))).toBe('2026-02-28')
    expect(fmtDay(addMonths(parseDay('2024-01-31'), 1))).toBe('2024-02-29') // leap
  })

  it('crosses a year boundary in both directions', () => {
    expect(fmtDay(addMonths(parseDay('2026-01-15'), -1))).toBe('2025-12-15')
    expect(fmtDay(addMonths(parseDay('2026-12-15'), 1))).toBe('2027-01-15')
  })
})

describe('mondayOf', () => {
  it('returns the same day for a Monday', () => {
    expect(fmtDay(mondayOf(parseDay('2026-08-03')))).toBe('2026-08-03')
  })

  it('walks Sunday back six days, not forward one', () => {
    expect(fmtDay(mondayOf(parseDay('2026-08-09')))).toBe('2026-08-03')
  })
})

describe('daysBetween', () => {
  it('counts whole days', () => {
    expect(daysBetween(parseDay('2026-08-01'), parseDay('2026-08-08'))).toBe(7)
    expect(daysBetween(parseDay('2026-08-08'), parseDay('2026-08-01'))).toBe(-7)
  })
})

describe('isDay', () => {
  it('accepts a real date', () => {
    expect(isDay('2026-08-08')).toBe(true)
  })

  it('rejects a date that does not exist', () => {
    // A range check alone would pass this — parseDay rolls it to 3 March.
    expect(isDay('2026-02-31')).toBe(false)
  })

  it('rejects malformed input', () => {
    for (const s of ['', '2026-8-8', '08/08/2026', 'today', '2026-13-01']) {
      expect(isDay(s), s).toBe(false)
    }
  })
})

describe('monthGrid', () => {
  it('is always six Monday-first weeks', () => {
    const g = monthGrid(parseDay('2026-08-08'))
    expect(g).toHaveLength(42)
    expect(g[0].getDay()).toBe(1) // Monday
    expect(fmtDay(g[0])).toBe('2026-07-27')
    expect(fmtDay(g[41])).toBe('2026-09-06')
  })

  it('contains every day of the anchor month', () => {
    const g = monthGrid(parseDay('2026-02-15'))
    for (let day = 1; day <= 28; day++) {
      expect(g.some((d) => sameDay(d, new Date(2026, 1, day))), `day ${day}`).toBe(true)
    }
  })
})
