import { faDiagramProject, faFlask } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Chip,
  CircularProgress,
  LinearProgress,
  Paper,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Typography,
} from '@mui/material'
import { useMemo, useState } from 'react'
import { useTheme } from '@mui/material/styles'
import { useTranslation } from 'react-i18next'
import { useMyProduction } from '../api/queries'
import { batchStatusLabel } from '../i18n/enums'
import { ProductionViewDialog } from '../components/productioncard/ProductionViewDialog'
import type { BatchRef, JobRef } from '../components/productioncard/prodref'
import type { Job, ProdBatch } from './types'
import { jobDisplayId } from './types'

// ProductionScreen is the rail-level, cross-project view of the caller's
// production work — the Production sibling of TasksScreen. It leads with runs
// in flight (planned/running), because "what is on the floor right now" is the
// question this app exists to answer; a second view lists every job the caller
// owns. Clicking anything opens the same read-only ProductionViewDialog the
// fls:job / fls:batch cards use, so this screen needs no browser nav state.

// A batch flattened together with the job/project it belongs to.
interface RunRow {
  job: Job
  batch: ProdBatch
}

const ACTIVE = new Set(['planned', 'running'])

export function ProductionScreen({ active = true }: { active?: boolean }) {
  const { t } = useTranslation('production')
  const q = useMyProduction(active)
  const [view, setView] = useState<'runs' | 'jobs'>('runs')
  const [open, setOpen] = useState<{ jobRef: JobRef; batchRef?: BatchRef } | null>(null)

  const jobs = useMemo(() => q.data?.jobs ?? [], [q.data])

  // Runs in flight, soonest-scheduled first.
  const runs = useMemo(() => {
    const rows: RunRow[] = []
    for (const job of jobs) {
      for (const batch of job.batches) {
        if (ACTIVE.has(batch.status)) rows.push({ job, batch })
      }
    }
    return rows.sort((a, b) => new Date(a.batch.runAt).getTime() - new Date(b.batch.runAt).getTime())
  }, [jobs])

  const jobRefOf = (job: Job): JobRef => ({
    hubId: job.hubId,
    projectId: job.projectId,
    projectName: job.projectName,
    jobId: job.id,
    jobName: job.name,
  })
  const batchRefOf = (job: Job, batch: ProdBatch): BatchRef => ({
    ...jobRefOf(job),
    batchId: batch.id,
    batchName: batch.name,
  })

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
      <Stack
        direction="row"
        alignItems="center"
        spacing={1}
        sx={{ px: 1.5, py: 0.75, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
      >
        <Typography variant="subtitle1" fontWeight={600}>
          {t('screen.title')}
        </Typography>
        <Box sx={{ flex: 1 }} />
        <ToggleButtonGroup
          size="small"
          exclusive
          value={view}
          onChange={(_, v) => v && setView(v)}
          sx={{ '& .MuiToggleButton-root': { py: 0.25, px: 1.25, textTransform: 'none' } }}
        >
          <ToggleButton value="runs">
            <FontAwesomeIcon icon={faFlask} style={{ fontSize: 12, marginRight: 6 }} />
            {t('screen.runsInFlight')}
            {runs.length > 0 && (
              <Box
                component="span"
                sx={{
                  ml: 0.75,
                  px: 0.6,
                  minWidth: 16,
                  borderRadius: 8,
                  bgcolor: 'primary.main',
                  color: 'primary.contrastText',
                  fontSize: 10,
                  lineHeight: '16px',
                }}
              >
                {runs.length}
              </Box>
            )}
          </ToggleButton>
          <ToggleButton value="jobs">
            <FontAwesomeIcon icon={faDiagramProject} style={{ fontSize: 12, marginRight: 6 }} />
            {t('screen.myJobs')}
          </ToggleButton>
        </ToggleButtonGroup>
      </Stack>

      <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto', p: 1.5 }}>
        {q.isLoading ? (
          <Box sx={{ display: 'grid', placeItems: 'center', py: 6 }}>
            <CircularProgress size={22} />
          </Box>
        ) : q.error ? (
          <Typography variant="body2" color="error">
            {t('screen.loadFailed')}
          </Typography>
        ) : view === 'runs' ? (
          runs.length === 0 ? (
            <Empty text={t('screen.emptyRuns')} />
          ) : (
            <Stack spacing={1}>
              {runs.map(({ job, batch }) => (
                <RunCard
                  key={`${job.projectId}:${job.id}:${batch.id}`}
                  job={job}
                  batch={batch}
                  onOpen={() => setOpen({ jobRef: jobRefOf(job), batchRef: batchRefOf(job, batch) })}
                />
              ))}
            </Stack>
          )
        ) : jobs.length === 0 ? (
          <Empty text={t('screen.emptyJobs')} />
        ) : (
          <Stack spacing={1}>
            {jobs.map((job) => (
              <JobCard
                key={`${job.projectId}:${job.id}`}
                job={job}
                onOpen={() => setOpen({ jobRef: jobRefOf(job) })}
              />
            ))}
          </Stack>
        )}
      </Box>

      {open && (
        <ProductionViewDialog
          jobRef={open.jobRef}
          batchRef={open.batchRef}
          onClose={() => setOpen(null)}
        />
      )}
    </Box>
  )
}

// completenessOf counts supplied placeholders against the batch's FROZEN plan —
// the same rule BatchDetail uses, so the two never disagree.
function completenessOf(batch: ProdBatch): { filled: number; total: number; pct: number } {
  const slots = batch.steps.flatMap((s) => s.placeholders.map((p) => ({ stepId: s.stepId, id: p.id })))
  const filled = slots.filter((slot) =>
    batch.fulfillments.some((f) => f.stepId === slot.stepId && f.placeholderId === slot.id),
  ).length
  const total = slots.length
  return { filled, total, pct: total ? Math.round((filled / total) * 100) : 100 }
}

function RunCard({ job, batch, onOpen }: { job: Job; batch: ProdBatch; onOpen: () => void }) {
  const { t } = useTranslation('production')
  const prodAccent = useTheme().palette.secondary.main
  const isProd = batch.kind === 'production'
  const { filled, total, pct } = completenessOf(batch)
  return (
    <Paper
      variant="outlined"
      onClick={onOpen}
      sx={{
        p: 1.25,
        borderRadius: 1.5,
        cursor: 'pointer',
        transition: 'border-color .1s, box-shadow .1s',
        '&:hover': { borderColor: 'primary.main', boxShadow: 1 },
      }}
    >
      <Stack direction="row" alignItems="center" spacing={1}>
        <FontAwesomeIcon
          icon={faFlask}
          style={{ fontSize: 13, color: isProd ? prodAccent : undefined }}
        />
        <Typography variant="body2" fontWeight={600} noWrap sx={{ minWidth: 0 }}>
          {batch.name}
        </Typography>
        <Chip
          size="small"
          label={batchStatusLabel(t, batch.status)}
          variant="outlined"
          sx={{ height: 18, fontSize: 10, textTransform: 'capitalize' }}
        />
        <Box sx={{ flex: 1 }} />
        {total > 0 && (
          <Stack direction="row" alignItems="center" spacing={0.75}>
            <LinearProgress
              variant="determinate"
              value={pct}
              sx={{ width: 70, height: 5, borderRadius: 3 }}
              color={pct === 100 ? 'success' : 'primary'}
            />
            <Typography variant="caption" color="text.secondary" sx={{ fontVariantNumeric: 'tabular-nums' }}>
              {filled}/{total}
            </Typography>
          </Stack>
        )}
        <Typography variant="caption" color="text.secondary" sx={{ whiteSpace: 'nowrap' }}>
          {new Date(batch.runAt).toLocaleDateString()}
        </Typography>
      </Stack>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.25, pl: 3 }}>
        {job.projectName} › {job.name} · {jobDisplayId(job)}
      </Typography>
    </Paper>
  )
}

