// Layout maths for the design History timeline. Pure — no React, no MUI, no
// DOM — because the repo's test setup is plain vitest over modules, so this is
// the only way the geometry gets covered at all. HistoryTimeline/DayRow do the
// drawing; every number they draw with comes from here.
//
// The view is a stack of day rows, newest at the top. Inside a row each author
// gets a track, and the dots sit on one of two x mappings:
//
//   • Day view (default)  — x is the clock: 00:00 at the left edge, 24:00 at
//     the right, the same scale in every row, so noon is the same column all
//     the way down and a day's shape is comparable to the day above it.
//   • Thread view (checkbox) — x is the version's position in the whole
//     history, COL_GAP apart, so empty time costs nothing and consecutive saves
//     can be threaded with a polyline across rows. This is the old single-strip
//     History refolded into day rows.
//
// Drawing weights (dot radius, rail width) live in ../graphstyle.ts and are
// shared with the production Batch timeline. The spacing below is this graph's
// own, per that file's rule.

import type { HistoryChange, HistorySave, VersionSummary } from '../../api/types'
import { calendarBreakdown, daysBetween, fmtDay, parseDay } from '../../fmt/dates'
import { NODE_R } from '../graphstyle'

// ---------------------------------------------------------------- constants

export const GUTTER_W = 46 // avatar column, LEFT of the plot — never eats axis space
export const PAD_R = 14
export const TRACK_H = 30 // one author track
export const AXIS_H = 20 // hour labels under the last track (day view only)
export const ROW_PAD_Y = 8
export const HEADER_H = 22 // the day's date line above its tracks

// Gap bands between two day rows. Fixed heights per tier, so the whole stack's
// vertical layout is computable here rather than measured in the DOM — that is
// what lets the thread overlay place its polyline.
export const GAP_H_TIGHT = 18 // next day
export const GAP_H = 24 // a few days
export const GAP_H_WIDE = 32 // a week or more

// Thread view: the pitch between consecutive saves. 38 px is what the previous
// single-strip History used, deliberately unchanged so the toggle reads as
// "unfold the strip", not "redraw the graph".
export const COL_GAP = 38

// Day view: the closest two dots may sit on one track before the declutter pass
// pushes them apart.
export const MIN_DOT_GAP = NODE_R * 2 + 2

// An edit that made no version (a property changed, a milestone marked) is
// drawn as a small open ring, deliberately smaller than a save's filled dot —
// a property tweak must never be mistaken for a save.
export const CHANGE_R = NODE_R - 2.5

export const DAY_ROWS_CAP = 60 // render cap, not a data cap — see HistoryTimeline
export const TRACKS_PER_DAY_CAP = 6 // above this, the tail merges into one overflow track

// Thread view is height-bounded so it owns both scrollbars and they stay at the
// edges of the visible box. MIN_VIEW_H is the floor for a short panel — below
// it the region scrolls the tab rather than collapsing to nothing.
export const MIN_VIEW_H = 320

const DAY_MS = 86_400_000

// ---------------------------------------------------------------- types

/**
 * A HistoryEvent is one thing on the timeline: a save (a version, from
 * itemVersions) or a change (an edit that made no version, from the history
 * list, shown behind the "Show other changes" toggle). The two share author and
 * instant, which is all the bucketing needs — a change lands on its author's
 * track on its own day exactly as a save does, so someone who only ever edited
 * properties gets a track of their own.
 */
export type HistoryEvent = ({ kind: 'save' } & VersionSummary) | ({ kind: 'change' } & HistoryChange)

/**
 * applyHistoryMarkers puts the history's milestone names and release labels
 * onto the versions they mark. The history has no version numbers, so the two
 * lists are joined BY POSITION, newest first — there is one history save per
 * version — and only when they are the same length: a marker on the wrong
 * version is worse than no marker, so a length disagreement leaves every
 * version as it was (the v2 isMilestone flag still holds).
 *
 * This is what lights up the accent ring (milestone) and the filled ring
 * (release) on a save's dot; `revision` had no source before it.
 */
