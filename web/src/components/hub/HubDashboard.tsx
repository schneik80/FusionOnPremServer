import { Box, Stack, ToggleButton, ToggleButtonGroup } from '@mui/material'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faDiagramProject, faTableColumns } from '@fortawesome/free-solid-svg-icons'
import { Suspense, lazy, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useHubOverview, useMyTasks, useProjects } from '../../api/queries'
import type { HubOverview } from '../../api/types'
import { useNav } from '../../state/nav'
import type { Task } from '../../tasks/types'
import { DashboardShell, Hint, WidgetCard, spinner } from '../dashboard/shell'
import HubPulse from './HubPulse'
import { ChatTrafficWidget, ContributorsWidget, PipelineWidget, ScheduleWidget } from './widgets'

// The constellation carries the pan/zoom canvas; lazy-load it so the default
// Overview never pays for it.
const HubConstellation = lazy(() => import('./HubConstellation'))

type Mode = 'overview' | 'explore'

// HubDashboard is the hub landing pane. Two modes share one shell: Overview
// (the default — a collaboration pulse hero plus mission-control widgets) and
// Explore (an opt-in pan/zoom star map of the projects). KPIs and widgets come
// from one /api/hub/overview call (one upstream GetProjects, then local
// aggregation); "your schedule" reuses the existing cross-project /mine feed.
export function HubDashboard() {
  const { t } = useTranslation('details')
  const nav = useNav()
  const [mode, setMode] = useState<Mode>('overview')

  const overviewQ = useHubOverview(nav.hubId)
  const projectsQ = useProjects(nav.hubId)
  const myTasksQ = useMyTasks(true)
  const ov = overviewQ.data
  const isExplore = mode === 'explore'

  const toggle = (
    <ToggleButtonGroup size="small" exclusive value={mode} onChange={(_, v: Mode | null) => v && setMode(v)}>
      <ToggleButton value="overview">
        <FontAwesomeIcon icon={faTableColumns} style={{ marginRight: 6 }} />
        {t('dashboards.viewOverview')}
      </ToggleButton>
      <ToggleButton value="explore">
        <FontAwesomeIcon icon={faDiagramProject} style={{ marginRight: 6 }} />
        {t('dashboards.viewExplore')}
      </ToggleButton>
    </ToggleButtonGroup>
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

  return (
    <DashboardShell
      title={nav.hubName ?? t('dashboards.hub')}
      subtitle={isExplore ? t('dashboards.exploreSubtitle') : t('dashboards.overviewSubtitle')}
      stats={stats}
      action={toggle}
      fill={isExplore}
    >
      {isExplore ? (
        ov && projectsQ.data ? (
          <Suspense fallback={<Hint>{t('common:loading')}</Hint>}>
            <HubConstellation projects={projectsQ.data} overview={ov} onOpen={(item) => nav.selectProject(item)} />
          </Suspense>
        ) : (
          <Hint>
            {overviewQ.isLoading || projectsQ.isLoading ? t('common:loading') : t('dashboards.overviewUnavailable')}
          </Hint>
        )
      ) : (
        <OverviewBody
          overview={ov}
          loading={overviewQ.isLoading}
          error={!!overviewQ.error}
          myTasks={myTasksQ.data?.tasks ?? []}
        />
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
