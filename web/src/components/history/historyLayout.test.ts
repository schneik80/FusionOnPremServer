import { describe, expect, it } from 'vitest'
import type { VersionSummary } from '../../api/types'
import {
  authorKey,
  bucketByDay,
  COL_GAP,
  declutter,
  gapBetween,
  GUTTER_W,
  hourTicks,
  indexBase,
  layoutStack,
  MIN_DOT_GAP,
  PAD_R,
  plotWidth,
  threadWidth,
  TRACKS_PER_DAY_CAP,
  tracksForDay,
  xOfIndex,
  xOfMs,
} from './historyLayout'

// Versions are built with LOCAL wall-clock times, matching what the view shows:
// the day a save belongs to is the day the author saw on their clock.
function v(
  number: number,
  local: string,
  who?: { id?: string; name?: string },
): VersionSummary {
  const [date, time = '12:00'] = local.split(' ')
  const [y, m, d] = date.split('-').map(Number)
  const [hh, mm] = time.split(':').map(Number)
  return {
    number,
    createdOn: new Date(y, m - 1, d, hh, mm).toISOString(),
    createdBy: who?.name,
    createdById: who?.id,
  }
}

const ada = { id: 'user-ada', name: 'Ada Lovelace' }
const grace = { id: 'user-grace', name: 'Grace Hopper' }

describe('authorKey', () => {
  it('prefers the user id', () => {
    expect(authorKey({ number: 1, createdById: 'u1', createdBy: 'Ada' })).toBe('u1')
  })

  it('falls back to the display name, then to empty', () => {
    expect(authorKey({ number: 1, createdBy: 'Ada' })).toBe('Ada')
    expect(authorKey({ number: 1 })).toBe('')
  })

  it('keeps two people with the same display name apart', () => {
    const a = { number: 1, createdById: 'u1', createdBy: 'Chris Smith' }
    const b = { number: 2, createdById: 'u2', createdBy: 'Chris Smith' }
    expect(authorKey(a)).not.toBe(authorKey(b))
  })
})

describe('bucketByDay', () => {
  it('groups by local calendar day, newest day first', () => {
    const rows = bucketByDay([v(3, '2026-08-12 09:00'), v(2, '2026-08-11 23:30'), v(1, '2026-08-11 08:00')])
    expect(rows.map((r) => r.day)).toEqual(['2026-08-12', '2026-08-11'])
    expect(rows[0].count).toBe(1)
    expect(rows[1].count).toBe(2)
  })

  it('buckets a late-evening save on the local day, not the UTC one', () => {
    // 23:30 local is the next UTC day in any positive-offset zone; it must stay
    // on the day the author actually saw.
    const rows = bucketByDay([v(1, '2026-08-11 23:30')])
    expect(rows[0].day).toBe('2026-08-11')
  })

  it('indexes versions oldest-first across the whole history', () => {
    const rows = bucketByDay([v(3, '2026-08-12 09:00'), v(2, '2026-08-11 15:00'), v(1, '2026-08-11 08:00')])
    const byNumber = new Map(rows.flatMap((r) => r.tracks.flatMap((t) => t.dots)).map((d) => [d.v.number, d.index]))
    expect(byNumber.get(1)).toBe(0)
    expect(byNumber.get(2)).toBe(1)
    expect(byNumber.get(3)).toBe(2)
  })

  it('computes ms from the local clock', () => {
    const rows = bucketByDay([v(1, '2026-08-11 06:30')])
    expect(rows[0].tracks[0].dots[0].ms).toBe((6 * 60 + 30) * 60_000)
  })

  it('collects undated versions in one trailing bucket rather than dropping them', () => {
    const rows = bucketByDay([
      { number: 2 },
      v(1, '2026-08-11 08:00'),
      { number: 3, createdOn: 'not a date' },
    ])
    expect(rows.map((r) => r.day)).toEqual(['2026-08-11', ''])
    expect(rows[1].count).toBe(2)
  })

  it('returns nothing for an empty history', () => {
    expect(bucketByDay([])).toEqual([])
  })
})

