import { useEffect, useMemo, useRef, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import {
  Box,
  Checkbox,
  CircularProgress,
  FormControlLabel,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from '@mui/material'
import { alpha, darken, lighten, useTheme } from '@mui/material/styles'
import type { ActivityReport } from '../api/types'

// ActivityHeatmap renders a design's activity as an isometric 3D heat map —
// inspired by jasonlong/isometric-contributions. Rather than one all-time chart,
// it shows a single bounded WINDOW (one day / week / month / year) sub-divided
// into smaller cells, with prev/next to step through time. Bar height and colour
// both encode the change count. Re-buckets the same events client-side, so the
// toggle and stepper need no extra fetch.

type Gran = 'day' | 'week' | 'month' | 'year'

const DAY_MS = 86_400_000
const HOUR_MS = 3_600_000
// Catalog keys for the axis labels (details namespace), indexed by month /
// UTC weekday number and rendered through t() where the labels are built.
const MONTH_KEYS = [
  'activity.monthJan',
  'activity.monthFeb',
  'activity.monthMar',
  'activity.monthApr',
  'activity.monthMay',
  'activity.monthJun',
  'activity.monthJul',
  'activity.monthAug',
  'activity.monthSep',
  'activity.monthOct',
  'activity.monthNov',
  'activity.monthDec',
]
const WEEKDAY_KEYS = [
  'activity.weekdaySun',
  'activity.weekdayMon',
  'activity.weekdayTue',
  'activity.weekdayWed',
  'activity.weekdayThu',
  'activity.weekdayFri',
  'activity.weekdaySat',
]

// --- UTC date helpers (server timestamps are RFC3339 Z) ---
const startOfDay = (ms: number) => {
  const d = new Date(ms)
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate())
}
const startOfWeek = (ms: number) => startOfDay(ms) - new Date(startOfDay(ms)).getUTCDay() * DAY_MS
const startOfMonth = (ms: number) => {
  const d = new Date(ms)
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1)
}
const startOfYear = (ms: number) => Date.UTC(new Date(ms).getUTCFullYear(), 0, 1)

function windowStartOf(gran: Gran, ms: number): number {
  switch (gran) {
    case 'week':
      return startOfWeek(ms)
    case 'month':
      return startOfMonth(ms)
    case 'year':
      return startOfYear(ms)
    default:
      return startOfDay(ms)
  }
}

// addWindows steps a window start by n whole windows (handles month/year length).
function addWindows(gran: Gran, ms: number, n: number): number {
  const d = new Date(ms)
  switch (gran) {
    case 'day':
      return ms + n * DAY_MS
    case 'week':
      return ms + n * 7 * DAY_MS
    case 'month':
      return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + n, 1)
    default:
      return Date.UTC(d.getUTCFullYear() + n, 0, 1)
  }
}

function windowLabel(gran: Gran, start: number): string {
  const d = new Date(start)
  switch (gran) {
    case 'day':
      return d.toLocaleDateString(undefined, {
        weekday: 'long',
        month: 'long',
        day: 'numeric',
        year: 'numeric',
        timeZone: 'UTC',
      })
    case 'week': {
      const a = new Date(start).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })
      const b = new Date(start + 6 * DAY_MS).toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
        timeZone: 'UTC',
      })
      return `${a} – ${b}`
    }
    case 'month':
      return d.toLocaleDateString(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' })
    default:
      return String(d.getUTCFullYear())
  }
}

const fmtHour = (t: TFunction, h: number) =>
  h === 0
    ? t('activity.hour12am')
    : h < 12
      ? t('activity.hourAm', { hour: h })
      : h === 12
        ? t('activity.hour12pm')
        : t('activity.hourPm', { hour: h - 12 })

type Cell = { col: number; row: number; count: number }
type Built = {
  cells: Cell[]
  maxCount: number
  total: number
  top: { col: number; text: string }[]
  left: { row: number; text: string }[]
}

