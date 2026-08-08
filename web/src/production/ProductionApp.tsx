import { Box } from '@mui/material'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useJobs } from '../api/queries'
import { useNav } from '../state/nav'
import { JobDetail } from './JobDetail'
import { JobList } from './JobList'

// ProductionApp is the project-tab job & batch tracker (WikiApp/ChatApp
// contract: `active` gates fetching to the visible tab). Master/detail: a rail
// of jobs on the left, the selected job's flow on the right. P1 renders the
// job's steps as a plain list; the interactive flow canvas lands in P2.
export function ProductionApp({ active = true }: { active?: boolean }) {
  const { t } = useTranslation('production')
  const nav = useNav()
  const projectId = nav.project?.id ?? null
  const jobsQ = useJobs(projectId, active)

  const [selectedId, setSelectedId] = useState<string | null>(null)

  const jobs = jobsQ.data?.jobs ?? []
  const caps = jobsQ.data?.capabilities
  // The selection is latched into state, never derived per render: the list
  // refetches every 15s sorted newest-first, so a render-time `?? jobs[0]`
  // fallback would swap the open job whenever a teammate creates one. The
  // effect seeds the initial selection and recovers from a deleted job.
  const selected = jobs.find((j) => j.id === selectedId) ?? null
  useEffect(() => {
    if (!selected && jobs.length > 0) setSelectedId(jobs[0].id)
  }, [selected, jobs])

  if (!projectId) return null

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex' }}>
      <JobList
        projectId={projectId}
        hubId={nav.hubId ?? ''}
        projectName={nav.project?.name ?? ''}
        jobs={jobs}
        caps={caps}
        loading={jobsQ.isLoading}
        error={jobsQ.error as Error | null}
        selectedId={selected?.id ?? null}
        onSelect={setSelectedId}
      />
      <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex' }}>
        {selected ? (
          <JobDetail
            key={selected.id}
            projectId={projectId}
            jobId={selected.id}
            active={active}
            canWrite={caps?.write ?? false}
            canModerate={caps?.moderate ?? false}
            onDeleted={() => setSelectedId(null)}
            onDuplicated={setSelectedId}
          />
        ) : (
          <Box
            sx={{
              flex: 1,
              display: 'grid',
              placeItems: 'center',
              color: 'text.secondary',
              fontSize: 13,
              px: 3,
              textAlign: 'center',
            }}
          >
            {jobsQ.isLoading ? t('app.loadingJobs') : t('app.emptyJobs')}
          </Box>
        )}
      </Box>
    </Box>
  )
}
