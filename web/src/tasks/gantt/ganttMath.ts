// Pure geometry for the Gantt view: dates ↔ pixels, row layout, arrow
// routing, calendar header bands. No React, no MUI — everything here is a
// plain function of its inputs so it unit-tests without a DOM.
//
// Blueprint: gantt-task-react's bar-helper math (equal-width columns with
// millisecond interpolation inside each column, which keeps Month view
// correct despite 28–31-day months) and its 4/6-segment orthogonal
// dependency arrows; known upstream bugs deliberately avoided (dates parse
// to LOCAL midnight, empty task lists still produce a scale).

import type { Task } from '../types'
import { isScheduled } from '../types'

export type TimeUnit = 'day' | 'week' | 'month'

export const ROW_H = 36
export const BAR_H = 22
export const HEADER_H = 44 // two 22px calendar bands
export const ARROW_INDENT = 16
export const HANDLE_W = 7
export const COL_WIDTH: Record<TimeUnit, number> = { day: 36, week: 84, month: 120 }
// Empty columns padded around the content span, per unit.
const PAD_COLS: Record<TimeUnit, number> = { day: 7, week: 4, month: 2 }

const DAY_MS = 86_400_000

// ---- dates ----

// parseDay reads YYYY-MM-DD as LOCAL midnight. new Date('YYYY-MM-DD') would
// parse UTC midnight and render a day early in negative-offset zones — the
// classic gantt off-by-one.
export function parseDay(s: string): Date {
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y, (m || 1) - 1, d || 1)
}

export function fmtDay(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

export function addDays(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() + n)
}

export function isWeekend(d: Date): boolean {
  const wd = d.getDay()
  return wd === 0 || wd === 6
}

// mondayOf returns the ISO week start (Monday) for a date.
export function mondayOf(d: Date): Date {
  const wd = (d.getDay() + 6) % 7 // Mon=0 … Sun=6
  return addDays(d, -wd)
}

// daysBetween counts whole local days from a to b (b − a). Rounding absorbs
// the odd DST hour so a 23/25-hour day still counts as one.
export function daysBetween(a: Date, b: Date): number {
  return Math.round((b.getTime() - a.getTime()) / DAY_MS)
}

// ---- scale ----

export interface GanttCol {
  start: Date
  end: Date // exclusive
}

export interface GanttScale {
  unit: TimeUnit
  cols: GanttCol[]
  colWidth: number
  start: Date // cols[0].start
  end: Date // cols.at(-1).end
  width: number // cols.length * colWidth
}

function unitStart(d: Date, unit: TimeUnit): Date {
  switch (unit) {
    case 'day':
      return new Date(d.getFullYear(), d.getMonth(), d.getDate())
    case 'week':
      return mondayOf(d)
    case 'month':
      return new Date(d.getFullYear(), d.getMonth(), 1)
  }
}

function nextUnit(d: Date, unit: TimeUnit): Date {
  switch (unit) {
    case 'day':
      return addDays(d, 1)
    case 'week':
      return addDays(d, 7)
    case 'month':
      return new Date(d.getFullYear(), d.getMonth() + 1, 1)
  }
}