// buildWindow sub-buckets the events that fall inside [winStart, next window)
// and lays them out for the granularity: day → 24 hours in a row; week → 7 days
// in a row; month → calendar (week column × weekday row); year → 12 months.
function buildWindow(t: TFunction, timestamps: number[], gran: Gran, winStart: number): Built {
  const winEnd = addWindows(gran, winStart, 1)
  const counts = new Map<number, number>()
  const keyOf = (t: number): number => {
    switch (gran) {
      case 'day':
        return winStart + Math.floor((t - winStart) / HOUR_MS) * HOUR_MS
      default:
        return startOfDay(t)
    }
  }
  for (const t of timestamps) {
    if (t < winStart || t >= winEnd) continue
    const k = keyOf(t)
    counts.set(k, (counts.get(k) ?? 0) + 1)
  }

  const cells: Cell[] = []
  const top: { col: number; text: string }[] = []
  const left: { row: number; text: string }[] = []

  if (gran === 'day') {
    for (let h = 0; h < 24; h++) cells.push({ col: h, row: 0, count: counts.get(winStart + h * HOUR_MS) ?? 0 })
    for (const h of [0, 6, 12, 18]) top.push({ col: h, text: fmtHour(t, h) })
  } else if (gran === 'week') {
    for (let i = 0; i < 7; i++) cells.push({ col: i, row: 0, count: counts.get(winStart + i * DAY_MS) ?? 0 })
    for (let i = 0; i < 7; i++) top.push({ col: i, text: t(WEEKDAY_KEYS[i]) })
  } else if (gran === 'month') {
    const base = startOfWeek(winStart)
    for (let dms = winStart; dms < winEnd; dms += DAY_MS) {
      const col = Math.round((startOfWeek(dms) - base) / (7 * DAY_MS))
      const row = new Date(dms).getUTCDay()
      cells.push({ col, row, count: counts.get(startOfDay(dms)) ?? 0 })
    }
    for (const row of [1, 3, 5]) left.push({ row, text: t(WEEKDAY_KEYS[row]) })
  } else {
    // year: full day calendar (week column × weekday row), GitHub-style, with a
    // month label where each month begins along the top.
    const base = startOfWeek(winStart)
    let lastMonth = -1
    for (let dms = winStart; dms < winEnd; dms += DAY_MS) {
      const col = Math.round((startOfWeek(dms) - base) / (7 * DAY_MS))
      const d = new Date(dms)
      cells.push({ col, row: d.getUTCDay(), count: counts.get(startOfDay(dms)) ?? 0 })
      const m = d.getUTCMonth()
      if (m !== lastMonth) {
        top.push({ col, text: t(MONTH_KEYS[m]) })
        lastMonth = m
      }
    }
    for (const row of [1, 3, 5]) left.push({ row, text: t(WEEKDAY_KEYS[row]) })
  }

  let maxCount = 0
  let total = 0
  for (const c of cells) {
    maxCount = Math.max(maxCount, c.count)
    total += c.count
  }
  return { cells, maxCount, total, top, left }
}

const fmtDate = (s?: string) =>
  s ? new Date(s).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' }) : '—'

// In-sentence (lowercase) window nouns for the "changes in this …" caption.
const GRAN_KEY: Record<Gran, string> = {
  day: 'activity.granDay',
  week: 'activity.granWeek',
  month: 'activity.granMonth',
  year: 'activity.granYear',
}

