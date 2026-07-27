import { Box, IconButton, Stack, ToggleButton, ToggleButtonGroup, Tooltip } from '@mui/material'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faDiagramProject, faPause, faPlay, faStream, faTableColumns } from '@fortawesome/free-solid-svg-icons'
import { Suspense, lazy, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useHubOverview, useMyProduction, useMyTasks, useProjects } from '../../api/queries'
import type { HubOverview } from '../../api/types'
import { useNav } from '../../state/nav'
import type { Task } from '../../tasks/types'
import { DashboardShell, Hint, WidgetCard, spinner } from '../dashboard/shell'
import HubPulse from './HubPulse'
import { ChatTrafficWidget, ContributorsWidget, PipelineWidget, ScheduleWidget } from './widgets'

// The two full-canvas pivots are lazy-loaded so the default Overview never pays
// for their pan/zoom + timeline code.
const HubConstellation = lazy(() => import('./HubConstellation'))
const HubRiver = lazy(() => import('./HubRiver'))

type Mode = 'overview' | 'explore' | 'river'
const MODES: Mode[] = ['overview', 'explore', 'river']
const TOUR_MS = 30_000

// HubDashboard is the hub landing pane. Three pivots share one shell: Overview
// (pulse hero + mission-control widgets), Explore (pan/zoom project star map),
// and River (a braided timeline of recent events). A play/stop "tour" auto-
// cycles the pivots on a 30s loop for an ambient, kiosk-style view. Data comes
// from one /api/hub/overview call plus the cross-project /mine feeds.
export function HubDashboard() {
  const { t } = useTranslation('details')
  const nav = useNav()
  const [mode, setMode] = useState<Mode>('overview')
  const [playing, setPlaying] = useState(false)

  const overviewQ = useHubOverview(nav.hubId)
  const projectsQ = useProjects(nav.hubId)
  const myTasksQ = useMyTasks(true)
  const myProductionQ = useMyProduction(mode === 'river')
  const ov = overviewQ.data

  // Tour: advance to the next pivot every TOUR_MS, looping. Keyed on
  // [playing, mode] so each pivot — whether reached by the tour or a manual
  // click — gets its full dwell before the next hop; stopping cancels it.
  useEffect(() => {
    if (!playing) return
    const h = window.setTimeout(() => {
      setMode((m) => MODES[(MODES.indexOf(m) + 1) % MODES.length])
    }, TOUR_MS)
    return () => window.clearTimeout(h)
  }, [playing, mode])

  const isCanvas = mode !== 'overview'

  const action = (
    <Stack direction="row" spacing={0.75} alignItems="center">
      <Tooltip title={playing ? t('dashboards.stopTour') : t('dashboards.playTour')}>
        <IconButton
          size="small"
          color={playing ? 'primary' : 'default'}
          onClick={() => setPlaying((p) => !p)}
          aria-label={playing ? t('dashboards.stopTour') : t('dashboards.playTour')}
        >
          <FontAwesomeIcon icon={playing ? faPause : faPlay} style={{ fontSize: 13 }} />
        </IconButton>
      </Tooltip>
      <ToggleButtonGroup size="small" exclusive value={mode} onChange={(_, v: Mode | null) => v && setMode(v)}>
        <ToggleButton value="overview">
          <FontAwesomeIcon icon={faTableColumns} style={{ marginRight: 6 }} />
          {t('dashboards.viewOverview')}
        </ToggleButton>
        <ToggleButton value="explore">
          <FontAwesomeIcon icon={faDiagramProject} style={{ marginRight: 6 }} />
          {t('dashboards.viewExplore')}
        </ToggleButton>
        <ToggleButton value="river">
          <FontAwesomeIcon icon={faStream} style={{ marginRight: 6 }} />
          {t('dashboards.viewRiver')}
        </ToggleButton>
      </ToggleButtonGroup>
    </Stack>
  )

  const stats = [
    {
      label: t('dashboards.statProjects'),
      value: ov ? ov.projectCount : projectsQ.isLoading ? spinner : (projectsQ.data?.length ?? 0),
    },
    { label: t('dashboards.statOpenTasks'), value: ov ? ov.tasks.open : spinner },
    { label: t('dashboards.statRunningBatches'), value: ov ? ov.production.running : spinner },
    { label: t('dashboards.statMessages'), value: ov ? ov.chat.total : spinner },
  ]

  const subtitle =
    mode === 'explore'
      ? t('dashboards.exploreSubtitle')
      : mode === 'river'
        ? t('dashboards.riverSubtitle')
        : t('dashboards.overviewSubtitle')

  const canvasFallback = (
    <Hint>{overviewQ.isLoading || projectsQ.isLoading ? t('common:loading') : t('dashboards.overviewUnavailable')}</Hint>
  )

  return (
    <DashboardShell title={nav.hubName ?? t('dashboards.hub')} subtitle={subtitle} stats={stats} action={action} fill={isCanvas}>
      {mode === 'explore' ? (
        ov && projectsQ.data ? (
          <Suspense fallback={<Hint>{t('common:loading')}</Hint>}>
            <HubConstellation projects={projectsQ.data} overview={ov} onOpen={(item) => nav.selectProject(item)} />
          </Suspense>
        ) : (
          canvasFallback
        )
      ) : mode === 'river' ? (
        ov ? (
          <Suspense fallback={<Hint>{t('common:loading')}</Hint>}>
            <HubRiver overview={ov} myTasks={myTasksQ.data?.tasks ?? []} myProduction={myProductionQ.data?.jobs ?? []} />
          </Suspense>
        ) : (
          canvasFallback
        )
      ) : (
        <OverviewBody overview={ov} loading={overviewQ.isLoading} error={!!overviewQ.error} myTasks={myTasksQ.data?.tasks ?? []} />
      )}
    </DashboardShell>
  )
}

function OverviewBody({
  overview,
  loading,
  error,
  myTasks,
}: {
  overview: HubOverview | undefined
  loading: boolean
  error: boolean
  myTasks: Task[]
}) {
  const { t } = useTranslation('details')
  if (!overview) {
    if (loading) return <Hint>{t('common:loading')}</Hint>
    return <Hint>{error ? t('dashboards.overviewUnavailable') : t('dashboards.pulseEmpty')}</Hint>
  }
  return (
    <Stack spacing={1.5}>
      <WidgetCard title={t('dashboards.pulseTitle')}>
        <HubPulse overview={overview} />
      </WidgetCard>
      <Box sx={{ display: 'grid', gap: 1.5, gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))' }}>
        <WidgetCard title={t('dashboards.wSchedule')}>
          <ScheduleWidget tasks={myTasks} />
        </WidgetCard>
        <WidgetCard title={t('dashboards.wChatTraffic')}>
          <ChatTrafficWidget days={overview.chat.days} windowDays={overview.windowDays} total={overview.chat.total} />
        </WidgetCard>
        <WidgetCard title={t('dashboards.wContributors')}>
          <ContributorsWidget contributors={overview.contributors} />
        </WidgetCard>
        <WidgetCard title={t('dashboards.wPipeline')}>
          <PipelineWidget prod={overview.production} />
        </WidgetCard>
      </Box>
    </Stack>
  )
}