// buildScale spans min(schedule starts, today) … max(schedule ends, due
// dates, today), padded by a few empty columns. An empty or all-backlog
// project still gets a today-centered window rather than a crash (upstream
// bug: ganttDateRange reads tasks[0] unconditionally).
export function buildScale(tasks: Task[], unit: TimeUnit, today: Date = new Date()): GanttScale {
  let min = unitStart(today, unit)
  let max = min
  for (const t of tasks) {
    if (isScheduled(t)) {
      const s = parseDay(t.startDate!)
      const e = parseDay(t.endDate!)
      if (s < min) min = s
      if (e > max) max = e
    }
    if (t.dueDate) {
      const due = parseDay(t.dueDate)
      if (due > max) max = due
    }
  }
  const pad = PAD_COLS[unit]
  let cursor = unitStart(min, unit)
  for (let i = 0; i < pad; i++) {
    // Step back one whole unit: the instant before the current column start
    // belongs to the previous column.
    cursor = unitStart(new Date(cursor.getTime() - 1), unit)
  }
  const cols: GanttCol[] = []
  let c = cursor
  // Columns through the content end, then the trailing pad. The guard bounds
  // a pathological span (bad data decades out) at ~20 years of day columns.
  const guardMax = 8000
  while (c <= max && cols.length < guardMax) {
    const end = nextUnit(c, unit)
    cols.push({ start: c, end })
    c = end
  }
  for (let i = 0; i < pad; i++) {
    const end = nextUnit(c, unit)
    cols.push({ start: c, end })
    c = end
  }
  const colWidth = COL_WIDTH[unit]
  return {
    unit,
    cols,
    colWidth,
    start: cols[0].start,
    end: cols[cols.length - 1].end,
    width: cols.length * colWidth,
  }
}

// dateToX maps a date to an x coordinate: whole columns plus linear
// interpolation inside the column's real millisecond span.
export function dateToX(scale: GanttScale, date: Date): number {
  const { cols, colWidth } = scale
  if (date <= scale.start) return 0
  if (date >= scale.end) return scale.width
  // Columns are uniform per unit except month; binary search keeps this
  // O(log n) even on multi-year day scales.
  let lo = 0
  let hi = cols.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (cols[mid].end.getTime() <= date.getTime()) lo = mid + 1
    else hi = mid
  }
  const col = cols[lo]
  const frac = (date.getTime() - col.start.getTime()) / (col.end.getTime() - col.start.getTime())
  return lo * colWidth + frac * colWidth
}

// xToDate inverts dateToX, snapped to whole local days.
export function xToDate(scale: GanttScale, x: number): Date {
  const { cols, colWidth } = scale
  const idx = Math.min(cols.length - 1, Math.max(0, Math.floor(x / colWidth)))
  const col = cols[idx]
  const frac = Math.min(1, Math.max(0, x / colWidth - idx))
  const ms = col.start.getTime() + frac * (col.end.getTime() - col.start.getTime())
  const d = new Date(ms)
  // Snap to the nearest local day boundary.
  const floor = new Date(d.getFullYear(), d.getMonth(), d.getDate())
  return d.getTime() - floor.getTime() >= DAY_MS / 2 ? addDays(floor, 1) : floor
}

// dayWidth is the pixel width of one day at this scale (for drag snapping).
export function dayWidth(scale: GanttScale): number {
  const col = scale.cols[0]
  const days = daysBetween(col.start, col.end) || 1
  return scale.colWidth / days
}

// ---- rows ----

// A row is either one task's bar or a derived stage bar aggregating its
// children. Stage bars are computed, never stored.
export interface GanttRow {
  kind: 'task' | 'stage'
  key: string
  task?: Task // kind === 'task'
  stage?: string // kind === 'stage'
  tasks?: Task[] // kind === 'stage': its children, row-ordered
  start: Date
  end: Date // inclusive last day
  progress: number // stage: duration-weighted roll-up
  collapsed?: boolean
}

