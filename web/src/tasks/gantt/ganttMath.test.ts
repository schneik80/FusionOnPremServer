import { describe, expect, it } from 'vitest'
import type { Task } from '../types'
import {
  ARROW_INDENT,
  BAR_H,
  ROW_H,
  addDays,
  barRect,
  buildScale,
  dateToX,
  dayWidth,
  daysBetween,
  dependencyPath,
  durationDays,
  fmtDay,
  headerBands,
  isWeekend,
  mondayOf,
  parseDay,
  rowModel,
  scheduledSplit,
  snapDays,
  unitToFit,
  xToDate,
} from './ganttMath'

// mkTask builds a minimal Task; only the fields ganttMath reads matter.
let num = 0
function mkTask(over: Partial<Task>): Task {
  num++
  return {
    id: `t${num}`,
    num,
    projectId: 'p',
    hubId: 'h',
    projectName: 'P',
    title: `Task ${num}`,
    status: 'todo',
    priority: 'medium',
    createdBy: { id: 'u' },
    createdAt: '',
    updatedAt: '',
    docRefs: [],
    dependsOn: [],
    rank: 0,
    ...over,
  }
}

const today = parseDay('2026-07-15') // a Wednesday

describe('dates', () => {
  it('parseDay is local midnight and round-trips through fmtDay', () => {
    const d = parseDay('2026-07-04')
    expect(d.getHours()).toBe(0)
    expect(d.getDate()).toBe(4)
    expect(d.getMonth()).toBe(6)
    expect(fmtDay(d)).toBe('2026-07-04')
  })
  it('mondayOf returns the ISO week start', () => {
    expect(fmtDay(mondayOf(parseDay('2026-07-15')))).toBe('2026-07-13') // Wed → Mon
    expect(fmtDay(mondayOf(parseDay('2026-07-13')))).toBe('2026-07-13') // Mon → itself
    expect(fmtDay(mondayOf(parseDay('2026-07-19')))).toBe('2026-07-13') // Sun → prior Mon
  })
  it('daysBetween is whole local days, DST-tolerant', () => {
    expect(daysBetween(parseDay('2026-03-01'), parseDay('2026-04-01'))).toBe(31) // spans US DST start
    expect(daysBetween(parseDay('2026-07-01'), parseDay('2026-07-01'))).toBe(0)
  })
  it('isWeekend', () => {
    expect(isWeekend(parseDay('2026-07-18'))).toBe(true) // Sat
    expect(isWeekend(parseDay('2026-07-19'))).toBe(true) // Sun
    expect(isWeekend(parseDay('2026-07-15'))).toBe(false)
  })
})

describe('buildScale', () => {
  it('empty task list still yields a today-centered window', () => {
    const scale = buildScale([], 'day', today)
    expect(scale.cols.length).toBeGreaterThan(0)
    expect(scale.start <= today).toBe(true)
    expect(scale.end > today).toBe(true)
  })
  it('spans schedules and due dates with padding', () => {
    const tasks = [
      mkTask({ startDate: '2026-07-06', endDate: '2026-07-10' }),
      mkTask({ dueDate: '2026-08-21' }),
    ]
    const scale = buildScale(tasks, 'day', today)
    expect(scale.start <= parseDay('2026-07-06')).toBe(true)
    expect(scale.end > parseDay('2026-08-21')).toBe(true)
  })
  it('week columns start on Mondays', () => {
    const scale = buildScale([], 'week', today)
    for (const c of scale.cols) expect(c.start.getDay()).toBe(1)
  })
  it('month columns are calendar months', () => {
    const scale = buildScale([], 'month', today)
    for (const c of scale.cols) expect(c.start.getDate()).toBe(1)
  })
})

