import { faListCheck, faPaperclip, faPlus, faTrash } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  Chip,
  IconButton,
  LinearProgress,
  Menu,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useBatchMutations } from '../api/queries'
import { batchKindLabel, batchStatusLabel } from '../i18n/enums'
import { RefCard } from '../components/RefCard'
import { HubBrowserDialog } from '../components/hubbrowser/HubBrowserDialog'
import { docRefFromItem, encodeDocRef } from '../components/doccard/docref'
import { encodeTaskRef, taskRefFromTask } from '../components/taskcard/taskref'
import { PinStar } from '../components/PinStar'
import { batchRefFromBatch, encodeBatchRef, type JobRef } from '../components/productioncard/prodref'
import { useNav } from '../state/nav'
import { useLocalPin } from '../state/pins'
import { AttachTaskDialog } from '../tasks/AttachTaskDialog'
import { DocSourceButton } from './DocSourceButton'
import { StepNumBadge } from './chips'
import { PinnedDocCard } from './PinnedDocCard'
import { BATCH_STATUSES } from './types'
import type { ProdBatch } from './types'

// BatchDetail is the as-run record: frozen plan-doc versions, placeholders to
// supply, and as-run artifacts — organised per step. Everything renders from
// the batch's FROZEN plan (batch.steps), never the live graph, so later plan
// edits (deleted steps, next-run placeholders) can't rewrite what this run
// recorded — the append-only-history invariant, end to end.
export function BatchDetail({
  projectId,
  jobId,
  jobName,
  jobRef,
  batch,
  canWrite,
  canModerate,
  myId,
  onDeleted,
}: {
  projectId: string
  jobId: string
  /** owning job, for filing uploads under Jobs/<job>/<batch> */
  jobName: string
  /** owning job's fls: ref — the base a batch's own token is built on */
  jobRef: JobRef
  batch: ProdBatch
  canWrite: boolean
  canModerate: boolean
  myId: string
  onDeleted: () => void
}) {
  const { t } = useTranslation('production')
  const nav = useNav()
  const pin = useLocalPin()
  const batchToken = encodeBatchRef(batchRefFromBatch(jobRef, batch))
  const { updateBatch, removeBatch, addFulfillment, removeFulfillment, addRef, removeRef } =
    useBatchMutations(projectId, jobId)
  // Draft-while-editing buffer for the name: null renders the server value, a
  // string means it's being edited (so refetches never clobber typing).
  const [nameDraft, setNameDraft] = useState<string | null>(null)
  const [refMenu, setRefMenu] = useState<HTMLElement | null>(null)
  const [taskPickOpen, setTaskPickOpen] = useState(false)
  const [docPickOpen, setDocPickOpen] = useState(false)

  const canDelete = canModerate || batch.createdBy.id === myId
  const isProd = batch.kind === 'production'

  const saveName = () => {
    const trimmed = (nameDraft ?? '').trim()
    if (trimmed && trimmed !== batch.name) updateBatch.mutate({ batchId: batch.id, patch: { name: trimmed } })
    setNameDraft(null) // blank or unchanged → revert to the server value
  }

  // Index the batch's supplied documents for quick per-step lookup.
  const fulfillmentFor = (stepId: string, placeholderId: string) =>
    batch.fulfillments.find((f) => f.stepId === stepId && f.placeholderId === placeholderId)
  const asRunForStep = (stepId: string) =>
    batch.fulfillments.filter((f) => f.stepId === stepId && (f.isAsRun || !f.placeholderId))

  // Completeness over the FROZEN placeholders — a finished run's number never
  // changes when the plan gains or loses slots for future runs.
  const allPlaceholders = batch.steps.flatMap((s) => s.placeholders.map((p) => ({ stepId: s.stepId, id: p.id })))
  const filled = allPlaceholders.filter((p) =>
    batch.fulfillments.some((f) => f.stepId === p.stepId && f.placeholderId === p.id),
  ).length
  const pct = allPlaceholders.length ? Math.round((filled / allPlaceholders.length) * 100) : 100

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
      {/* header */}
      <Stack
        direction="row"
        alignItems="center"
        spacing={1.5}
        sx={{ px: 1.5, py: 1, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
      >
        {canWrite ? (
          <TextField
            variant="standard"
            value={nameDraft ?? batch.name}
            onFocus={() => setNameDraft(batch.name)}
            onChange={(e) => setNameDraft(e.target.value)}
            onBlur={saveName}
            onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
            placeholder={t('batchDetail.namePlaceholder')}
            sx={{
              minWidth: 0,
              maxWidth: 260,
              '& input': { fontWeight: 600, fontSize: 15, py: 0.25 },
              '& .MuiInput-underline:before': { borderBottom: 'none' },
            }}
          />
        ) : (
          <Typography variant="subtitle1" fontWeight={600} noWrap>
            {batch.name}
          </Typography>
        )}
        <Chip
          size="small"
          label={batchKindLabel(t, batch.kind)}
          sx={{
            height: 20,
            fontSize: 11,
            textTransform: 'capitalize',
            ...(isProd
              ? { color: 'secondary.contrastText', bgcolor: 'secondary.main' }
              : { color: 'primary.contrastText', bgcolor: 'primary.main' }),
          }}
        />
        <Typography variant="caption" color="text.secondary">
          {new Date(batch.runAt).toLocaleString()}
        </Typography>
        <PinStar
          pinned={pin.isPinned(batchToken)}
          onToggle={() =>
            pin.toggle({
              ref: batchToken,
              kind: 'batch',
              name: batch.name,
              projectId: jobRef.projectId,
              projectName: jobRef.projectName,
            })
          }
          fontSize={12}
        />
        {allPlaceholders.length > 0 && (
          <Tooltip title={t('batchDetail.placeholdersSupplied', { filled, count: allPlaceholders.length })}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, ml: 1 }}>
              <LinearProgress
                variant="determinate"
                value={pct}
                sx={{ width: 90, height: 6, borderRadius: 3 }}
                color={pct === 100 ? 'success' : 'primary'}
              />
              <Typography variant="caption" color="text.secondary" sx={{ fontVariantNumeric: 'tabular-nums' }}>
                {filled}/{allPlaceholders.length}
              </Typography>
            </Box>
          </Tooltip>
        )}
        <Box sx={{ flex: 1 }} />
        <Select
          size="small"
          value={batch.status}
          disabled={!canWrite}
          onChange={(e) => updateBatch.mutate({ batchId: batch.id, patch: { status: e.target.value } })}
          sx={{ fontSize: 12, '& .MuiSelect-select': { py: 0.5 }, textTransform: 'capitalize' }}
        >
          {BATCH_STATUSES.map((s) => (
            <MenuItem key={s} value={s} sx={{ fontSize: 12, textTransform: 'capitalize' }}>
              {batchStatusLabel(t, s)}
            </MenuItem>
          ))}
        </Select>
        {canDelete && (
          <Tooltip title={t('batchDetail.deleteBatch')}>
            <IconButton
              size="small"
              color="error"
              disabled={removeBatch.isPending}
              onClick={() => {
                if (window.confirm(t('batchDetail.deleteConfirm', { name: batch.name }))) {
                  removeBatch.mutate(batch.id, { onSuccess: onDeleted })
                }
              }}
            >
              <FontAwesomeIcon icon={faTrash} style={{ fontSize: 13 }} />
            </IconButton>
          </Tooltip>
        )}
      </Stack>

      {/* per-step record — the FROZEN plan, not the live graph */}
      <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto', p: 1.5 }}>
        {/* related tasks & documents (live references, not version-pinned) */}
        <Box sx={{ mb: 2 }}>
          <Stack direction="row" alignItems="center" spacing={1} sx={{ mb: 0.75 }}>
            <SectionLabel>{t('batchDetail.relatedRefs')}</SectionLabel>
            <Box sx={{ flex: 1 }} />
            {canWrite && (
              <Button
                size="small"
                startIcon={<FontAwesomeIcon icon={faPlus} style={{ fontSize: 10 }} />}
                onClick={(e) => setRefMenu(e.currentTarget)}
                sx={{ textTransform: 'none' }}
              >
                {t('batchDetail.add')}
              </Button>
            )}
          </Stack>
          <Stack spacing={0.75} alignItems="flex-start">
            {batch.refs.map((token) => (
              <Stack key={token} direction="row" alignItems="center" spacing={0.5} sx={{ maxWidth: '100%' }}>
                <RefCard token={token} />
                {canWrite && (
                  <Tooltip title={t('batchDetail.removeRef')}>
                    <IconButton size="small" onClick={() => removeRef.mutate({ batchId: batch.id, token })}>
                      <FontAwesomeIcon icon={faTrash} style={{ fontSize: 11 }} />
                    </IconButton>
                  </Tooltip>
                )}
              </Stack>
            ))}
            {batch.refs.length === 0 && (
              <Typography variant="caption" color="text.disabled">
                {t('empty.none')}
              </Typography>
            )}
          </Stack>
        </Box>

        {batch.steps.length === 0 && (
          <Typography variant="caption" color="text.secondary">
            {t('batchDetail.emptyPlan')}
          </Typography>
        )}
        <Stack spacing={1.5}>
          {batch.steps.map((step) => {
            const asRun = asRunForStep(step.stepId)
            return (
              <Paper key={step.stepId} variant="outlined" sx={{ p: 1.25, borderRadius: 1.5 }}>
                <Stack direction="row" alignItems="center" spacing={1} sx={{ mb: 1 }}>
                  <StepNumBadge num={step.num} size={20} />
                  <Typography variant="body2" fontWeight={600}>
                    {step.title}
                  </Typography>
                </Stack>

                {/* frozen plan documents */}
                {step.planDocs.length > 0 && (
                  <Box sx={{ mb: 1 }}>
                    <SectionLabel>{t('batchDetail.planDocsFrozen')}</SectionLabel>
                    <ChipWrap>
                      {step.planDocs.map((pd) => (
                        <PinnedDocCard key={pd.id} doc={pd.doc} />
                      ))}
                    </ChipWrap>
                  </Box>
                )}

                {/* placeholders to supply */}
                {step.placeholders.length > 0 && (
                  <Box sx={{ mb: 1 }}>
                    <SectionLabel>{t('batchDetail.docsToSupply')}</SectionLabel>
                    <Stack spacing={0.75}>
                      {step.placeholders.map((ph) => {
                        const f = fulfillmentFor(step.stepId, ph.id)
                        return (
                          <Stack key={ph.id} direction="row" spacing={1} alignItems="center">
                            <Typography variant="caption" sx={{ minWidth: 120, color: 'text.secondary' }}>
                              {ph.label}
                              {ph.required && <span style={{ color: '#d32f2f' }}> *</span>}
                            </Typography>
                            {f ? (
                              <PinnedDocCard
                                doc={f.doc}
                                onRemove={
                                  canWrite
                                    ? () => removeFulfillment.mutate({ batchId: batch.id, fulfillmentId: f.id })
                                    : undefined
                                }
                              />
                            ) : canWrite ? (
                              <DocSourceButton
                                folderPath={['Jobs', jobName, batch.name]}
                                label={t('batchDetail.supply')}
                                icon={faPlus}
                                onPin={(pin, source) =>
                                  addFulfillment.mutate({
                                    batchId: batch.id,
                                    body: { ...pin, stepId: step.stepId, placeholderId: ph.id, source },
                                  })
                                }
                              />
                            ) : (
                              <Chip size="small" label={t('batchDetail.notSupplied')} variant="outlined" sx={{ fontSize: 11 }} />
                            )}
                          </Stack>
                        )
                      })}
                    </Stack>
                  </Box>
                )}

                {/* as-run artifacts */}
                <Box>
                  <SectionLabel>{t('batchDetail.asRunArtifacts')}</SectionLabel>
                  <ChipWrap>
                    {asRun.map((f) => (
                      <PinnedDocCard
                        key={f.id}
                        doc={f.doc}
                        asRun
                        onRemove={
                          canWrite
                            ? () => removeFulfillment.mutate({ batchId: batch.id, fulfillmentId: f.id })
                            : undefined
                        }
                      />
                    ))}
                    {asRun.length === 0 && (
                      <Typography variant="caption" color="text.disabled">
                        {t('empty.none')}
                      </Typography>
                    )}
                  </ChipWrap>
                  {canWrite && (
                    <Box sx={{ mt: 0.75 }}>
                      <DocSourceButton
                        folderPath={['Jobs', jobName, batch.name]}
                        label={t('batchDetail.addAsRunArtifact')}
                        icon={faPlus}
                        variant="text"
                        onPin={(pin, source) =>
                          addFulfillment.mutate({
                            batchId: batch.id,
                            body: { ...pin, stepId: step.stepId, isAsRun: true, source },
                          })
                        }
                      />
                    </Box>
                  )}
                </Box>
              </Paper>
            )
          })}
        </Stack>
      </Box>

      {/* add-reference menu + pickers */}
      <Menu anchorEl={refMenu} open={!!refMenu} onClose={() => setRefMenu(null)}>
        <MenuItem
          onClick={() => {
            setRefMenu(null)
            setTaskPickOpen(true)
          }}
        >
          <FontAwesomeIcon icon={faListCheck} style={{ fontSize: 12, marginRight: 8, width: 16 }} />
          {t('batchDetail.linkTask')}
        </MenuItem>
        <MenuItem
          onClick={() => {
            setRefMenu(null)
            setDocPickOpen(true)
          }}
        >
          <FontAwesomeIcon icon={faPaperclip} style={{ fontSize: 12, marginRight: 8, width: 16 }} />
          {t('batchDetail.linkDoc')}
        </MenuItem>
      </Menu>
      {taskPickOpen && (
        <AttachTaskDialog
          open={taskPickOpen}
          projectId={projectId}
          onClose={() => setTaskPickOpen(false)}
          onPick={(task) => {
            setTaskPickOpen(false)
            addRef.mutate({ batchId: batch.id, token: encodeTaskRef(taskRefFromTask(task)) })
          }}
        />
      )}
      {docPickOpen && (
        <HubBrowserDialog
          open={docPickOpen}
          hubId={nav.hubId ?? null}
          title={t('batchDetail.linkDocTitle')}
          pickLabel={t('batchDetail.linkAction')}
          initialProject={nav.project ?? null}
          onClose={() => setDocPickOpen(false)}
          onPick={(pick) => {
            setDocPickOpen(false)
            if (!pick.item) return
            addRef.mutate({ batchId: batch.id, token: encodeDocRef(docRefFromItem(pick.hubId, pick.item)) })
          }}
        />
      )}
    </Box>
  )
}

function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.5, fontWeight: 600 }}>
      {children}
    </Typography>
  )
}

// The card grids inside a step. Items align to the top, not the middle: cards
// are the same height at rest, but flipping one to its back face makes it
// taller, and centring would shove every other card in its row downwards to
// re-centre on it.
function ChipWrap({ children }: { children: ReactNode }) {
  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75, alignItems: 'flex-start' }}>{children}</Box>
  )
}
