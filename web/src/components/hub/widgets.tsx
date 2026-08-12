import { Box, Stack, Typography } from '@mui/material'
import UserAvatar from '../UserAvatar'
import { alpha, useTheme } from '@mui/material/styles'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { HubContributor, HubDayCount, HubProdStats } from '../../api/types'
import type { Task } from '../../tasks/types'
import { statusBarColor } from '../../tasks/gantt/GanttBar'
import { Hint } from '../dashboard/shell'

// Mission-control widgets for the hub Overview. Each takes already-fetched data
// and draws a small, hand-built visual (no chart library) in the app's theme
// colours. "Your schedule" is your own upcoming/overdue work (from /mine);
// traffic, contributors and pipeline are hub-wide aggregates.

const fmtDay = (d: Date): string => d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })

interface ScheduleRow {
  task: Task
  due: Date
  overdue: boolean
}

function scheduleEntries(tasks: Task[]): ScheduleRow[] {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const parse = (s?: string) => (s ? new Date(`${s}T00:00:00`) : null)
  const rows: ScheduleRow[] = []
  for (const task of tasks) {
    if (task.status === 'done') continue
    const due = parse(task.dueDate) ?? parse(task.endDate)
    if (!due || Number.isNaN(due.getTime())) continue
    rows.push({ task, due, overdue: due < today })
  }
  rows.sort((a, b) => a.due.getTime() - b.due.getTime())
  return rows
}

// ScheduleWidget: your next deadlines across the hub, overdue first.
export function ScheduleWidget({ tasks }: { tasks: Task[] }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const rows = useMemo(() => scheduleEntries(tasks), [tasks])
  if (rows.length === 0) return <Hint>{t('dashboards.scheduleEmpty')}</Hint>
  const shown = rows.slice(0, 6)
  return (
    <Stack spacing={0.75}>
      {shown.map(({ task, due, overdue }) => (
        <Box key={`${task.projectId}:${task.id}`} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <Box
            sx={{
              width: 8,
              height: 8,
              borderRadius: '50%',
              flexShrink: 0,
              bgcolor: overdue ? theme.palette.error.main : statusBarColor(theme, task.status),
            }}
          />
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography variant="body2" noWrap>
              {task.title}
            </Typography>
            <Typography variant="caption" color="text.secondary" noWrap sx={{ display: 'block' }}>
              {task.projectName}
            </Typography>
          </Box>
          <Typography
            variant="caption"
            sx={{ flexShrink: 0, color: overdue ? 'error.main' : 'text.secondary', fontWeight: overdue ? 600 : 400 }}
          >
            {overdue ? t('dashboards.scheduleOverdue', { date: fmtDay(due) }) : fmtDay(due)}
          </Typography>
        </Box>
      ))}
      {rows.length > shown.length && (
        <Typography variant="caption" color="text.secondary">
          {t('dashboards.scheduleMore', { count: rows.length - shown.length })}
        </Typography>
      )}
    </Stack>
  )
}

// densify turns the sparse per-day counts into a continuous windowDays-long
// series (UTC day keys, matching the server), so the sparkline spacing is true.
function densify(days: HubDayCount[], windowDays: number): number[] {
  const map = new Map(days.map((d) => [d.day, d.count]))
  const now = new Date()
  const startMs = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()) - (windowDays - 1) * 86400000
  const out: number[] = []
  for (let i = 0; i < windowDays; i++) {
    out.push(map.get(new Date(startMs + i * 86400000).toISOString().slice(0, 10)) ?? 0)
  }
  return out
}