describe('dateToX / xToDate', () => {
  it('day scale maps whole columns', () => {
    const scale = buildScale([], 'day', today)
    const x0 = dateToX(scale, scale.cols[0].start)
    const x1 = dateToX(scale, scale.cols[1].start)
    expect(x0).toBe(0)
    expect(x1).toBe(scale.colWidth)
  })
  it('month scale interpolates within the real month length', () => {
    const scale = buildScale(
      [mkTask({ startDate: '2026-01-01', endDate: '2026-03-31' })],
      'month',
      today,
    )
    // Feb 15 2026 sits 14/28ths into February.
    const febIdx = scale.cols.findIndex((c) => c.start.getMonth() === 1 && c.start.getFullYear() === 2026)
    const x = dateToX(scale, parseDay('2026-02-15'))
    expect(x).toBeCloseTo(febIdx * scale.colWidth + (14 / 28) * scale.colWidth, 5)
  })
  it('xToDate inverts dateToX to the day', () => {
    const scale = buildScale([], 'week', today)
    const d = parseDay('2026-07-16')
    expect(fmtDay(xToDate(scale, dateToX(scale, d)))).toBe('2026-07-16')
  })
  it('snapDays uses the per-day pixel width', () => {
    const day = buildScale([], 'day', today)
    expect(snapDays(day.colWidth * 3, day)).toBe(3)
    const week = buildScale([], 'week', today)
    expect(snapDays(dayWidth(week) * 2.4, week)).toBe(2)
    expect(snapDays(-dayWidth(week) * 1.6, week)).toBe(-2)
  })
})

describe('barRect', () => {
  it('end date is inclusive: a one-day bar spans a full day column', () => {
    const scale = buildScale([mkTask({ startDate: '2026-07-15', endDate: '2026-07-15' })], 'day', today)
    const rect = barRect(scale, { start: today, end: today }, 0)
    expect(rect.x2 - rect.x1).toBeCloseTo(scale.colWidth, 5)
    expect(rect.y).toBe((ROW_H - BAR_H) / 2)
  })
})

describe('dependencyPath', () => {
  const from = { x1: 100, x2: 200, y: 100, h: BAR_H, mid: 111 }
  it('routes directly when the successor starts well right of the predecessor', () => {
    const to = { x1: 300, x2: 400, y: 172, h: BAR_H, mid: 183 }
    const p = dependencyPath(from, to)
    expect(p).toBe(`M 200 111 H ${300 - ARROW_INDENT} V 183 H 300`)
  })
  it('S-routes through the row gutter when the successor starts left of the predecessor end', () => {
    const to = { x1: 120, x2: 240, y: 172, h: BAR_H, mid: 183 }
    const p = dependencyPath(from, to)
    expect(p).toContain(`H ${200 + ARROW_INDENT}`) // stub out
    expect(p).toContain(`V ${111 + ROW_H / 2}`) // into the gutter below
    expect(p).toContain(`H ${120 - ARROW_INDENT}`) // run back
    expect(p.endsWith('H 120')).toBe(true)
  })
  it('routes upward when the successor sits above', () => {
    const to = { x1: 150, x2: 260, y: 28, h: BAR_H, mid: 39 }
    const p = dependencyPath(from, to)
    expect(p).toContain(`V ${111 - ROW_H / 2}`) // gutter above
  })
})

