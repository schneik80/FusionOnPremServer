// Calendar-day arithmetic on `YYYY-MM-DD` strings and local-midnight Dates.
// Separate from ../fmt/index.ts, which formats an instant for display: these
// are pure whole-day functions with no locale and no Intl.
//
// Lifted out of tasks/gantt/ganttMath.ts (which re-exports them, so the Gantt
// call sites are unchanged) once components/DateField.tsx needed the same
// math — a shared component must not import from a feature folder.

const DAY_MS = 86_400_000

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

// addMonths clamps the day to the target month's length, so 31 Jan + 1 month
// is 28/29 Feb rather than rolling into March the way a raw setMonth would.
export function addMonths(d: Date, n: number): Date {
  const y = d.getFullYear()
  const m = d.getMonth() + n
  const last = new Date(y, m + 1, 0).getDate()
  return new Date(y, m, Math.min(d.getDate(), last))
}

export function isWeekend(d: Date): boolean {
  const wd = d.getDay()
  return wd === 0 || wd === 6
}

// mondayOf returns the ISO week start (Monday) for a date. The whole app is
// Monday-first — the Gantt calendar bands and the date picker's month grid
// must agree, so this is deliberately not locale-derived.
export function mondayOf(d: Date): Date {
  const wd = (d.getDay() + 6) % 7 // Mon=0 … Sun=6
  return addDays(d, -wd)
}

// daysBetween counts whole local days from a to b (b − a). Rounding absorbs
// the odd DST hour so a 23/25-hour day still counts as one.
export function daysBetween(a: Date, b: Date): number {
  return Math.round((b.getTime() - a.getTime()) / DAY_MS)
}

// calendarBreakdown splits the span between two local-midnight days into
// calendar years/months/days — the shape the History view's between-day labels
// render ("1 year, 2 months and 3 days later").
//
// It walks the anchor forward with addMonths rather than subtracting date
// fields, so it inherits that function's end-of-month clamp: 31 Jan → 28 Feb is
// "1 month", not "28 days". The remainder goes through daysBetween, which
// already rounds the odd DST hour away.
export function calendarBreakdown(from: Date, to: Date): { years: number; months: number; days: number } {
  if (to < from) return calendarBreakdown(to, from)
  let months = (to.getFullYear() - from.getFullYear()) * 12 + (to.getMonth() - from.getMonth())
  if (addMonths(from, months) > to) months--
  const days = daysBetween(addMonths(from, months), to)
  return { years: Math.floor(months / 12), months: months % 12, days }
}

export function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
  )
}

export function today(): Date {
  const n = new Date()
  return new Date(n.getFullYear(), n.getMonth(), n.getDate())
}

// isDay reports whether a string is a well-formed YYYY-MM-DD that round-trips
// through parseDay — "2026-02-31" parses to 3 March, so a range check alone
// would accept a date the user never picked.
export function isDay(s: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(s) && fmtDay(parseDay(s)) === s
}

// monthGrid returns the six Monday-first weeks covering the month `anchor`
// falls in. Always 42 cells, so the popover never changes height between
// months — a shifting calendar makes the next-month chevron move under the
// cursor.
export function monthGrid(anchor: Date): Date[] {
  const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1)
  const start = mondayOf(first)
  return Array.from({ length: 42 }, (_, i) => addDays(start, i))
}