export function applyHistoryMarkers(versions: VersionSummary[], saves?: HistorySave[]): VersionSummary[] {
  if (!saves || saves.length !== versions.length) return versions
  const byNumberDesc = [...versions].sort((a, b) => b.number - a.number)
  const marked = new Map<number, VersionSummary>()
  byNumberDesc.forEach((v, i) => {
    const s = saves[i]
    if (!s.milestone && !s.revision) return
    marked.set(v.number, {
      ...v,
      isMilestone: true,
      milestoneName: s.milestone || v.milestoneName,
      revision: s.revision || v.revision,
    })
  })
  return marked.size ? versions.map((v) => marked.get(v.number) ?? v) : versions
}

/** toEvents tags both lists; bucketByDay does the ordering. */
export function toEvents(versions: VersionSummary[], changes?: HistoryChange[]): HistoryEvent[] {
  const out: HistoryEvent[] = versions.map((v) => ({ kind: 'save', ...v }))
  for (const c of changes ?? []) out.push({ kind: 'change', ...c })
  return out
}

/**
 * humanizeChangeType renders an unmapped HistoryChange typename as something a
 * reader can use: the "HistoryChange" suffix dropped and the stem split on its
 * capitals ("BomEditHistoryChange" → "Bom Edit"). The schema has more change
 * types than any one design has produced, so an unknown one still says
 * something truthful rather than vanishing from the history.
 */
export function humanizeChangeType(type: string): string {
  const stem = type.endsWith('HistoryChange') ? type.slice(0, -'HistoryChange'.length) : type
  const words = stem.match(/[A-Z][a-z0-9]*|[A-Z]+(?![a-z])/g)
  return words?.length ? words.join(' ') : stem || 'Change'
}

export interface HistoryDot {
  v: HistoryEvent
  /** Stable, unique per dot: `v<number>` for a save, `c<index>` for a change. */
  key: string
  /** Position in the whole history, oldest = 0. Drives the thread x mapping. */
  index: number
  /** Milliseconds since local midnight; the day-view x mapping. */
  ms: number
}

export interface HistoryTrack {
  /** Stable grouping key: APS user id, else display name, else ''. */
  key: string
  name: string
  dots: HistoryDot[]
  /** True for the merged tail track when a day has more authors than fit. */
  overflow: boolean
  /** Distinct author count folded into this track (overflow rows only). */
  authorCount: number
}

export interface HistoryDay {
  /** YYYY-MM-DD in local time, or '' for the undated bucket. */
  day: string
  /** Local midnight of `day`; null for the undated bucket. */
  date: Date | null
  tracks: HistoryTrack[]
  /** Every dot on the row: saves + changes. */
  count: number
  saves: number
  changes: number
}

export type GapTier = 'nextDay' | 'days' | 'wide'

export interface HistoryGap {
  tier: GapTier
  /** Whole days between the two rows. */
  days: number
  breakdown: { years: number; months: number; days: number }
}

// ---------------------------------------------------------------- bucketing

/**
 * authorKey groups a version onto a track. The APS user id is preferred so two
 * people who share a display name stay apart and a rename does not split one
 * person into two tracks; the name is the fallback for versions whose author id
 * the API did not resolve.
 */
export function authorKey(v: { createdById?: string; createdBy?: string }): string {
  return v.createdById || v.createdBy || ''
}

/**
 * parseCreatedOn returns a valid Date or null. A version with no usable
 * timestamp is not dropped — it lands in the undated bucket.
 */
export function parseCreatedOn(v: { createdOn?: string }): Date | null {
  if (!v.createdOn) return null
  const d = new Date(v.createdOn)
  return Number.isNaN(d.getTime()) ? null : d
}

/**
 * bucketByDay turns the API's newest-first version list into day rows, newest
 * day first, each split into per-author tracks.
 *
 * Days are LOCAL calendar days (via fmtDay), never a slice of the RFC3339
 * string: a 23:30 save would otherwise jump to the next day for anyone east of
 * Greenwich. Undated versions collect in one trailing bucket.
 */