describe('tracksForDay', () => {
  const dotsFor = (versions: VersionSummary[]) => bucketByDay(versions)[0].tracks

  it('gives a single author one track', () => {
    const tracks = dotsFor([v(1, '2026-08-11 08:00', ada), v(2, '2026-08-11 09:00', ada)])
    expect(tracks).toHaveLength(1)
    expect(tracks[0].dots).toHaveLength(2)
    expect(tracks[0].name).toBe('Ada Lovelace')
  })

  it('splits two authors and orders them by who saved first', () => {
    const tracks = dotsFor([
      v(1, '2026-08-11 15:00', grace),
      v(2, '2026-08-11 08:00', ada),
      v(3, '2026-08-11 16:00', grace),
    ])
    expect(tracks.map((t) => t.key)).toEqual(['user-ada', 'user-grace'])
    expect(tracks[1].dots.map((d) => d.v.number)).toEqual([1, 3])
  })

  it('groups by name when no id is present', () => {
    const tracks = dotsFor([
      v(1, '2026-08-11 08:00', { name: 'Ada Lovelace' }),
      v(2, '2026-08-11 09:00', { name: 'Ada Lovelace' }),
    ])
    expect(tracks).toHaveLength(1)
    expect(tracks[0].key).toBe('Ada Lovelace')
  })

  it('merges the tail into one overflow track past the cap, losing no dots', () => {
    const versions = Array.from({ length: 9 }, (_, i) =>
      v(i + 1, `2026-08-11 0${i}:00`, { id: `u${i}`, name: `User ${i}` }),
    )
    const tracks = dotsFor(versions)
    expect(tracks).toHaveLength(TRACKS_PER_DAY_CAP)
    const overflow = tracks[tracks.length - 1]
    expect(overflow.overflow).toBe(true)
    expect(overflow.authorCount).toBe(9 - (TRACKS_PER_DAY_CAP - 1))
    const total = tracks.reduce((n, t) => n + t.dots.length, 0)
    expect(total).toBe(9)
  })

  it('keeps the overflow track in chronological order', () => {
    const versions = Array.from({ length: 8 }, (_, i) =>
      v(i + 1, `2026-08-11 0${i}:00`, { id: `u${i}`, name: `User ${i}` }),
    )
    const overflow = tracksForDay(bucketByDay(versions)[0].tracks.flatMap((t) => t.dots)).slice(-1)[0]
    const idx = overflow.dots.map((d) => d.index)
    expect(idx).toEqual([...idx].sort((a, b) => a - b))
  })
})

describe('gapBetween', () => {
  const rowsFor = (days: string[]) => bucketByDay(days.map((d, i) => v(i + 1, `${d} 12:00`)))

  it('calls one day a next-day gap', () => {
    const rows = rowsFor(['2026-08-11', '2026-08-12'])
    expect(gapBetween(rows[0], rows[1])).toMatchObject({ tier: 'nextDay', days: 1 })
  })

  it('tiers a few days apart from a week or more', () => {
    expect(gapBetween(...(rowsFor(['2026-08-08', '2026-08-11']) as [never, never]))).toMatchObject({
      tier: 'days',
      days: 3,
    })
    expect(gapBetween(...(rowsFor(['2026-08-01', '2026-08-11']) as [never, never]))).toMatchObject({
      tier: 'wide',
      days: 10,
    })
  })

  it('carries a calendar breakdown for long gaps', () => {
    const rows = rowsFor(['2025-06-09', '2026-08-12'])
    expect(gapBetween(rows[0], rows[1])?.breakdown).toEqual({ years: 1, months: 2, days: 3 })
  })

  it('has nothing to say about the undated bucket', () => {
    const rows = bucketByDay([v(1, '2026-08-11 12:00'), { number: 2 }])
    expect(gapBetween(rows[0], rows[1])).toBeNull()
  })
})

describe('declutter', () => {
  const gaps = (x: number[]) => x.slice(1).map((v, i) => v - x[i])

  it('leaves well-spaced dots exactly where they were', () => {
    const raw = [10, 60, 200, 400]
    expect(declutter(raw, 0, 500)).toEqual(raw)
  })

  it('is a no-op for a single dot', () => {
    expect(declutter([123], 0, 500)).toEqual([123])
    expect(declutter([], 0, 500)).toEqual([])
  })

  it('pushes a cluster apart to at least the minimum gap', () => {
    const out = declutter([100, 101, 102, 103], 0, 500)
    for (const g of gaps(out)) expect(g).toBeGreaterThanOrEqual(MIN_DOT_GAP - 1e-9)
    expect(out).toEqual([...out].sort((a, b) => a - b))
  })

  it('pulls a cluster off the right wall instead of clipping it', () => {
    const out = declutter([498, 499, 500, 500], 0, 500)
    expect(out[out.length - 1]).toBeLessThanOrEqual(500)
    expect(out[0]).toBeGreaterThanOrEqual(0)
    for (const g of gaps(out)) expect(g).toBeGreaterThanOrEqual(MIN_DOT_GAP - 1e-9)
  })

  it('spaces evenly when there are more dots than the axis can separate', () => {
    const raw = Array.from({ length: 40 }, () => 50)
    const out = declutter(raw, 0, 100)
    expect(out[0]).toBe(0)
    expect(out[out.length - 1]).toBe(100)
    expect(out).toEqual([...out].sort((a, b) => a - b))
  })

  it('never leaves the plot bounds', () => {
    const out = declutter([-50, 0, 10, 900], 0, 500)
    for (const x of out) {
      expect(x).toBeGreaterThanOrEqual(0)
      expect(x).toBeLessThanOrEqual(500)
    }
  })
})