function JobCard({ job, onOpen }: { job: Job; onOpen: () => void }) {
  const { t } = useTranslation('production')
  const activeRuns = job.batches.filter((b) => ACTIVE.has(b.status)).length
  return (
    <Paper
      variant="outlined"
      onClick={onOpen}
      sx={{
        p: 1.25,
        borderRadius: 1.5,
        cursor: 'pointer',
        transition: 'border-color .1s, box-shadow .1s',
        '&:hover': { borderColor: 'primary.main', boxShadow: 1 },
      }}
    >
      <Stack direction="row" alignItems="center" spacing={1}>
        <FontAwesomeIcon icon={faDiagramProject} style={{ fontSize: 13 }} />
        <Typography variant="body2" fontWeight={600} noWrap sx={{ minWidth: 0 }}>
          {job.name}
        </Typography>
        <Box sx={{ flex: 1 }} />
        {activeRuns > 0 && (
          <Chip
            size="small"
            label={t('counts.inFlight', { count: activeRuns })}
            sx={{ height: 18, fontSize: 10, bgcolor: 'primary.main', color: 'primary.contrastText' }}
          />
        )}
      </Stack>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.25, pl: 3 }}>
        {job.projectName} · {jobDisplayId(job)} · {t('counts.steps', { count: job.steps.length })} ·{' '}
        {t('counts.batches', { count: job.batches.length })}
      </Typography>
    </Paper>
  )
}

function Empty({ text }: { text: string }) {
  return (
    <Typography variant="body2" color="text.secondary" sx={{ px: 1, py: 4, textAlign: 'center' }}>
      {text}
    </Typography>
  )
}