export default function ActivityHeatmap({
  report,
  childCount,
  rollup,
  scale = 1,
}: {
  report: ActivityReport
  childCount?: number
  // When the design has children, the tab passes a roll-up control. `loading`
  // shows a spinner over the chart while child activity is being merged in.
  rollup?: { checked: boolean; loading: boolean; onChange: (next: boolean) => void }
  // Uniformly scales the rendered isometric grid (viewBox unchanged, so the
  // aspect ratio is preserved). The dashboard passes <1 for a shorter chart.
  scale?: number
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const [gran, setGran] = useState<Gran>('year')

  // The isometric grid has a fixed aspect ratio (its viewBox), so to fill the
  // pane's vertical space we measure the chart area's height and scale the SVG
  // to it, letting the width grow proportionally and scroll horizontally.
  // Where the host has no definite height (e.g. a dashboard widget), the chart
  // box's minHeight pins a sensible size instead — the measurement settles
  // there rather than collapsing.
  const chartRef = useRef<HTMLDivElement | null>(null)
  const [availH, setAvailH] = useState(0)
  useEffect(() => {
    const el = chartRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      const h = entries[0]?.contentRect.height ?? 0
      setAvailH((prev) => (Math.abs(prev - h) > 0.5 ? h : prev))
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const createdMs = report.createdOn ? Date.parse(report.createdOn) : Date.now()
  const lastMs = report.lastChange ? Date.parse(report.lastChange) : Date.now()
  // Anchor a point in time; the visible window is the window containing it,
  // clamped to the design's activity span. Start on the most recent activity.
  const [anchorMs, setAnchorMs] = useState(lastMs)

  const minWin = windowStartOf(gran, createdMs)
  const maxWin = windowStartOf(gran, lastMs)
  const winStart = Math.min(maxWin, Math.max(minWin, windowStartOf(gran, anchorMs)))

  const timestamps = useMemo(
    () =>
      report.events
        .map((e) => (e.timestamp ? Date.parse(e.timestamp) : NaN))
        .filter((n) => !Number.isNaN(n)),
    [report.events],
  )

  // Recent-change tallies for the totals caption — window-independent, always
  // relative to now: "since yesterday" counts from the start of yesterday
  // (UTC, matching the rest of this component's day boundaries); "this week"
  // from the start of the current week.
  const recent = useMemo(() => {
    const now = Date.now()
    const yesterdayStart = startOfDay(now) - DAY_MS
    const weekStart = startOfWeek(now)
    let sinceYesterday = 0
    let thisWeek = 0
    for (const t of timestamps) {
      if (t >= yesterdayStart) sinceYesterday++
      if (t >= weekStart) thisWeek++
    }
    return { sinceYesterday, thisWeek }
  }, [timestamps])

  const { cells, maxCount, total, top, left } = useMemo(
    () => buildWindow(t, timestamps, gran, winStart),
    [t, timestamps, gran, winStart],
  )

  const accent = theme.palette.primary.main
  const empty = alpha(theme.palette.text.primary, theme.palette.mode === 'dark' ? 0.1 : 0.07)
  const ramp = useMemo(
    () => [empty, lighten(accent, 0.55), lighten(accent, 0.37), lighten(accent, 0.18), accent],
    [accent, empty],
  )

  // Per-window change totals across the whole activity span — the data for the
  // clickable timeline. Each dot is one increment, coloured by the same ramp.
  const windows = useMemo(() => {
    const out: { start: number; total: number }[] = []
    for (let w = minWin; w <= maxWin; w = addWindows(gran, w, 1)) {
      const end = addWindows(gran, w, 1)
      let n = 0
      for (const t of timestamps) if (t >= w && t < end) n++
      out.push({ start: w, total: n })
    }
    return out
  }, [timestamps, gran, minWin, maxWin])
  const maxWindowTotal = windows.reduce((m, w) => Math.max(m, w.total), 0)
  const dotLevel = (n: number) =>
    n <= 0 || maxWindowTotal <= 0 ? 0 : Math.max(1, Math.min(4, Math.ceil((n / maxWindowTotal) * 4)))

  const TW = gran === 'day' ? 14 : gran === 'week' ? 28 : gran === 'month' ? 18 : 13
  const TH = TW / 2
  const MAX_BAR = TW * 2.2
  const levelOf = (count: number) =>
    count <= 0 || maxCount <= 0 ? 0 : Math.max(1, Math.min(4, Math.ceil((count / maxCount) * 4)))
  const heightOf = (count: number) =>
    count <= 0 || maxCount <= 0 ? 0 : Math.max(TW * 0.28, (count / maxCount) * MAX_BAR)

  const svg = useMemo(() => {
    if (cells.length === 0) return null
    let minX = Infinity
    let maxX = -Infinity
    let minY = Infinity
    let maxY = -Infinity
    const note = (x: number, y: number) => {
      minX = Math.min(minX, x)
      maxX = Math.max(maxX, x)
      minY = Math.min(minY, y)
      maxY = Math.max(maxY, y)
    }
    const pts = (arr: number[][]) => arr.map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ')
    const proj = (col: number, row: number) => ({ cx: (col - row) * (TW / 2), cy: (col + row) * (TH / 2) })

    const ordered = [...cells].sort((a, b) => a.col + a.row - (b.col + b.row) || a.row - b.row)
    const faces: React.ReactNode[] = []

    ordered.forEach((c, i) => {
      const { cx, cy } = proj(c.col, c.row)
      const h = heightOf(c.count)
      const topColor = ramp[levelOf(c.count)]
      const stroke = alpha(theme.palette.background.paper, 0.55)

      const T = [cx, cy - TH / 2]
      const R = [cx + TW / 2, cy]
      const B = [cx, cy + TH / 2]
      const L = [cx - TW / 2, cy]
      const Tt = [cx, cy - TH / 2 - h]
      const Rt = [cx + TW / 2, cy - h]
      const Bt = [cx, cy + TH / 2 - h]
      const Lt = [cx - TW / 2, cy - h]
      ;[T, R, B, L, Tt, Rt, Bt, Lt].forEach((p) => note(p[0], p[1]))

      if (h === 0) {
        faces.push(<polygon key={i} points={pts([T, R, B, L])} fill={topColor} stroke={stroke} strokeWidth={0.5} />)
        return
      }
      faces.push(
        <g key={i}>
          <polygon points={pts([L, B, Bt, Lt])} fill={darken(topColor, 0.14)} stroke={stroke} strokeWidth={0.5} />
          <polygon points={pts([B, R, Rt, Bt])} fill={darken(topColor, 0.28)} stroke={stroke} strokeWidth={0.5} />
          <polygon points={pts([Tt, Rt, Bt, Lt])} fill={topColor} stroke={stroke} strokeWidth={0.5} />
        </g>,
      )
    })

    // Sparse axis labels.
    const fontSize = Math.max(7, TW * 0.42)
    const labelColor = theme.palette.text.secondary
    const labelNodes: React.ReactNode[] = []
    const pushLabel = (x: number, y: number, text: string, anchor: 'start' | 'middle' | 'end') => {
      note(x, y)
      note(anchor === 'end' ? x - text.length * fontSize * 0.6 : x + text.length * fontSize * 0.6, y)
      labelNodes.push(
        <text
          key={`l${labelNodes.length}`}
          x={x.toFixed(1)}
          y={y.toFixed(1)}
          fontSize={fontSize}
          textAnchor={anchor}
          fill={labelColor}
          style={{ userSelect: 'none' }}
        >
          {text}
        </text>,
      )
    }
    for (const t of top) {
      const { cx, cy } = proj(t.col, 0)
      pushLabel(cx, cy - TH / 2 - fontSize * 0.6, t.text, 'middle')
    }
    for (const l of left) {
      const { cx, cy } = proj(0, l.row)
      pushLabel(cx - TW * 0.7, cy + fontSize * 0.35, l.text, 'end')
    }

    const pad = TW
    const w = maxX - minX + pad * 2
    const hgt = maxY - minY + pad * 2
    // Keep the rendered size between 800 and 1200 px: scale the larger dimension
    // up to MIN if the grid is small, or down to MAX if it's large, preserving
    // aspect ratio (the viewBox keeps the iso coordinate space intact).
    const MIN = 800
    const MAX = 1200
    const larger = Math.max(w, hgt)
    const scale = larger > MAX ? MAX / larger : larger < MIN ? MIN / larger : 1
    return {
      faces,
      labels: labelNodes,
      viewBox: `${minX - pad} ${minY - pad} ${w} ${hgt}`,
      w: w * scale,
      hgt: hgt * scale,
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cells, gran, ramp, maxCount, theme.palette.background.paper, theme.palette.text.secondary])

  const aspect = svg ? svg.w / svg.hgt : 1
  // Height the SVG fills: the measured chart area (once observed), else its
  // natural height for the first paint. `scale` lets the dashboard render a
  // shorter chart.
  const chartH = (availH > 0 ? availH : svg ? svg.hgt : 0) * scale
  const chartW = chartH * aspect

  return (
    <Stack spacing={1} sx={{ height: '100%', minHeight: 0 }}>
      {/* Current window + its change count (left) beside the granularity toggle. */}
      <Stack direction="row" justifyContent="space-between" alignItems="center" flexWrap="wrap" gap={1}>
        <Typography variant="body2">
          <b>{windowLabel(gran, winStart)}</b>
          <Box component="span" sx={{ color: 'text.secondary' }}>
            {' · '}
            {t('activity.changesInWindow', { count: total, window: t(GRAN_KEY[gran]) })}
          </Box>
        </Typography>
        <ToggleButtonGroup size="small" exclusive value={gran} onChange={(_, v: Gran | null) => v && setGran(v)}>
          <ToggleButton value="day">{t('activity.day')}</ToggleButton>
          <ToggleButton value="week">{t('activity.week')}</ToggleButton>
          <ToggleButton value="month">{t('activity.month')}</ToggleButton>
          <ToggleButton value="year">{t('activity.year')}</ToggleButton>
        </ToggleButtonGroup>
      </Stack>

      {/* All-time totals plus recent-change tallies, as a compact caption. */}
      <Typography variant="caption" color="text.secondary">
        <Trans t={t} i18nKey="activity.statVersions" count={report.versionCount} components={{ b: <b /> }} />
        {' · '}
        <Trans t={t} i18nKey="activity.statContributors" count={report.contributorCount} components={{ b: <b /> }} />
        {' · '}
        {t('activity.statTotalChanges', { count: report.totalEvents })}
        {' · '}
        <Trans t={t} i18nKey="activity.statSinceYesterday" count={recent.sinceYesterday} components={{ b: <b /> }} />
        {' · '}
        <Trans t={t} i18nKey="activity.statThisWeek" count={recent.thisWeek} components={{ b: <b /> }} />
        {childCount ? (
          <>
            {' · '}
            <Trans t={t} i18nKey="activity.statChildDocuments" count={childCount} components={{ b: <b /> }} />
          </>
        ) : null}
      </Typography>

      {/* Roll-up toggle (only when the design has children). */}
      {rollup && (
        <FormControlLabel
          sx={{ m: 0 }}
          control={
            <Checkbox
              size="small"
              checked={rollup.checked}
              onChange={(e) => rollup.onChange(e.target.checked)}
            />
          }
          label={<Typography variant="body2">{t('activity.rollupChildChanges')}</Typography>}
        />
      )}

      {rollup?.loading ? (
        <Stack alignItems="center" justifyContent="center" spacing={1} sx={{ py: 6 }}>
          <CircularProgress />
          <Typography variant="caption" color="text.secondary">
            {t('activity.rollingUpChildActivity')}
          </Typography>
        </Stack>
      ) : (
        <>
      {/* Window timeline: one dot per increment, coloured by its change count
          (same ramp as the heat map). Click a dot to load that window. */}
      <Box sx={{ overflowX: 'auto', py: 0.5 }}>
        <Stack
          direction="row"
          alignItems="center"
          spacing={0.5}
          sx={{ position: 'relative', minWidth: 'min-content', px: 1 }}
        >
          <Box
            sx={{ position: 'absolute', left: 8, right: 8, top: '50%', height: 2, bgcolor: 'divider', zIndex: 0 }}
          />
          {windows.map((wd) => {
            const selected = wd.start === winStart
            return (
              <Tooltip
                key={wd.start}
                title={t('activity.windowTooltip', { window: windowLabel(gran, wd.start), count: wd.total })}
              >
                <Box
                  component="button"
                  onClick={() => setAnchorMs(wd.start)}
                  aria-label={windowLabel(gran, wd.start)}
                  sx={{
                    zIndex: 1,
                    p: 0,
                    m: 0,
                    border: 0,
                    background: 'transparent',
                    cursor: 'pointer',
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    width: 20,
                    height: 22,
                  }}
                >
                  <Box
                    sx={{
                      width: selected ? 16 : 11,
                      height: selected ? 16 : 11,
                      borderRadius: '50%',
                      bgcolor: ramp[dotLevel(wd.total)],
                      border: selected ? 2 : 1,
                      borderColor: selected ? 'primary.main' : 'divider',
                      boxShadow: selected ? 1 : 0,
                      transition: 'all .1s',
                    }}
                  />
                </Box>
              </Tooltip>
            )
          })}
        </Stack>
      </Box>

      {/* Isometric grid: fills the pane's remaining height (aspect preserved),
          scrolling horizontally when the proportional width overflows. */}
      <Box
        ref={chartRef}
        sx={{
          // flex:1 fills a definite-height pane; minHeight keeps a sensible
          // size where the host is content-sized (dashboard widget).
          flex: 1,
          minHeight: 160,
          overflowX: 'auto',
          overflowY: 'hidden',
          py: 1,
        }}
      >
        {svg && chartH > 0 && (
          <svg
            viewBox={svg.viewBox}
            width={chartW}
            height={chartH}
            style={{ maxWidth: 'none', display: 'block', margin: '0 auto', shapeRendering: 'geometricPrecision' }}
          >
            {svg.faces}
            {svg.labels}
          </svg>
        )}
      </Box>

      {/* Legend + span */}
      <Stack direction="row" spacing={0.75} alignItems="center" justifyContent="space-between" flexWrap="wrap" gap={1}>
        <Stack direction="row" spacing={0.75} alignItems="center">
          <Typography variant="caption" color="text.secondary">
            {t('activity.less')}
          </Typography>
          {ramp.map((c, i) => (
            <Box key={i} sx={{ width: 12, height: 12, bgcolor: c, borderRadius: 0.5 }} />
          ))}
          <Typography variant="caption" color="text.secondary">
            {t('activity.more')}
          </Typography>
        </Stack>
        <Typography variant="caption" color="text.secondary">
          {fmtDate(report.createdOn)} → {fmtDate(report.lastChange)}
        </Typography>
      </Stack>
        </>
      )}
    </Stack>
  )
}