describe('rowModel', () => {
  it('splits scheduled from backlog', () => {
    const a = mkTask({ startDate: '2026-07-06', endDate: '2026-07-10' })
    const b = mkTask({})
    const { scheduled, backlog } = scheduledSplit([a, b])
    expect(scheduled.map((t) => t.id)).toEqual([a.id])
    expect(backlog.map((t) => t.id)).toEqual([b.id])
  })
  it('keeps done tasks off the backlog but leaves scheduled done tasks on the chart', () => {
    const doneUnscheduled = mkTask({ status: 'done' })
    const doneScheduled = mkTask({ startDate: '2026-07-06', endDate: '2026-07-10', status: 'done' })
    const openUnscheduled = mkTask({})
    const { scheduled, backlog } = scheduledSplit([doneUnscheduled, doneScheduled, openUnscheduled])
    expect(scheduled.map((t) => t.id)).toEqual([doneScheduled.id])
    expect(backlog.map((t) => t.id)).toEqual([openUnscheduled.id])
  })
  it('orders rows by start date and nests stage children under the stage bar', () => {
    const late = mkTask({ startDate: '2026-07-20', endDate: '2026-07-24' })
    const s1 = mkTask({ startDate: '2026-07-08', endDate: '2026-07-10', stage: 'Design' })
    const s2 = mkTask({ startDate: '2026-07-06', endDate: '2026-07-07', stage: 'Design' })
    const early = mkTask({ startDate: '2026-07-01', endDate: '2026-07-03' })
    const rows = rowModel([late, s1, s2, early], new Set())
    expect(rows.map((r) => r.key)).toEqual([
      early.id,
      'stage:Design',
      s2.id, // earlier child first
      s1.id,
      late.id,
    ])
    const stage = rows[1]
    expect(fmtDay(stage.start)).toBe('2026-07-06')
    expect(fmtDay(stage.end)).toBe('2026-07-10')
  })
  it('stage progress is a duration-weighted roll-up; done counts as 100', () => {
    const a = mkTask({ startDate: '2026-07-06', endDate: '2026-07-10', stage: 'S', progress: 50 }) // 5 days
    const b = mkTask({ startDate: '2026-07-11', endDate: '2026-07-15', stage: 'S', status: 'done' }) // 5 days
    const rows = rowModel([a, b], new Set())
    expect(rows[0].kind).toBe('stage')
    expect(rows[0].progress).toBe(75)
  })
  it('collapsed stages hide their children but keep the bar', () => {
    const a = mkTask({ startDate: '2026-07-06', endDate: '2026-07-10', stage: 'S' })
    const rows = rowModel([a], new Set(['S']))
    expect(rows).toHaveLength(1)
    expect(rows[0].kind).toBe('stage')
    expect(rows[0].collapsed).toBe(true)
  })
})

describe('headerBands', () => {
  it('top band groups columns by month on the day scale', () => {
    const scale = buildScale(
      [mkTask({ startDate: '2026-07-25', endDate: '2026-08-05' })],
      'day',
      today,
    )
    const { top, bottom } = headerBands(scale)
    expect(bottom).toHaveLength(scale.cols.length)
    expect(top.length).toBeGreaterThanOrEqual(2) // spans July and August
    const width = top.reduce((acc, b) => acc + b.w, 0)
    expect(width).toBe(scale.width)
  })
  it('top band groups by year on the month scale', () => {
    const scale = buildScale(
      [mkTask({ startDate: '2026-11-01', endDate: '2027-02-15' })],
      'month',
      today,
    )
    const { top } = headerBands(scale)
    expect(top.some((b) => b.label === '2026')).toBe(true)
    expect(top.some((b) => b.label === '2027')).toBe(true)
  })
})

describe('misc', () => {
  it('durationDays is inclusive', () => {
    expect(durationDays(mkTask({ startDate: '2026-07-06', endDate: '2026-07-10' }))).toBe(5)
    expect(durationDays(mkTask({ startDate: '2026-07-06', endDate: '2026-07-06' }))).toBe(1)
    expect(durationDays(mkTask({}))).toBe(0)
  })
  it('addDays crosses month boundaries', () => {
    expect(fmtDay(addDays(parseDay('2026-08-30'), 5))).toBe('2026-09-04')
  })
  it('unitToFit picks a coarser unit for longer spans', () => {
    const short = [mkTask({ startDate: '2026-07-06', endDate: '2026-07-10' })]
    expect(unitToFit(short, 1200)).toBe('day')
    const long = [mkTask({ startDate: '2026-01-01', endDate: '2026-12-31' })]
    expect(unitToFit(long, 800)).toBe('month')
  })
})