export function bucketByDay(events: HistoryEvent[]): HistoryDay[] {
  // Oldest → newest, so `index` is the position on the thread axis.
  const ordered = [...events]
    .map((v) => ({ v, at: parseCreatedOn(v) }))
    .sort((a, b) => {
      if (!a.at) return 1 // undated sorts last, order among themselves preserved
      if (!b.at) return -1
      return a.at.getTime() - b.at.getTime()
    })

  const byDay = new Map<string, HistoryDot[]>()
  ordered.forEach(({ v, at }, index) => {
    const day = at ? fmtDay(at) : ''
    const ms = at ? at.getHours() * 3_600_000 + at.getMinutes() * 60_000 + at.getSeconds() * 1000 : 0
    const key = v.kind === 'save' ? `v${v.number}` : `c${index}`
    const dots = byDay.get(day)
    if (dots) dots.push({ v, key, index, ms })
    else byDay.set(day, [{ v, key, index, ms }])
  })

  const undated = byDay.get('')
  byDay.delete('')

  // ISO day strings sort lexicographically = chronologically.
  const days = [...byDay.keys()].sort().reverse()
  const row = (day: string, dots: HistoryDot[]): HistoryDay => {
    const changes = dots.filter((d) => d.v.kind === 'change').length
    return {
      day,
      date: day ? parseDay(day) : null,
      tracks: tracksForDay(dots),
      count: dots.length,
      saves: dots.length - changes,
      changes,
    }
  }
  const rows = days.map((day) => row(day, byDay.get(day)!))
  if (undated) rows.push(row('', undated))
  return rows
}

/**
 * tracksForDay splits one day's dots into per-author tracks, ordered by who
 * saved first that day.
 *
 * Ordering by first save rather than by volume keeps a person's slot stable
 * within the day; a globally fixed slot per person was rejected because a day
 * with one of five authors would then reserve four empty tracks.
 *
 * Beyond TRACKS_PER_DAY_CAP authors the tail merges into a single overflow
 * track. Nothing is hidden — every dot still renders and still carries its own
 * author in its tooltip — only the row height is bounded.
 */
export function tracksForDay(dots: HistoryDot[]): HistoryTrack[] {
  const byAuthor = new Map<string, HistoryDot[]>()
  for (const d of dots) {
    const key = authorKey(d.v)
    const list = byAuthor.get(key)
    if (list) list.push(d)
    else byAuthor.set(key, [d])
  }

  const all: HistoryTrack[] = [...byAuthor.entries()]
    .map(([key, list]) => ({
      key,
      name: list[0].v.createdBy ?? '',
      dots: list,
      overflow: false,
      authorCount: 1,
    }))
    .sort((a, b) => a.dots[0].index - b.dots[0].index)

  if (all.length <= TRACKS_PER_DAY_CAP) return all

  const head = all.slice(0, TRACKS_PER_DAY_CAP - 1)
  const tail = all.slice(TRACKS_PER_DAY_CAP - 1)
  head.push({
    key: tail.map((t) => t.key).join(' '),
    name: tail.map((t) => t.name).join(', '),
    dots: tail.flatMap((t) => t.dots).sort((a, b) => a.index - b.index),
    overflow: true,
    authorCount: tail.length,
  })
  return head
}

/**
 * gapBetween describes the elapsed time from the older row to the newer one.
 * Returns null when the two rows are consecutive calendar days in the API's own
 * sense (same day, or the undated bucket), where there is nothing to say.
 */
export function gapBetween(newer: HistoryDay, older: HistoryDay): HistoryGap | null {
  if (!newer.date || !older.date) return null
  const days = daysBetween(older.date, newer.date)
  if (days <= 0) return null
  const tier: GapTier = days === 1 ? 'nextDay' : days < 7 ? 'days' : 'wide'
  return { tier, days, breakdown: calendarBreakdown(older.date, newer.date) }
}

export function gapHeight(tier: GapTier): number {
  return tier === 'nextDay' ? GAP_H_TIGHT : tier === 'days' ? GAP_H : GAP_H_WIDE
}

// ---------------------------------------------------------------- geometry

export function rowHeight(trackCount: number, withAxis: boolean): number {
  return ROW_PAD_Y * 2 + Math.max(1, trackCount) * TRACK_H + (withAxis ? AXIS_H : 0)
}

export function trackY(i: number): number {
  return ROW_PAD_Y + i * TRACK_H + TRACK_H / 2
}

/** Usable plot width inside a row, excluding the sticky avatar gutter. */
export function plotWidth(w: number): number {
  return Math.max(80, w - GUTTER_W - PAD_R)
}

