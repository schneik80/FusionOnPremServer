import { Box, Chip, Stack, Typography } from '@mui/material'
import { lighten, useTheme } from '@mui/material/styles'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { HubOverview } from '../../api/types'
import ActivityHeatmap from '../ActivityHeatmap'
import { Hint } from '../dashboard/shell'
import { synthReport } from './pulseReport'

const CYCLE_MS = 4200

// HubPulse is the hero band: the app's isometric activity heat map, driven by
// LOCAL collaboration events (task/batch/chat), auto-cycling the hub roll-up
// and the busiest projects. Hover to pause; click a chip to pin one. It reuses
// ActivityHeatmap (hideStats, since the data is not design versions) so the
// grid, granularity toggle and window timeline match the rest of the app.
export default function HubPulse({ overview }: { overview: HubOverview }) {
  const { t } = useTranslation('details')
  const theme = useTheme()

  // Series 0 is the hub roll-up ("All projects"); the rest are the most active
  // projects (already sorted + capped server-side).
  const series = useMemo(() => {
    const all = {
      id: 'all',
      name: t('dashboards.pulseAllProjects'),
      days: overview.pulse,
      total: overview.pulse.reduce((s, d) => s + d.count, 0),
    }
    const projects = overview.projects.map((p) => ({
      id: p.projectId || p.projectName,
      name: p.projectName,
      days: p.days,
      total: p.total,
    }))
    return [all, ...projects]
  }, [overview, t])

  const [idx, setIdx] = useState(0)
  const [paused, setPaused] = useState(false)
  const safeIdx = Math.min(idx, series.length - 1)
  const active = series[safeIdx]

  useEffect(() => {
    if (paused || series.length <= 1) return
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return
    const h = window.setInterval(() => setIdx((i) => (i + 1) % series.length), CYCLE_MS)
    return () => window.clearInterval(h)
  }, [paused, series.length])

  const report = useMemo(() => synthReport(active.name, active.days), [active])

  const ramp = useMemo(() => {
    const accent = theme.palette.primary.main
    const empty = theme.palette.mode === 'dark' ? 'rgba(255,255,255,0.10)' : 'rgba(0,0,0,0.07)'
    return [empty, lighten(accent, 0.55), lighten(accent, 0.37), lighten(accent, 0.18), accent]
  }, [theme])

  // Nothing recorded anywhere yet.
  if (active.total === 0 && series.length === 1) {
    return <Hint>{t('dashboards.pulseEmpty')}</Hint>
  }

  return (
    <Box onMouseEnter={() => setPaused(true)} onMouseLeave={() => setPaused(false)}>
      <Stack direction="row" justifyContent="space-between" alignItems="baseline" flexWrap="wrap" gap={1} sx={{ mb: 0.5 }}>
        <Typography variant="subtitle2" noWrap sx={{ fontWeight: 600, minWidth: 0 }} title={active.name}>
          {active.name}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          {t('dashboards.pulseChanges', { count: active.total, days: overview.windowDays })}
        </Typography>
      </Stack>

      {/* Fixed height so the heat map measures a stable size in this scrolling
          (non-fill) pane; it scrolls horizontally on its own when wide. Keyed by
          the selection so its window re-anchors to each series on cycle. */}
      <Box sx={{ height: 240 }}>
        <ActivityHeatmap key={active.id} report={report} hideStats />
      </Box>

      <Stack direction="row" alignItems="center" spacing={0.75} sx={{ mt: 0.5, color: 'text.secondary' }}>
        <Typography variant="caption">{t('activity.less')}</Typography>
        {ramp.map((c, i) => (
          <Box key={i} sx={{ width: 12, height: 12, borderRadius: '2px', bgcolor: c }} />
        ))}
        <Typography variant="caption">{t('activity.more')}</Typography>
      </Stack>

      {series.length > 1 && (
        <Stack direction="row" spacing={0.5} useFlexGap flexWrap="wrap" sx={{ mt: 1 }}>
          {series.map((s, i) => (
            <Chip
              key={s.id}
              size="small"
              label={s.name}
              onClick={() => setIdx(i)}
              variant={i === safeIdx ? 'filled' : 'outlined'}
              color={i === safeIdx ? 'primary' : 'default'}
              sx={{ maxWidth: 180 }}
            />
          ))}
        </Stack>
      )}
    </Box>
  )
}
