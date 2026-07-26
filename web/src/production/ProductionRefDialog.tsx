import {
  Box,
  Dialog,
  DialogContent,
  DialogTitle,
  List,
  ListItemButton,
  Radio,
  Stack,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useJob, useJobs } from '../api/queries'
import { batchKindLabel } from '../i18n/enums'
import { encodeBatchRef, encodeJobRef } from '../components/productioncard/prodref'
import type { JobSummary } from './types'
import { jobDisplayId } from './types'

// ProductionRefDialog picks a Job — or a specific Batch within it — from the
// current project and returns the encoded fls:job / fls:batch token to embed.
// Reused by chat, the wiki editor, and task details to link production work.
export function ProductionRefDialog({
  open,
  projectId,
  hubId,
  projectName,
  onClose,
  onPick,
}: {
  open: boolean
  projectId: string | null
  hubId: string
  projectName: string
  onClose: () => void
  // token is the raw fls: token; label is the display name, for callers (the
  // wiki) that need to wrap it in a markdown link. Chat/tasks ignore label.
  onPick: (token: string, label: string) => void
}) {
  const { t } = useTranslation('production')
  const jobsQ = useJobs(projectId, open)
  const [expanded, setExpanded] = useState<string | null>(null)

  const jobs = jobsQ.data?.jobs ?? []

  const pickJob = (j: JobSummary) => {
    onPick(encodeJobRef({ hubId, projectId: projectId!, projectName, jobId: j.id, jobName: j.name }), j.name)
    onClose()
  }
  const pickBatch = (j: JobSummary, batchId: string, batchName: string) => {
    onPick(
      encodeBatchRef({
        hubId,
        projectId: projectId!,
        projectName,
        jobId: j.id,
        jobName: j.name,
        batchId,
        batchName,
      }),
      batchName,
    )
    onClose()
  }

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle>{t('refDialog.title')}</DialogTitle>
      <DialogContent dividers>
        {jobs.length === 0 ? (
          <Typography variant="body2" color="text.secondary">
            {jobsQ.isLoading ? t('common:loading') : t('refDialog.emptyJobs')}
          </Typography>
        ) : (
          <List dense disablePadding>
            {jobs.map((j) => (
              <Box key={j.id}>
                <ListItemButton onClick={() => pickJob(j)} sx={{ borderRadius: 1 }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Typography variant="body2" fontWeight={600} noWrap>
                      {j.name}
                    </Typography>
                    <Typography variant="caption" color="text.secondary">
                      {jobDisplayId(j)} · {t('refDialog.jobRow', { count: j.batchCount })}
                    </Typography>
                  </Box>
                  {j.batchCount > 0 && (
                    <Typography
                      variant="caption"
                      color="primary"
                      onClick={(e) => {
                        e.stopPropagation()
                        setExpanded(expanded === j.id ? null : j.id)
                      }}
                      sx={{ cursor: 'pointer', ml: 1, flexShrink: 0 }}
                    >
                      {expanded === j.id ? t('refDialog.hideBatches') : t('refDialog.orBatch')}
                    </Typography>
                  )}
                </ListItemButton>
                {expanded === j.id && (
                  <BatchChoices
                    projectId={projectId}
                    job={j}
                    onPick={(batchId, batchName) => pickBatch(j, batchId, batchName)}
                  />
                )}
              </Box>
            ))}
          </List>
        )}
      </DialogContent>
    </Dialog>
  )
}

// BatchChoices fetches one job's graph on demand — the jobs list carries only
// counts, so batches are pulled just for the row the user expanded rather than
// for every job in the project.
function BatchChoices({
  projectId,
  job,
  onPick,
}: {
  projectId: string | null
  job: JobSummary
  onPick: (batchId: string, batchName: string) => void
}) {
  const { t } = useTranslation('production')
  const jobQ = useJob(projectId, job.id, true)
  const batches = jobQ.data?.batches ?? []

  if (jobQ.isLoading) {
    return (
      <Stack sx={{ pl: 3, py: 0.5 }}>
        <Typography variant="caption" color="text.secondary">
          {t('refDialog.loadingBatches')}
        </Typography>
      </Stack>
    )
  }
  return (
    <Stack sx={{ pl: 3, py: 0.5 }}>
      {batches.map((b) => (
        <ListItemButton key={b.id} onClick={() => onPick(b.id, b.name)} sx={{ borderRadius: 1, py: 0.25 }}>
          <Radio size="small" checked={false} sx={{ p: 0.5, mr: 0.5 }} />
          <Typography variant="body2" noWrap>
            {b.name}
          </Typography>
          <Typography variant="caption" color="text.secondary" sx={{ ml: 1 }}>
            {batchKindLabel(t, b.kind)} · {new Date(b.runAt).toLocaleDateString()}
          </Typography>
        </ListItemButton>
      ))}
    </Stack>
  )
}
