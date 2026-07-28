import { faDiagramProject, faFlask } from '@fortawesome/free-solid-svg-icons'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useJob } from '../../api/queries'
import { fmtDate } from '../../fmt'
import { batchKindLabel, batchStatusLabel } from '../../i18n/enums'
import { EntityCard, type CardMeta } from '../entitycard/EntityCard'
import { ProductionViewDialog } from './ProductionViewDialog'
import type { BatchRef, JobRef } from './prodref'

// ProductionCard is the unfurled form of an fls:job / fls:batch token — the
// same shared EntityCard as the document and task cards, hydrating from the
// shared job query and opening a read-only ProductionViewDialog. It sits in any
// project's chat, wiki, or task body, so it must not depend on the browser's
// nav state.
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

  const meta: CardMeta[] = batch
    ? ([
        { label: t('card.kind'), value: batchKindLabel(t, batch.kind) },
        { label: t('card.status'), value: batchStatusLabel(t, batch.status) },
        batch.runAt
          ? {
              label: t('card.run'),
              value: fmtDate(batch.runAt, { dateStyle: 'medium', timeStyle: 'short' }),
            }
          : null,
        batch.createdBy?.name ? { label: t('card.modifiedBy'), value: batch.createdBy.name } : null,
      ].filter(Boolean) as CardMeta[])
    : job
      ? ([
          { label: t('card.steps'), value: String(job.steps.length) },
          { label: t('card.batches'), value: String(job.batches.length) },
          job.createdBy?.name ? { label: t('card.modifiedBy'), value: job.createdBy.name } : null,
        ].filter(Boolean) as CardMeta[])
      : []

  return (
    <>
      <EntityCard
        title={title}
        subtitle={subtitle}
        icon={batchRef ? faFlask : faDiagramProject}
        iconColor={isProdBatch ? 'secondary.main' : 'primary.main'}
        meta={meta}
        metaLoading={jobQ.isLoading}
        onNavigate={gone ? undefined : () => setOpen(true)}
        dimmed={gone}
        selectable
      />
      {open && <ProductionViewDialog jobRef={jobRef} batchRef={batchRef} onClose={() => setOpen(false)} />}
    </>
  )
}
