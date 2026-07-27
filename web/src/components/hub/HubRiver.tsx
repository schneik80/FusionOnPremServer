import { Box } from '@mui/material'
import { useTheme, type Theme } from '@mui/material/styles'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { HubOverview } from '../../api/types'
import type { Task } from '../../tasks/types'
import type { Job } from '../../production/types'
import { Hint } from '../dashboard/shell'
import { useReducedMotion } from './useReducedMotion'

const DAY = 86400000
const W = 960
const PAD_L = 132
const PAD_R = 28
const TOP = 28
const LANE_GAP = 46

type Dot =
  | { t: 'task'; ms: number; milestone: boolean; overdue: boolean; status: string }
  | { t: 'batch'; ms: number; kind: string; status: string }
  | { t: 'day'; ms: number; count: number }

interface Lane {
  key: string
  label: string
  color: string
  dots: Dot[]
}

// HubRiver braids the hub's recent events into one horizontal timeline: your
// tasks (with milestone ◇ and overdue markers) and production batches on their
// own lanes, plus the hub-wide message and activity pulse as per-day dots
// sized by volume. Same lane grammar as HistoryGraph. A playhead sweeps "now"
// as ambient motion (off under reduced-motion). Uses only data already
// fetched — no extra requests.
export default function HubRiver({
  overview,
  myTasks,
  myProduction,
}: {
  overview: HubOverview
  myTasks: Task[]
  myProduction: Job[]
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const reduced = useReducedMotion()

  const now = Date.now()
  const startMs = now - overview.windowDays * DAY
  const inWin = (ms: number) => !Number.isNaN(ms) && ms >= startMs && ms <= now + DAY

  const lanes: Lane[] = useMemo(() => {
    const taskDots: Dot[] = []
    for (const task of myTasks) {
      const ms = Date.parse(task.createdAt)
      if (!inWin(ms)) continue
      const dueMs = task.dueDate ? Date.parse(task.dueDate) : task.endDate ? Date.parse(task.endDate) : NaN
      taskDots.push({
        t: 'task',
        ms,
        milestone: !!task.milestone,
        overdue: task.status !== 'done' && !Number.isNaN(dueMs) && dueMs < now,
        status: task.status,
      })
    }

    const batchDots: Dot[] = []
    for (const job of myProduction) {
      for (const b of job.batches) {
        const ms = Date.parse(b.runAt)
        if (!inWin(ms)) continue
        batchDots.push({ t: 'batch', ms, kind: b.kind, status: b.status })
      }
    }

    const dayDots = (days: { day: string; count: number }[]): Dot[] =>
      days
        .map((d) => ({ t: 'day' as const, ms: Date.parse(`${d.day}T12:00:00Z`), count: d.count }))
        .filter((d) => d.count > 0 && inWin(d.ms))

    return [
      { key: 'tasks', label: t('dashboards.riverTasks'), color: theme.palette.info.main, dots: taskDots },
      { key: 'batches', label: t('dashboards.riverBatches'), color: theme.palette.success.main, dots: batchDots },
      { key: 'messages', label: t('dashboards.riverMessages'), color: theme.palette.text.secondary, dots: dayDots(overview.chat.days) },
      { key: 'activity', label: t('dashboards.riverActivity'), color: theme.palette.primary.main, dots: dayDots(overview.pulse) },
    ]
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [overview, myTasks, myProduction, t, theme])

  const total = lanes.reduce((s, l) => s + l.dots.length, 0)
  if (total === 0) return <Hint>{t('dashboards.riverEmpty')}</Hint>

  const lanesBottom = TOP + (lanes.length - 1) * LANE_GAP
  const H = lanesBottom + 56
  const span = Math.max(1, now - startMs)
  const xFor = (ms: number) => PAD_L + Math.max(0, Math.min(1, (ms - startMs) / span)) * (W - PAD_L - PAD_R)
  const laneY = (i: number) => TOP + i * LANE_GAP
  const ring = theme.palette.background.paper
  const accent = theme.palette.primary.main
  const muted = theme.palette.text.secondary

  const maxDay = Math.max(1, ...lanes.flatMap((l) => l.dots).filter((d): d is Dot & { t: 'day' } => d.t === 'day').map((d) => d.count))
  const dayR = (count: number) => 3 + Math.sqrt(count / maxDay) * 5

  // ~6 evenly spaced date ticks across the window.
  const ticks = Array.from({ length: 6 }, (_, i) => startMs + (span * i) / 5)
  const fmtTick = (ms: number) => new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })

  return (
    <Box sx={{ flex: 1, minHeight: 300, display: 'flex' }}>
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height="100%" preserveAspectRatio="xMidYMid meet" role="img" aria-label={t('dashboards.riverSubtitle')}>
        {/* date grid */}
        {ticks.map((ms, i) => {
          const x = xFor(ms)
          return (
            <g key={`t${i}`}>
              <line x1={x} y1={TOP - 12} x2={x} y2={lanesBottom + 12} stroke={theme.palette.divider} strokeOpacity={0.4} />
              <text
                x={x}
                y={lanesBottom + 30}
                textAnchor="end"
                fontSize={9}
                fill={muted}
                transform={`rotate(-35 ${x} ${lanesBottom + 30})`}
              >
                {fmtTick(ms)}
              </text>
            </g>
          )
        })}

        {/* lanes */}
        {lanes.map((lane, i) => {
          const y = laneY(i)
          return (
            <g key={lane.key}>
              <line x1={PAD_L} y1={y} x2={W - PAD_R} y2={y} stroke={lane.color} strokeOpacity={0.4} strokeWidth={3} strokeLinecap="round" />
              <text x={PAD_L - 12} y={y + 3.5} textAnchor="end" fontSize={11} fill={theme.palette.text.secondary}>
                {lane.label}
              </text>
              {lane.dots.map((d, j) => {
                const x = xFor(d.ms)
                if (d.t === 'task') {
                  if (d.milestone) {
                    const r = 6
                    return <polygon key={j} points={`${x},${y - r} ${x + r},${y} ${x},${y + r} ${x - r},${y}`} fill={accent} stroke={ring} strokeWidth={1.5} />
                  }
                  const c = d.overdue ? theme.palette.error.main : taskColorFor(theme, d.status)
                  return <circle key={j} cx={x} cy={y} r={4.5} fill={c} stroke={ring} strokeWidth={1.5} />
                }
                if (d.t === 'batch') {
                  const c = batchColorFor(theme, d.status)
                  const s = 9
                  return (
                    <rect
                      key={j}
                      x={x - s / 2}
                      y={y - s / 2}
                      width={s}
                      height={s}
                      rx={2}
                      fill={d.kind === 'prove' ? 'none' : c}
                      stroke={c}
                      strokeWidth={2}
                    />
                  )
                }
                return <circle key={j} cx={x} cy={y} r={dayR(d.count)} fill={lane.color} fillOpacity={0.75} stroke={ring} strokeWidth={1} />
              })}
            </g>
          )
        })}

        {/* ambient playhead sweeping "now" (off under reduced motion) */}
        <line x1={PAD_L} x2={PAD_L} y1={TOP - 10} y2={lanesBottom + 10} stroke={accent} strokeOpacity={0.45} strokeWidth={1.5}>
          {!reduced && (
            <>
              <animate attributeName="x1" values={`${PAD_L};${W - PAD_R}`} dur="11s" repeatCount="indefinite" />
              <animate attributeName="x2" values={`${PAD_L};${W - PAD_R}`} dur="11s" repeatCount="indefinite" />
              <animate attributeName="stroke-opacity" values="0;0.45;0" dur="11s" repeatCount="indefinite" />
            </>
          )}
        </line>
      </svg>
    </Box>
  )
}

function taskColorFor(theme: Theme, status: string): string {
  if (status === 'inprogress') return theme.palette.info.main
  if (status === 'blocked') return theme.palette.warning.main
  if (status === 'done') return theme.palette.success.main
  return theme.palette.text.secondary
}

function batchColorFor(theme: Theme, status: string): string {
  if (status === 'running') return theme.palette.info.main
  if (status === 'complete') return theme.palette.success.main
  return theme.palette.text.secondary
}
