import { faPaperclip, faTrash, faXmark } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  Divider,
  Drawer,
  IconButton,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { useJobGraphMutations } from '../api/queries'
import { DocSourceButton } from './DocSourceButton'
import { PinnedDocCard } from './PinnedDocCard'
import { ResultsEditor } from './ResultsEditor'
import { DecisionGlyph, PlaceholderChip, StepNumBadge } from './chips'
import type { ProdStep } from './types'
import { isDecision } from './types'

type Graph = ReturnType<typeof useJobGraphMutations>

// StepEditor is the right-hand drawer for the step selected on the canvas. It
// owns the step's non-positional edits — title, description, placeholders,
// delete — keeping the canvas node itself compact. Title/description save on
// blur; placeholders are added/removed live. Plan documents are attached here
// too once P3 lands.
export function StepEditor({
  step,
  jobName,
  canWrite,
  graph,
  onClose,
}: {
  step: ProdStep | null
  /** owning job, for filing uploads under Jobs/<job> */
  jobName: string
  canWrite: boolean
  graph: Graph
  onClose: () => void
}) {
  const { t } = useTranslation('production')
  const [title, setTitle] = useState('')
  const [desc, setDesc] = useState('')
  const [placeholder, setPlaceholder] = useState('')

  // Re-seed the local fields only when a DIFFERENT step is selected. The
  // fields are edit buffers: keying the effect on title/description too would
  // clobber in-progress typing whenever a mutation settles or the 15s poll
  // delivers a remote edit (save title → type into description → title
  // mutation lands → description wiped).
  useEffect(() => {
    setTitle(step?.title ?? '')
    setDesc(step?.description ?? '')
    setPlaceholder('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step?.id])

  const open = !!step
  const decision = !!step && isDecision(step)

  const saveTitle = () => {
    if (!step) return
    const trimmed = title.trim()
    if (trimmed && trimmed !== step.title) graph.updateStep.mutate({ stepId: step.id, patch: { title: trimmed } })
    else setTitle(step.title)
  }
  const saveDesc = () => {
    if (!step) return
    if (desc !== (step.description ?? '')) graph.updateStep.mutate({ stepId: step.id, patch: { description: desc } })
  }
  const addPlaceholder = () => {
    if (!step) return
    const label = placeholder.trim()
    if (!label) return
    graph.addPlaceholder.mutate({ stepId: step.id, body: { label } }, { onSuccess: () => setPlaceholder('') })
  }

  return (
    <Drawer
      anchor="right"
      variant="persistent"
      open={open}
      PaperProps={{
        sx: { width: 320, position: 'absolute', border: 0, borderLeft: 1, borderColor: 'divider' },
      }}
      sx={{ '& .MuiDrawer-root': { position: 'absolute' }, position: 'absolute' }}
    >
      {step && (
        <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
          <Stack
            direction="row"
            alignItems="center"
            spacing={1}
            sx={{ px: 1.5, py: 1, borderBottom: 1, borderColor: 'divider' }}
          >
            <StepNumBadge num={step.num} />
            {decision && <DecisionGlyph color="primary.main" />}
            <Typography variant="subtitle2" sx={{ flex: 1 }}>
              {t(decision ? 'stepEditor.titleDecision' : 'stepEditor.title')}
            </Typography>
            <IconButton size="small" onClick={onClose}>
              <FontAwesomeIcon icon={faXmark} style={{ fontSize: 14 }} />
            </IconButton>
          </Stack>

          <Box sx={{ p: 1.5, flex: 1, minHeight: 0, overflowY: 'auto' }}>
            <TextField
              label={t('stepEditor.titleLabel')}
              size="small"
              fullWidth
              value={title}
              disabled={!canWrite}
              onChange={(e) => setTitle(e.target.value)}
              onBlur={saveTitle}
              onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
            />
            <TextField
              label={t('stepEditor.descriptionLabel')}
              size="small"
              fullWidth
              multiline
              minRows={2}
              value={desc}
              disabled={!canWrite}
              onChange={(e) => setDesc(e.target.value)}
              onBlur={saveDesc}
              sx={{ mt: 1.5 }}
            />

            <Divider sx={{ my: 2 }} />

            {/* A decision routes rather than carrying work, so its outcomes
                replace the document sections entirely — the server rejects
                plan documents and placeholders on one. */}
            {decision && (
              <>
                <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.75 }}>
                  {t('stepEditor.resultsCaption')}
                </Typography>
                <ResultsEditor step={step} canWrite={canWrite} graph={graph} />
              </>
            )}

            {!decision && (
              <>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.75 }}>
              {t('stepEditor.placeholdersCaption')}
            </Typography>
            <Stack direction="row" spacing={0.75} sx={{ flexWrap: 'wrap', gap: 0.75, mb: 1 }}>
              {step.placeholders.map((ph) => (
                <PlaceholderChip
                  key={ph.id}
                  placeholder={ph}
                  onDelete={
                    canWrite
                      ? () => graph.removePlaceholder.mutate({ stepId: step.id, placeholderId: ph.id })
                      : undefined
                  }
                />
              ))}
              {step.placeholders.length === 0 && (
                <Typography variant="caption" color="text.disabled">
                  {t('empty.noneYet')}
                </Typography>
              )}
            </Stack>
            {canWrite && (
              <TextField
                size="small"
                fullWidth
                placeholder={t('placeholders.addHint')}
                value={placeholder}
                onChange={(e) => setPlaceholder(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && addPlaceholder()}
                sx={{ '& input': { fontSize: 12 } }}
              />
            )}

            <Divider sx={{ my: 2 }} />

            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.75 }}>
              {t('stepEditor.planDocsCaption')}
            </Typography>
            <Stack spacing={0.75} sx={{ mb: 1 }}>
              {step.planDocs.map((pd) => (
                <PinnedDocCard
                  key={pd.id}
                  doc={pd.doc}
                  onRemove={
                    canWrite
                      ? () => graph.removePlanDoc.mutate({ stepId: step.id, planDocId: pd.id })
                      : undefined
                  }
                />
              ))}
              {step.planDocs.length === 0 && (
                <Typography variant="caption" color="text.disabled">
                  {t('empty.noneYet')}
                </Typography>
              )}
            </Stack>
            {canWrite && (
              <DocSourceButton
                folderPath={['Jobs', jobName]}
                label={t('stepEditor.attachDocument')}
                icon={faPaperclip}
                onPin={(pin) => graph.addPlanDoc.mutate({ stepId: step.id, body: pin })}
              />
            )}
              </>
            )}
          </Box>

          {canWrite && (
            <Box sx={{ p: 1.5, borderTop: 1, borderColor: 'divider' }}>
              <Tooltip title={t('stepEditor.deleteStep')}>
                <Button
                  size="small"
                  color="error"
                  fullWidth
                  startIcon={<FontAwesomeIcon icon={faTrash} style={{ fontSize: 12 }} />}
                  disabled={graph.removeStep.isPending}
                  onClick={() => {
                    if (window.confirm(t('stepEditor.deleteConfirm', { title: step.title }))) {
                      graph.removeStep.mutate(step.id, { onSuccess: onClose })
                    }
                  }}
                  sx={{ textTransform: 'none' }}
                >
                  {t('stepEditor.deleteStep')}
                </Button>
              </Tooltip>
            </Box>
          )}
        </Box>
      )}
    </Drawer>
  )
}