/** Day view: milliseconds since local midnight → x inside the plot. */
export function xOfMs(ms: number, w: number): number {
  const f = Math.max(0, Math.min(1, ms / DAY_MS))
  return f * plotWidth(w)
}

/** Thread view: position in the whole history → x inside the plot. */
export function xOfIndex(index: number): number {
  return COL_GAP / 2 + index * COL_GAP
}

/**
 * indexBase is the lowest version index among the rendered rows — what
 * xOfIndex must be offset by.
 *
 * The day-row cap drops the OLDEST days, so a capped history keeps a suffix of
 * the index range. Without rebasing, the thread axis would start with sixty
 * days' worth of blank width before the first dot.
 */
export function indexBase(rows: HistoryDay[]): number {
  let min = Infinity
  for (const row of rows) {
    for (const track of row.tracks) {
      for (const d of track.dots) min = Math.min(min, d.index)
    }
  }
  return Number.isFinite(min) ? min : 0
}

/** Thread view: total plot width for a rendered history of `count` versions. */
export function threadWidth(count: number): number {
  return Math.max(COL_GAP, count * COL_GAP) + PAD_R
}

/**
 * layoutStack walks the rendered rows top to bottom and returns each row's y
 * offset within the stack, plus the total height. Header and gap heights are
 * constants precisely so this can be arithmetic instead of a DOM measurement —
 * ThreadOverlay needs a row's y before the browser has laid anything out.
 */
export function layoutStack(rows: HistoryDay[], withAxis: boolean): { tops: number[]; total: number } {
  const tops: number[] = []
  let y = 0
  rows.forEach((row, i) => {
    if (i > 0) {
      const gap = gapBetween(rows[i - 1], row)
      y += gap ? gapHeight(gap.tier) : 0
    }
    tops.push(y)
    y += HEADER_H + rowHeight(row.tracks.length, withAxis)
  })
  return { tops, total: y }
}

// ---------------------------------------------------------------- declutter

/**
 * declutter nudges same-track dots apart so a burst of saves a minute apart
 * does not stack into one blob, while preserving order and staying as close to
 * true clock position as it can. Day view only — the thread axis already
 * guarantees a full COL_GAP between saves.
 *
 * Forward pass pushes right; the back pass pulls the run off the right wall, so
 * a cluster at 23:59 spreads leftward instead of being clipped.
 */
export function declutter(rawX: number[], left: number, right: number, minGap = MIN_DOT_GAP): number[] {
  const n = rawX.length
  if (n === 0) return []
  const x = rawX.map((v) => Math.min(right, Math.max(left, v)))

  // More dots than the axis can ever separate: space them evenly and let the
  // tooltip carry the exact time.
  if ((n - 1) * minGap > right - left) {
    // Interpolate rather than accumulate a step: i * step drifts past `right`
    // on the last dot for most widths.
    if (n === 1) return [left]
    return Array.from({ length: n }, (_, i) => left + ((right - left) * i) / (n - 1))
  }

  for (let i = 1; i < n; i++) if (x[i] - x[i - 1] < minGap) x[i] = x[i - 1] + minGap
  for (let i = n - 2; i >= 0; i--) if (x[i + 1] - x[i] < minGap) x[i] = x[i + 1] - minGap
  if (x[n - 1] > right) {
    const d = x[n - 1] - right
    for (let i = 0; i < n; i++) x[i] -= d
  }
  return x
}

// ---------------------------------------------------------------- hour axis

/**
 * hourTicks picks the hour gridlines for a plot of the given width, thinning
 * them as the details panel narrows so the labels never collide. `label` marks
 * the ticks that get a printed hour.
 */
export function hourTicks(plotW: number): { hour: number; label: boolean }[] {
  if (plotW < 200) return []
  const every = plotW >= 420 ? 1 : plotW >= 260 ? 3 : 6
  const labelEvery = plotW >= 420 ? 6 : plotW >= 260 ? 12 : 24
  const ticks: { hour: number; label: boolean }[] = []
  for (let h = 0; h <= 24; h += every) {
    ticks.push({ hour: h, label: h % labelEvery === 0 })
  }
  // Below 260 px only noon is labelled — the edges are implied by the row.
  if (plotW < 260) return ticks.map((t) => ({ ...t, label: t.hour === 12 }))
  return ticks
}
