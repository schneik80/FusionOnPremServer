import { faDiagramProject, faFlask } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Chip,
  CircularProgress,
  Dialog,
  DialogContent,
  DialogTitle,
  Divider,
  Stack,
  Typography,
} from '@mui/material'
import { useTheme } from '@mui/material/styles'
import { useTranslation } from 'react-i18next'
import { useJob } from '../../api/queries'
import { batchKindLabel, batchStatusLabel } from '../../i18n/enums'
import { PinnedDocCard } from '../../production/PinnedDocCard'
import { jobDisplayId } from '../../production/types'
import type { BatchRef, JobRef } from './prodref'

// ProductionViewDialog is the read-only unfurled view of a Job or Batch ref —
// the production sibling of TaskViewDialog. It hydrates from the shared job
// query (which carries the full graph including batches), so a card in any
// project's chat/wiki/task can show the job or the frozen batch record without
// depending on the browser's nav state.
export function ProductionViewDialog({
  jobRef,
  batchRef,
  onClose,
}: {
  jobRef: JobRef
  batchRef?: BatchRef
  onClose: () => void
}) {
  const { t } = useTranslation('browse')
  const prodAccent = useTheme().palette.secondary.main
  const jobQ = useJob(jobRef.projectId, jobRef.jobId, true)
  const job = jobQ.data
  const batch = batchRef ? job?.batches.find((b) => b.id === batchRef.batchId) : undefined
  const gone = !jobQ.isLoading && !job

  const title = batchRef
    ? batch?.name ?? batchRef.batchName
    : job?.name ?? jobRef.jobName

  return (
    <Dialog open onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <FontAwesomeIcon
          icon={batchRef ? faFlask : faDiagramProject}
          style={{ fontSize: 15, color: batch?.kind === 'production' ? prodAccent : undefined }}
        />
        <Box sx={{ minWidth: 0, flex: 1 }}>
          <Typography variant="h6" noWrap>
            {title}
          </Typography>
          <Typography variant="caption" color="text.secondary">
            {jobRef.projectName}
            {job ? ` · ${jobDisplayId(job)} ${job.name}` : ''}
          </Typography>
        </Box>
      </DialogTitle>
      <DialogContent dividers>
        {jobQ.isLoading ? (
          <Box sx={{ display: 'grid', placeItems: 'center', py: 4 }}>
            <CircularProgress size={22} />
          </Box>
        ) : gone ? (
          <Typography variant="body2" color="text.secondary">
            {batchRef ? t('prodView.batchGone') : t('prodView.jobGone')}
          </Typography>
        ) : batchRef ? (
          batch ? (
            <BatchSummary batch={batch} />
          ) : (
            <Typography variant="body2" color="text.secondary">
              {t('prodView.batchDeletedFrom', { job: job?.name })}
            </Typography>
          )
        ) : (
          job && <JobSummary job={job} />
        )}
      </DialogContent>
    </Dialog>
  )
}

function JobSummary({ job }: { job: NonNullable<ReturnType<typeof useJob>['data']> }) {
  const { t } = useTranslation('browse')
  return (
    <Stack spacing={1.5}>
      {job.description && (
        <Typography variant="body2" color="text.secondary">
          {job.description}
        </Typography>
      )}
      <Typography variant="caption" color="text.secondary">
        {t('prodCard.steps', { count: job.steps.length })} ·{' '}
        {t('prodCard.batches', { count: job.batches.length })}
      </Typography>
      <Divider />
      <Typography variant="subtitle2">{t('prodView.stepsHeading')}</Typography>
      <Stack spacing={0.5}>
        {job.steps.map((s) => (
          <Typography key={s.id} variant="body2">
            {s.num}. {s.title}
          </Typography>
        ))}
        {job.steps.length === 0 && (
          <Typography variant="caption" color="text.disabled">
            {t('prodView.noSteps')}
          </Typography>
        )}
      </Stack>
    </Stack>
  )
}

function BatchSummary({ batch }: { batch: NonNullable<ReturnType<typeof useJob>['data']>['batches'][number] }) {
  const { t } = useTranslation('browse')
  return (
    <Stack spacing={1.5}>
      <Stack direction="row" spacing={1} alignItems="center">
        <Chip
          size="small"
          label={batchKindLabel(t, batch.kind)}
          sx={{
            height: 20,
            fontSize: 11,
            textTransform: 'capitalize',
            ...(batch.kind === 'production'
              ? { color: 'secondary.contrastText', bgcolor: 'secondary.main' }
              : { color: 'primary.contrastText', bgcolor: 'primary.main' }),
          }}
        />
        <Chip size="small" variant="outlined" label={batchStatusLabel(t, batch.status)} sx={{ height: 20, fontSize: 11, textTransform: 'capitalize' }} />
        <Typography variant="caption" color="text.secondary">
          {new Date(batch.runAt).toLocaleString()}
        </Typography>
      </Stack>
      {batch.steps.map((step) => {
        const asRun = batch.fulfillments.filter((f) => f.stepId === step.stepId)
        return (
          <Box key={step.stepId}>
            <Typography variant="subtitle2" gutterBottom>
              {step.num}. {step.title}
            </Typography>
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
              {step.planDocs.map((pd) => (
                <PinnedDocCard key={pd.id} doc={pd.doc} />
              ))}
              {asRun.map((f) => (
                <PinnedDocCard key={f.id} doc={f.doc} asRun={f.isAsRun} />
              ))}
              {step.planDocs.length === 0 && asRun.length === 0 && (
                <Typography variant="caption" color="text.disabled">
                  {t('prodView.noDocuments')}
                </Typography>
              )}
            </Box>
          </Box>
        )
      })}
    </Stack>
  )
}