// ChatTrafficWidget: messages per day across the hub, as an area sparkline.
export function ChatTrafficWidget({
  days,
  windowDays,
  total,
}: {
  days: HubDayCount[]
  windowDays: number
  total: number
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const series = useMemo(() => densify(days, windowDays), [days, windowDays])
  if (total === 0) return <Hint>{t('dashboards.chatEmpty')}</Hint>

  const W = 300
  const H = 84
  const max = Math.max(1, ...series)
  const step = series.length > 1 ? W / (series.length - 1) : W
  const pts = series.map((v, i) => `${(i * step).toFixed(1)},${(H - 5 - (v / max) * (H - 12)).toFixed(1)}`)
  const accent = theme.palette.primary.main
  return (
    <Stack spacing={0.5}>
      <Box sx={{ width: '100%' }}>
        <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="none" role="img" aria-hidden>
          <polyline points={`0,${H} ${pts.join(' ')} ${W},${H}`} fill={accent} fillOpacity={0.12} stroke="none" />
          <polyline
            points={pts.join(' ')}
            fill="none"
            stroke={accent}
            strokeWidth={1.75}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        </svg>
      </Box>
      <Typography variant="caption" color="text.secondary">
        {t('dashboards.chatTotal', { count: total, days: windowDays })}
      </Typography>
    </Stack>
  )
}

// ContributorsWidget: top local authors (tasks + jobs/batches created).
export function ContributorsWidget({ contributors }: { contributors: HubContributor[] }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  if (contributors.length === 0) return <Hint>{t('dashboards.contributorsEmpty')}</Hint>
  const max = Math.max(1, ...contributors.map((c) => c.count))
  const accent = theme.palette.primary.main
  return (
    <Stack spacing={0.75}>
      {contributors.map((c) => (
        <Box key={c.id || c.name} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <UserAvatar id={c.id} name={c.name} size={22} />
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography variant="body2" noWrap>
              {c.name}
            </Typography>
            <Box sx={{ height: 5, borderRadius: 3, bgcolor: alpha(theme.palette.text.primary, 0.08), mt: 0.25 }}>
              <Box sx={{ width: `${(c.count / max) * 100}%`, height: '100%', borderRadius: 3, bgcolor: accent }} />
            </Box>
          </Box>
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}
          >
            {c.count}
          </Typography>
        </Box>
      ))}
    </Stack>
  )
}

function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
      <Box sx={{ width: 9, height: 9, borderRadius: '2px', bgcolor: color }} />
      <Typography variant="caption" color="text.secondary">
        {label}
      </Typography>
    </Box>
  )
}

// PipelineWidget: batch runs by status as a proportional bar (planned neutral,
// running info, complete success — the app's status colour language).
export function PipelineWidget({ prod }: { prod: HubProdStats }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  if (prod.batches === 0) return <Hint>{t('dashboards.pipelineEmpty')}</Hint>
  const segs = [
    { key: 'planned', n: prod.planned, color: theme.palette.text.secondary },
    { key: 'running', n: prod.running, color: theme.palette.info.main },
    { key: 'complete', n: prod.complete, color: theme.palette.success.main },
  ].filter((s) => s.n > 0)
  return (
    <Stack spacing={1}>
      <Box sx={{ display: 'flex', height: 24, borderRadius: 1, overflow: 'hidden', gap: '2px' }}>
        {segs.map((s) => (
          <Box key={s.key} sx={{ flexGrow: s.n, flexBasis: 0, bgcolor: s.color, display: 'grid', placeItems: 'center' }}>
            <Typography variant="caption" sx={{ color: theme.palette.getContrastText(s.color), fontWeight: 600 }}>
              {s.n}
            </Typography>
          </Box>
        ))}
      </Box>
      <Stack direction="row" spacing={1.5} flexWrap="wrap" useFlexGap>
        <LegendDot color={theme.palette.text.secondary} label={t('dashboards.pipelinePlanned', { count: prod.planned })} />
        <LegendDot color={theme.palette.info.main} label={t('dashboards.pipelineRunning', { count: prod.running })} />
        <LegendDot color={theme.palette.success.main} label={t('dashboards.pipelineComplete', { count: prod.complete })} />
      </Stack>
      <Typography variant="caption" color="text.secondary">
        {t('dashboards.pipelineJobs', { count: prod.jobs })}
      </Typography>
    </Stack>
  )
}