// rowModel lays out scheduled tasks: stages (and stageless tasks) interleave
// ordered by earliest start, children nest under their stage bar sorted
// startDate → endDate → num. Collapsed stages contribute only their bar row.
export function rowModel(tasks: Task[], collapsed: Set<string>): GanttRow[] {
  const scheduled = tasks.filter(isScheduled)
  const byStage = new Map<string, Task[]>()
  const loose: Task[] = []
  for (const t of scheduled) {
    const stage = (t.stage ?? '').trim()
    if (stage) {
      const list = byStage.get(stage)
      if (list) list.push(t)
      else byStage.set(stage, [t])
    } else {
      loose.push(t)
    }
  }
  const cmp = (a: Task, b: Task) =>
    a.startDate!.localeCompare(b.startDate!) ||
    a.endDate!.localeCompare(b.endDate!) ||
    a.num - b.num
  loose.sort(cmp)

  interface Block {
    sortKey: string
    rows: GanttRow[]
  }
  const blocks: Block[] = []
  for (const [stage, children] of byStage) {
    children.sort(cmp)
    let progressDays = 0
    let totalDays = 0
    let minStart = children[0].startDate!
    let maxEnd = children[0].endDate!
    for (const c of children) {
      if (c.startDate! < minStart) minStart = c.startDate!
      if (c.endDate! > maxEnd) maxEnd = c.endDate!
      const dur = daysBetween(parseDay(c.startDate!), parseDay(c.endDate!)) + 1
      totalDays += dur
      progressDays += ((c.status === 'done' ? 100 : c.progress ?? 0) / 100) * dur
    }
    const isCollapsed = collapsed.has(stage)
    const stageRow: GanttRow = {
      kind: 'stage',
      key: `stage:${stage}`,
      stage,
      tasks: children,
      start: parseDay(minStart),
      end: parseDay(maxEnd),
      progress: totalDays ? Math.round((progressDays / totalDays) * 100) : 0,
      collapsed: isCollapsed,
    }
    const rows = [stageRow]
    if (!isCollapsed) {
      for (const c of children) rows.push(taskRow(c))
    }
    blocks.push({ sortKey: minStart, rows })
  }
  for (const t of loose) {
    blocks.push({ sortKey: t.startDate!, rows: [taskRow(t)] })
  }
  blocks.sort((a, b) => a.sortKey.localeCompare(b.sortKey))
  return blocks.flatMap((b) => b.rows)
}

function taskRow(t: Task): GanttRow {
  return {
    kind: 'task',
    key: t.id,
    task: t,
    start: parseDay(t.startDate!),
    end: parseDay(t.endDate!),
    progress: t.status === 'done' ? 100 : t.progress ?? 0,
  }
}

// scheduledSplit partitions tasks for the Gantt view. Scheduled tasks (done
// or not) render on the chart; the backlog rail lists only unscheduled tasks
// that are still open — a done task with no dates has nothing left to plan.
export function scheduledSplit(tasks: Task[]): { scheduled: Task[]; backlog: Task[] } {
  const scheduled: Task[] = []
  const backlog: Task[] = []
  for (const t of tasks) {
    if (isScheduled(t)) scheduled.push(t)
    else if (t.status !== 'done') backlog.push(t)
  }
  return { scheduled, backlog }
}

// ---- bar rects ----

export interface BarRect {
  x1: number
  x2: number
  y: number
  h: number
  mid: number
}

// barRect computes a row's bar box in body coordinates (the calendar header
// is a separate sticky strip, so y starts at the first row). End dates are
// inclusive (a one-day task spans its whole day): the right edge sits at
// end+1day.
export function barRect(scale: GanttScale, row: { start: Date; end: Date }, rowIndex: number): BarRect {
  const x1 = dateToX(scale, row.start)
  const x2 = dateToX(scale, addDays(row.end, 1))
  const y = rowIndex * ROW_H + (ROW_H - BAR_H) / 2
  return { x1, x2, y, h: BAR_H, mid: y + BAR_H / 2 }
}

// ---- dependency arrows ----

// dependencyPath routes finish→start: out of the predecessor, half a row
// into the gutter, across, down/up to the successor's row and in. When the
// successor starts left of the predecessor's end the route becomes the
// 6-segment S through the gutter (gantt-task-react's algorithm).
export function dependencyPath(from: BarRect, to: BarRect): string {
  const down = to.mid > from.mid ? 1 : -1
  const stubEnd = from.x2 + ARROW_INDENT
  if (stubEnd < to.x1 - ARROW_INDENT) {
    // Direct: out, vertical to target row, in.
    return `M ${from.x2} ${from.mid} H ${to.x1 - ARROW_INDENT} V ${to.mid} H ${to.x1}`
  }
  // Backtrack: out, drop into the gutter between rows, run back, then in.
  const gutterY = from.mid + (down * ROW_H) / 2
  return (
    `M ${from.x2} ${from.mid} H ${stubEnd} V ${gutterY} ` +
    `H ${to.x1 - ARROW_INDENT} V ${to.mid} H ${to.x1}`
  )
}

