// Shared locale-aware formatting. Generalizes the cached-Intl-format pattern
// from tasks/gantt/ganttMath.ts: Intl constructors are expensive, so formats
// are cached per (app locale, options) and the cache flushes on language
// change. Always format through here (or with getDateTimeFmt) rather than
// bare toLocale* calls, so dates follow the PICKED app language rather than
// whatever the browser happens to be set to.

import i18n from '../i18n'

const dtCache = new Map<string, Intl.DateTimeFormat>()
const rtCache = new Map<string, Intl.RelativeTimeFormat>()
const numCache = new Map<string, Intl.NumberFormat>()
const listCache = new Map<string, Intl.ListFormat>()

i18n.on('languageChanged', () => {
  dtCache.clear()
  rtCache.clear()
  numCache.clear()
  listCache.clear()
})

export function getDateTimeFmt(opts: Intl.DateTimeFormatOptions = {}): Intl.DateTimeFormat {
  const key = `${i18n.language}|${JSON.stringify(opts)}`
  let f = dtCache.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat(i18n.language, opts)
    dtCache.set(key, f)
  }
  return f
}

export function getNumberFmt(opts: Intl.NumberFormatOptions = {}): Intl.NumberFormat {
  const key = `${i18n.language}|${JSON.stringify(opts)}`
  let f = numCache.get(key)
  if (!f) {
    f = new Intl.NumberFormat(i18n.language, opts)
    numCache.set(key, f)
  }
  return f
}

export function fmtDate(d: Date | string, opts: Intl.DateTimeFormatOptions = { dateStyle: 'medium' }): string {
  const date = typeof d === 'string' ? new Date(d) : d
  if (Number.isNaN(date.getTime())) return typeof d === 'string' ? d : ''
  return getDateTimeFmt(opts).format(date)
}

// fmtRelative renders "3 days ago" / "in 2 hours" in the app language.
export function fmtRelative(d: Date | string, now: number = Date.now()): string {
  const date = typeof d === 'string' ? new Date(d) : d
  if (Number.isNaN(date.getTime())) return typeof d === 'string' ? d : ''
  const key = i18n.language
  let rtf = rtCache.get(key)
  if (!rtf) {
    rtf = new Intl.RelativeTimeFormat(i18n.language, { numeric: 'auto' })
    rtCache.set(key, rtf)
  }
  const diffSec = (date.getTime() - now) / 1000
  const abs = Math.abs(diffSec)
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ['year', 31_536_000],
    ['month', 2_592_000],
    ['week', 604_800],
    ['day', 86_400],
    ['hour', 3_600],
    ['minute', 60],
  ]
  for (const [unit, secs] of units) {
    if (abs >= secs) return rtf.format(Math.round(diffSec / secs), unit)
  }
  return rtf.format(Math.round(diffSec), 'second')
}

// getListFmt returns the cached conjunction list formatter ("a, b and c"), or
// null on an engine without Intl.ListFormat — the same feature-gate posture as
// fmt/graphemes.ts takes with Intl.Segmenter.
function getListFmt(): Intl.ListFormat | null {
  if (typeof Intl.ListFormat !== 'function') return null
  const key = i18n.language
  let f = listCache.get(key)
  if (!f) {
    f = new Intl.ListFormat(i18n.language, { type: 'conjunction', style: 'long' })
    listCache.set(key, f)
  }
  return f
}

// fmtDuration renders a calendar breakdown (see fmt/dates.ts) as a localized
// phrase — "3 months and 2 days", "1 Jahr, 2 Monate und 3 Tage" — WITHOUT a
// per-unit catalog: each part goes through Intl.NumberFormat's unit style,
// which already knows every locale's plural forms, and Intl.ListFormat joins
// them. Callers wrap the result in their own key ("{{duration}} later"), which
// is the only string a translator has to touch.
//
// Returns '' for a zero span; callers that could hit that must not render.
export function fmtDuration(b: { years: number; months: number; days: number }): string {
  const part = (n: number, unit: 'year' | 'month' | 'week' | 'day') =>
    getNumberFmt({ style: 'unit', unit, unitDisplay: 'long' }).format(n)
  const parts: string[] = []
  if (b.years) parts.push(part(b.years, 'year'))
  if (b.months) parts.push(part(b.months, 'month'))
  if (b.days) {
    // Weeks only below a month, where "2 weeks" beats "14 days". Above one,
    // "1 month, 1 week and 3 days" reads worse than "1 month and 10 days".
    if (!b.years && !b.months && b.days >= 7) {
      const weeks = Math.floor(b.days / 7)
      const rest = b.days % 7
      parts.push(part(weeks, 'week'))
      if (rest) parts.push(part(rest, 'day'))
    } else {
      parts.push(part(b.days, 'day'))
    }
  }
  if (parts.length < 2) return parts.join('')
  return getListFmt()?.format(parts) ?? parts.join(', ')
}

// fmtBytes renders a byte count with locale-aware digits (1,024 vs 1.024).
export function fmtBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return ''
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const fmt = getNumberFmt({ maximumFractionDigits: v >= 100 || i === 0 ? 0 : 1 })
  return `${fmt.format(v)} ${units[i]}`
}
