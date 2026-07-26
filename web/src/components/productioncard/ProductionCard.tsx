import { faArrowUpRightFromSquare, faDiagramProject, faFlask } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, Tooltip, Typography } from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useJob } from '../../api/queries'
import { batchStatusLabel } from '../../i18n/enums'
import { ProductionViewDialog } from './ProductionViewDialog'
import type { BatchRef, JobRef } from './prodref'

// ProductionCard is the unfurled form of an fls:job / fls:batch token — the
// production sibling of TaskCard: a compact link-preview card that hydrates
// from the shared job query and opens a read-only ProductionViewDialog on
// click. It sits in any project's chat, wiki, or task body, so it must not
// depend on the browser's nav state. Built from span elements so it is valid
// inside a markdown <p>.
export function ProductionCard({ jobRef, batchRef }: { jobRef: JobRef; batchRef?: BatchRef }) {
  const { t } = useTranslation('browse')
  const jobQ = useJob(jobRef.projectId, jobRef.jobId, true)
  const [open, setOpen] = useState(false)

  const job = jobQ.data
  const batch = batchRef ? job?.batches.find((b) => b.id === batchRef.batchId) : undefined
  const gone = !jobQ.isLoading && (!job || (batchRef && !batch))

  const title = batchRef ? batch?.name ?? batchRef.batchName : job?.name ?? jobRef.jobName
  const isProdBatch = batch?.kind === 'production'

  const subtitle = gone
    ? batchRef
      ? t('prodCard.batchNotFound')
      : t('prodCard.jobNotFound')
    : batchRef
      ? batch
        ? [jobRef.projectName, jobRef.jobName, batchStatusLabel(t, batch.status)]
            .filter(Boolean)
            .join(' · ')
        : jobQ.isLoading
          ? t('common:loading')
          : t('prodCard.batchUnavailable')
      : job
        ? [
            jobRef.projectName,
            t('prodCard.steps', { count: job.steps.length }),
            t('prodCard.batches', { count: job.batches.length }),
          ].join(' · ')
        : jobQ.isLoading
          ? t('common:loading')
          : t('prodCard.jobUnavailable')

  const card = (
    <Box
      component="span"
      role={gone ? undefined : 'button'}
      tabIndex={gone ? undefined : 0}
      onClick={gone ? undefined : () => setOpen(true)}
      onKeyDown={
        gone
          ? undefined
          : (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                setOpen(true)
              }
            }
      }
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 1.25,
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
        bgcolor: 'background.paper',
        px: 1,
        py: 0.75,
        my: 0.25,
        maxWidth: 'min(420px, 100%)',
        cursor: gone ? 'default' : 'pointer',
        verticalAlign: 'middle',
        userSelect: 'none',
        opacity: gone ? 0.6 : 1,
        transition: 'border-color 120ms',
        '&:hover, &:focus-visible': gone
          ? undefined
          : { borderColor: 'primary.main', '& .prodcard-go': { opacity: 1 } },
      }}
    >
      <Box
        component="span"
        sx={{
          width: 40,
          height: 40,
          flexShrink: 0,
          borderRadius: 0.5,
          bgcolor: 'action.hover',
          color: isProdBatch ? undefined : 'primary.main',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          ...(isProdBatch && { color: '#b7410e' }),
        }}
      >
        <FontAwesomeIcon icon={batchRef ? faFlask : faDiagramProject} style={{ fontSize: 17 }} />
      </Box>
      <Box component="span" sx={{ display: 'inline-flex', flexDirection: 'column', minWidth: 0 }}>
        <Typography component="span" variant="subtitle2" noWrap sx={{ lineHeight: 1.3 }}>
          {title}
        </Typography>
        <Typography component="span" variant="caption" noWrap color="text.secondary">
          {subtitle}
        </Typography>
      </Box>
      <Box
        component="span"
        className="prodcard-go"
        sx={{ ml: 0.5, color: 'primary.main', opacity: 0, transition: 'opacity 120ms', flexShrink: 0, display: 'inline-flex' }}
      >
        <FontAwesomeIcon icon={faArrowUpRightFromSquare} style={{ fontSize: 12 }} />
      </Box>
    </Box>
  )

  return (
    <>
      {gone ? card : <Tooltip title={batchRef ? t('prodCard.openBatch') : t('prodCard.openJob')}>{card}</Tooltip>}
      {open && <ProductionViewDialog jobRef={jobRef} batchRef={batchRef} onClose={() => setOpen(false)} />}
    </>
  )
}