// arrowHead returns polygon points for the small triangle at the target.
export function arrowHead(to: BarRect): string {
  const s = 4.5
  return `${to.x1},${to.mid} ${to.x1 - s * 1.6},${to.mid - s} ${to.x1 - s * 1.6},${to.mid + s}`
}

// ---- calendar header ----

export interface HeaderBand {
  x: number
  w: number
  label: string
}

const fmtCache = new Map<string, Intl.DateTimeFormat>()
function fmt(opts: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const key = JSON.stringify(opts)
  let f = fmtCache.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat(undefined, opts)
    fmtCache.set(key, f)
  }
  return f
}

// headerBands: top band = coarse unit emitted at boundaries (month for
// day/week scales, year for month scale); bottom band = one label per column.
export function headerBands(scale: GanttScale): { top: HeaderBand[]; bottom: HeaderBand[] } {
  const { cols, colWidth, unit } = scale
  const bottom: HeaderBand[] = cols.map((c, i) => ({
    x: i * colWidth,
    w: colWidth,
    label:
      unit === 'day'
        ? `${fmt({ weekday: 'narrow' }).format(c.start)} ${c.start.getDate()}`
        : unit === 'week'
          ? `${fmt({ month: 'short' }).format(c.start)} ${c.start.getDate()}`
          : fmt({ month: 'short' }).format(c.start),
  }))
  const top: HeaderBand[] = []
  const topKey = (d: Date) =>
    unit === 'month' ? `${d.getFullYear()}` : `${d.getFullYear()}-${d.getMonth()}`
  const topLabel = (d: Date) =>
    unit === 'month'
      ? String(d.getFullYear())
      : fmt({ month: 'long', year: 'numeric' }).format(d)
  let runStart = 0
  for (let i = 1; i <= cols.length; i++) {
    if (i === cols.length || topKey(cols[i].start) !== topKey(cols[runStart].start)) {
      top.push({
        x: runStart * colWidth,
        w: (i - runStart) * colWidth,
        label: topLabel(cols[runStart].start),
      })
      runStart = i
    }
  }
  return { top, bottom }
}

// ---- misc ----

// snapDays converts a drag delta in pixels to whole days.
export function snapDays(dxPx: number, scale: GanttScale): number {
  return Math.round(dxPx / dayWidth(scale))
}

// durationDays is the inclusive length of a schedule in days.
export function durationDays(t: Task): number {
  if (!isScheduled(t)) return 0
  return daysBetween(parseDay(t.startDate!), parseDay(t.endDate!)) + 1
}

// unitToFit picks the coarsest-enough unit so the whole scheduled span fits
// a container width (zoom-to-fit).
export function unitToFit(tasks: Task[], containerWidth: number): TimeUnit {
  const scheduled = tasks.filter(isScheduled)
  if (!scheduled.length) return 'week'
  let min = scheduled[0].startDate!
  let max = scheduled[0].endDate!
  for (const t of scheduled) {
    if (t.startDate! < min) min = t.startDate!
    if (t.endDate! > max) max = t.endDate!
  }
  const days = daysBetween(parseDay(min), parseDay(max)) + 1
  for (const unit of ['day', 'week', 'month'] as TimeUnit[]) {
    const px = (days * COL_WIDTH[unit]) / (unit === 'day' ? 1 : unit === 'week' ? 7 : 30)
    if (px <= containerWidth) return unit
  }
  return 'month'
}