describe('x mappings', () => {
  it('maps the clock across the plot width', () => {
    const w = GUTTER_W + 480 + PAD_R
    expect(plotWidth(w)).toBe(480)
    expect(xOfMs(0, w)).toBe(0)
    expect(xOfMs(12 * 3_600_000, w)).toBe(240)
    expect(xOfMs(24 * 3_600_000, w)).toBe(480)
  })

  it('clamps a clock value outside the day', () => {
    const w = GUTTER_W + 480 + PAD_R
    expect(xOfMs(-1, w)).toBe(0)
    expect(xOfMs(99 * 3_600_000, w)).toBe(480)
  })

  it('spaces the thread axis one COL_GAP per save', () => {
    expect(xOfIndex(1) - xOfIndex(0)).toBe(COL_GAP)
    expect(threadWidth(10)).toBe(10 * COL_GAP + PAD_R)
  })
})

describe('layoutStack', () => {
  it('stacks rows top-down and accounts for the gap bands', () => {
    const rows = bucketByDay([v(1, '2026-05-10 09:00'), v(2, '2026-08-11 09:00'), v(3, '2026-08-12 09:00')])
    const { tops, total } = layoutStack(rows, true)
    expect(tops).toHaveLength(3)
    expect(tops[0]).toBe(0)
    expect(tops[1]).toBeGreaterThan(tops[0])
    expect(tops[2]).toBeGreaterThan(tops[1])
    expect(total).toBeGreaterThan(tops[2])
  })

  it('gives a taller stack when the axis is drawn', () => {
    const rows = bucketByDay([v(1, '2026-08-11 09:00'), v(2, '2026-08-12 09:00')])
    expect(layoutStack(rows, true).total).toBeGreaterThan(layoutStack(rows, false).total)
  })

  it('is empty for no rows', () => {
    expect(layoutStack([], true)).toEqual({ tops: [], total: 0 })
  })
})

describe('hourTicks', () => {
  it('draws every hour and labels every sixth when there is room', () => {
    const t = hourTicks(600)
    expect(t).toHaveLength(25)
    expect(t.filter((x) => x.label).map((x) => x.hour)).toEqual([0, 6, 12, 18, 24])
  })

  it('thins to three-hourly in a mid-width panel', () => {
    const t = hourTicks(300)
    expect(t.map((x) => x.hour)).toEqual([0, 3, 6, 9, 12, 15, 18, 21, 24])
    expect(t.filter((x) => x.label).map((x) => x.hour)).toEqual([0, 12, 24])
  })

  it('labels only noon when narrow, and gives up below 200', () => {
    expect(hourTicks(230).filter((x) => x.label).map((x) => x.hour)).toEqual([12])
    expect(hourTicks(150)).toEqual([])
  })
})

describe('indexBase', () => {
  it('is zero for an uncapped history', () => {
    const rows = bucketByDay([v(1, '2026-08-11 09:00'), v(2, '2026-08-12 09:00')])
    expect(indexBase(rows)).toBe(0)
  })

  it('rebases onto the oldest day that survived the row cap', () => {
    // The cap drops the OLDEST days, so a capped stack keeps a suffix of the
    // index range. Without this offset the thread axis would open with a wide
    // empty band before the first dot.
    const all = bucketByDay(
      Array.from({ length: 5 }, (_, i) => v(i + 1, `2026-08-0${i + 1} 09:00`)),
    )
    const kept = all.slice(0, 2) // the two newest days
    expect(indexBase(kept)).toBe(3)
    expect(xOfIndex(3 - indexBase(kept))).toBe(xOfIndex(0))
  })

  it('is zero when nothing is rendered', () => {
    expect(indexBase([])).toBe(0)
  })
})
