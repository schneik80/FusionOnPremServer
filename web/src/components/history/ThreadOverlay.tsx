import { useMemo } from 'react'
import { alpha, useTheme } from '@mui/material/styles'
import { CONNECTOR_OPACITY, CONNECTOR_W } from '../graphstyle'
import {
  GUTTER_W,
  HEADER_H,
  layoutStack,
  trackY,
  xOfIndex,
} from './historyLayout'
import type { HistoryDay } from './historyLayout'

// Thread mode's connecting line: one polyline through every save in
// chronological order, plus a seam wherever the axis crosses from one day into
// the next.
//
// It lives in its own SVG stretched over the whole stack, because the line
// crosses rows and a per-row SVG cannot draw outside itself. That is only
// possible because the stack's vertical layout is arithmetic — fixed header and
// gap-band heights (see historyLayout.layoutStack) — so a row's y is known
// without measuring the DOM.
//
// Decorative and pointer-transparent: it repeats what the rows already say, and
// must never steal a dot's hover.
export default function ThreadOverlay({
  rows,
  width,
  base,
}: {
  rows: HistoryDay[]
  width: number
  /** The index the axis starts from — must match what DayRow drew with. */
  base: number
}) {
  const theme = useTheme()

  const { points, seams, total } = useMemo(() => {
    const { tops, total } = layoutStack(rows, false)
    // Rows are newest-first; the thread is drawn oldest → newest, so walk them
    // in reverse and collect every dot with its absolute position.
    const pts: { index: number; x: number; y: number }[] = []
    const dayRanges: { first: number; last: number }[] = []
    for (let r = rows.length - 1; r >= 0; r--) {
      const row = rows[r]
      if (!row.date) continue // the undated bucket has no place on a time axis
      let first = Infinity
      let last = -Infinity
      row.tracks.forEach((track, ti) => {
        for (const d of track.dots) {
          pts.push({
            index: d.index,
            x: GUTTER_W + xOfIndex(d.index - base),
            y: tops[r] + HEADER_H + trackY(ti),
          })
          first = Math.min(first, d.index)
          last = Math.max(last, d.index)
        }
      })
      if (first <= last) dayRanges.push({ first, last })
    }
    pts.sort((a, b) => a.index - b.index)

    // A seam sits midway between the last save of one day and the first of the
    // next — the only cue that the axis has crossed a date boundary, since
    // empty time consumes no width.
    const s: number[] = []
    for (let i = 1; i < dayRanges.length; i++) {
      const mid = (xOfIndex(dayRanges[i - 1].last - base) + xOfIndex(dayRanges[i].first - base)) / 2
      s.push(GUTTER_W + mid)
    }
    return { points: pts, seams: s, total }
  }, [rows, base])

  if (points.length === 0) return null

  return (
    <svg
      width={width}
      height={total}
      aria-hidden
      style={{
        position: 'absolute',
        top: 0,
        left: 0,
        pointerEvents: 'none',
        overflow: 'visible',
      }}
    >
      {seams.map((x, i) => (
        <line
          key={`seam-${i}`}
          x1={x}
          y1={0}
          x2={x}
          y2={total}
          stroke={theme.palette.divider}
          strokeOpacity={0.6}
          strokeDasharray="2 4"
        />
      ))}
      {points.length > 1 && (
        <polyline
          points={points.map((p) => `${p.x},${p.y}`).join(' ')}
          fill="none"
          stroke={alpha(theme.palette.text.secondary, 0.6)}
          strokeWidth={CONNECTOR_W}
          strokeOpacity={CONNECTOR_OPACITY}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
      )}
    </svg>
  )
}
