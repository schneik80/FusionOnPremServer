import {
  faDiagramProject,
  faFlask,
  faLink,
  faPlus,
  faTableList,
  faTrash,
  faXmark,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  Chip,
  CircularProgress,
  IconButton,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthMe, useJob, useJobGraphMutations, useProductionMutations } from '../api/queries'
import { PinStar } from '../components/PinStar'
import { encodeJobRef, jobRefFromJob } from '../components/productioncard/prodref'
import { useLocalPin } from '../state/pins'
import { BatchesView } from './BatchesView'
import { PlaceholderChip, StepNumBadge } from './chips'
import { JobCanvas } from './JobCanvas'
import { StepEditor } from './StepEditor'
import type { ProdStep } from './types'
import { jobDisplayId } from './types'

// JobDetail renders one job: its steps as a plain vertical list with inline
// graph edits (add/remove steps, connect steps, manage placeholders). The
// interactive pan/zoom flow canvas replaces this list in P2 — the data model
// (positioned steps + a flat edge list) is already canvas-ready.
export function JobDetail({
  projectId,
  jobId,
  active,
  canWrite,
  canModerate,
  onDeleted,
}: {
  projectId: string
  jobId: string
  active: boolean
  canWrite: boolean
  canModerate: boolean
  onDeleted: () => void
}) {
  const { t } = useTranslation('production')
  const jobQ = useJob(projectId, jobId, active)
  const { updateJob, removeJob } = useProductionMutations(projectId)
  const g = useJobGraphMutations(projectId, jobId)
  const myId = useAuthMe().data?.user?.id ?? ''
  // Called before the loading/not-found early returns below — hooks can't sit
  // behind them.
  const pin = useLocalPin()

  const [newStep, setNewStep] = useState('')
  const [view, setView] = useState<'flow' | 'list' | 'batches'>('flow')
  const [selectedStepId, setSelectedStepId] = useState<string | null>(null)
  // Draft-while-editing buffers: null renders the server value, a string means
  // the field is being edited. No seeding effect needed, so refetches never
  // clobber typing and the display stays live when not editing.
  const [nameDraft, setNameDraft] = useState<string | null>(null)
  const [descDraft, setDescDraft] = useState<string | null>(null)

  const job = jobQ.data
  if (jobQ.isLoading && !job) {
    return (
      <Box sx={{ flex: 1, display: 'grid', placeItems: 'center' }}>
        <CircularProgress size={22} />
      </Box>
    )
  }
  if (!job) {
    return (
      <Box sx={{ flex: 1, display: 'grid', placeItems: 'center', color: 'text.secondary' }}>
        <Typography variant="caption">{t('jobDetail.notAvailable')}</Typography>
      </Box>
    )
  }

  const canDelete = canModerate || job.createdBy.id === myId
  const stepName = (id: string) => job.steps.find((s) => s.id === id)?.title ?? id
  const jobToken = encodeJobRef(jobRefFromJob(job))

  const saveName = () => {
    const trimmed = (nameDraft ?? '').trim()
    if (trimmed && trimmed !== job.name) updateJob.mutate({ jobId: job.id, patch: { name: trimmed } })
    setNameDraft(null) // blank or unchanged → revert to the server value
  }
  const saveDesc = () => {
    if (descDraft !== null && descDraft !== (job.description ?? '')) {
      updateJob.mutate({ jobId: job.id, patch: { description: descDraft } })
    }
    setDescDraft(null)
  }

  const addStep = () => {
    const title = newStep.trim()
    if (!title) return
    // Stagger new steps so the P2 canvas doesn't stack them; the list ignores
    // x/y but they persist for the canvas.
    g.addStep.mutate(
      { title, x: 40 + job.steps.length * 200, y: 120 },
      { onSuccess: () => setNewStep('') },
    )
  }

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
      {/* header */}
      <Stack
        direction="row"
        alignItems="center"
        spacing={1}
        sx={{ px: 1.5, py: 0.75, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
      >
        {canWrite ? (
          <TextField
            variant="standard"
            value={nameDraft ?? job.name}
            onFocus={() => setNameDraft(job.name)}
            onChange={(e) => setNameDraft(e.target.value)}
            onBlur={saveName}
            onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
            placeholder={t('jobDetail.namePlaceholder')}
            sx={{
              minWidth: 0,
              maxWidth: 320,
              '& input': { fontWeight: 600, fontSize: 15, py: 0.25 },
              // Underline only on hover/focus, so the header reads as a title.
              '& .MuiInput-underline:before': { borderBottom: 'none' },
            }}
          />
        ) : (
          <Typography variant="subtitle1" fontWeight={600} noWrap sx={{ minWidth: 0 }}>
            {job.name}
          </Typography>
        )}
        <Typography variant="caption" color="text.secondary">
          {jobDisplayId(job)}
        </Typography>
        <PinStar
          pinned={pin.isPinned(jobToken)}
          onToggle={() =>
            pin.toggle({
              ref: jobToken,
              kind: 'job',
              name: job.name,
              projectId: job.projectId,
              projectName: job.projectName,
            })
          }
          fontSize={12}
        />
        <Box sx={{ flex: 1 }} />
        <ToggleButtonGroup
          size="small"
          exclusive
          value={view}
          onChange={(_, v) => v && setView(v)}
          sx={{ mr: 0.5, '& .MuiToggleButton-root': { py: 0.25, px: 1, textTransform: 'none' } }}
        >
          <ToggleButton value="flow">
            <FontAwesomeIcon icon={faDiagramProject} style={{ fontSize: 12, marginRight: 6 }} />
            {t('jobDetail.viewFlow')}
          </ToggleButton>
          <ToggleButton value="list">
            <FontAwesomeIcon icon={faTableList} style={{ fontSize: 12, marginRight: 6 }} />
            {t('jobDetail.viewList')}
          </ToggleButton>
          <ToggleButton value="batches">
            <FontAwesomeIcon icon={faFlask} style={{ fontSize: 12, marginRight: 6 }} />
            {t('jobDetail.viewBatches')}
            {job.batches.length > 0 && (
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
                {job.batches.length}
              </Box>
            )}
          </ToggleButton>
        </ToggleButtonGroup>
        {canDelete && (
          <Tooltip title={t('jobDetail.deleteJob')}>
            <IconButton
              size="small"
              color="error"
              disabled={removeJob.isPending}
              onClick={() => {
                if (window.confirm(t('jobDetail.deleteConfirm', { name: job.name }))) {
                  removeJob.mutate(job.id, { onSuccess: onDeleted })
                }
              }}
            >
              <FontAwesomeIcon icon={faTrash} style={{ fontSize: 13 }} />
            </IconButton>
          </Tooltip>
        )}
      </Stack>

      {/* body: flow canvas, plain list, or batches — all over the same job */}
      {view === 'flow' ? (
        <Box sx={{ flex: 1, minHeight: 0, position: 'relative', display: 'flex' }}>
          <JobCanvas
            job={job}
            canWrite={canWrite}
            graph={g}
            selectedStepId={selectedStepId}
            onSelectStep={setSelectedStepId}
          />
          <StepEditor
            step={job.steps.find((s) => s.id === selectedStepId) ?? null}
            jobName={job.name}
            canWrite={canWrite}
            graph={g}
            onClose={() => setSelectedStepId(null)}
          />
        </Box>
      ) : view === 'batches' ? (
        <BatchesView
          projectId={projectId}
          jobId={job.id}
          job={job}
          canWrite={canWrite}
          canModerate={canModerate}
          myId={myId}
        />
      ) : (
        <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto', p: 1.5 }}>
          {canWrite ? (
            <TextField
              variant="standard"
              fullWidth
              multiline
              value={descDraft ?? job.description ?? ''}
              onFocus={() => setDescDraft(job.description ?? '')}
              onChange={(e) => setDescDraft(e.target.value)}
              onBlur={saveDesc}
              placeholder={t('jobDetail.descPlaceholder')}
              sx={{
                mb: 1.5,
                '& textarea': { fontSize: 13 },
                '& .MuiInput-root': { color: 'text.secondary' },
                '& .MuiInput-underline:before': { borderBottom: 'none' },
              }}
            />
          ) : (
            job.description && (
              <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
                {job.description}
              </Typography>
            )
          )}

        <Stack spacing={1.5}>
          {job.steps.length === 0 && (
            <Typography variant="caption" color="text.secondary">
              {t('jobDetail.emptySteps')}
            </Typography>
          )}
          {job.steps.map((step) => (
            <StepCard
              key={step.id}
              step={step}
              allSteps={job.steps}
              outgoing={job.edges.filter((e) => e.from === step.id)}
              stepName={stepName}
              canWrite={canWrite}
              graph={g}
            />
          ))}
        </Stack>

        {canWrite && (
          <Stack direction="row" spacing={1} sx={{ mt: 2 }} alignItems="center">
            <TextField
              size="small"
              placeholder={t('jobDetail.newStepPlaceholder')}
              value={newStep}
              onChange={(e) => setNewStep(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && addStep()}
              sx={{ maxWidth: 320 }}
            />
            <Button
              size="small"
              variant="contained"
              onClick={addStep}
              disabled={!newStep.trim() || g.addStep.isPending}
              startIcon={<FontAwesomeIcon icon={faPlus} style={{ fontSize: 11 }} />}
              sx={{ textTransform: 'none' }}
            >
              {t('jobDetail.addStep')}
            </Button>
          </Stack>
          )}
        </Box>
      )}
    </Box>
  )
}

type Graph = ReturnType<typeof useJobGraphMutations>

function StepCard({
  step,
  allSteps,
  outgoing,
  stepName,
  canWrite,
  graph,
}: {
  step: ProdStep
  allSteps: ProdStep[]
  outgoing: { id: string; from: string; to: string }[]
  stepName: (id: string) => string
  canWrite: boolean
  graph: Graph
}) {
  const { t } = useTranslation('production')
  const [placeholder, setPlaceholder] = useState('')
  const [connectTo, setConnectTo] = useState('')
  const [title, setTitle] = useState(step.title)
  const [desc, setDesc] = useState(step.description ?? '')

  // Candidate targets: any other step not already connected from this one.
  const connected = new Set(outgoing.map((e) => e.to))
  const candidates = allSteps.filter((s) => s.id !== step.id && !connected.has(s.id))

  const saveTitle = () => {
    const trimmed = title.trim()
    if (trimmed && trimmed !== step.title) graph.updateStep.mutate({ stepId: step.id, patch: { title: trimmed } })
    else if (!trimmed) setTitle(step.title) // titles can't be blank — revert
  }
  const saveDesc = () => {
    if (desc !== (step.description ?? '')) graph.updateStep.mutate({ stepId: step.id, patch: { description: desc } })
  }

  return (
    <Paper variant="outlined" sx={{ p: 1.25, borderRadius: 1.5 }}>
      <Stack direction="row" alignItems="center" spacing={1}>
        <StepNumBadge num={step.num} />
        {canWrite ? (
          <TextField
            variant="standard"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onBlur={saveTitle}
            onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
            placeholder={t('stepCard.namePlaceholder')}
            sx={{ flex: 1, minWidth: 0, '& input': { fontWeight: 600, fontSize: 14, py: 0.25 } }}
          />
        ) : (
          <Typography variant="body2" fontWeight={600} sx={{ flex: 1, minWidth: 0 }} noWrap>
            {step.title}
          </Typography>
        )}
        {canWrite && (
          <Tooltip title={t('stepCard.deleteStep')}>
            <IconButton
              size="small"
              onClick={() => graph.removeStep.mutate(step.id)}
              disabled={graph.removeStep.isPending}
            >
              <FontAwesomeIcon icon={faXmark} style={{ fontSize: 12 }} />
            </IconButton>
          </Tooltip>
        )}
      </Stack>

      {canWrite ? (
        <Box sx={{ mt: 0.25, pl: 3.75 }}>
          <TextField
            variant="standard"
            fullWidth
            multiline
            value={desc}
            onChange={(e) => setDesc(e.target.value)}
            onBlur={saveDesc}
            placeholder={t('stepCard.descPlaceholder')}
            sx={{ '& textarea': { fontSize: 12 }, '& .MuiInput-root': { fontSize: 12, color: 'text.secondary' } }}
          />
        </Box>
      ) : (
        step.description && (
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5, pl: 3.75 }}>
            {step.description}
          </Typography>
        )
      )}

      {/* placeholders */}
      <Box sx={{ mt: 1, pl: 3.75 }}>
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.5 }}>
          {t('stepCard.placeholders')}
        </Typography>
        <Stack direction="row" spacing={0.75} sx={{ flexWrap: 'wrap', gap: 0.75 }}>
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
              {t('empty.none')}
            </Typography>
          )}
        </Stack>
        {canWrite && (
          <Stack direction="row" spacing={1} sx={{ mt: 0.75 }} alignItems="center">
            <TextField
              size="small"
              variant="standard"
              placeholder={t('placeholders.addHint')}
              value={placeholder}
              onChange={(e) => setPlaceholder(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && placeholder.trim()) {
                  graph.addPlaceholder.mutate(
                    { stepId: step.id, body: { label: placeholder.trim() } },
                    { onSuccess: () => setPlaceholder('') },
                  )
                }
              }}
              sx={{ maxWidth: 320, '& input': { fontSize: 12 } }}
            />
          </Stack>
        )}
      </Box>

      {/* connections (outgoing edges) */}
      <Box sx={{ mt: 1, pl: 3.75 }}>
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.5 }}>
          {t('stepCard.leadsTo')}
        </Typography>
        <Stack direction="row" spacing={0.75} sx={{ flexWrap: 'wrap', gap: 0.75 }}>
          {outgoing.map((e) => (
            <Chip
              key={e.id}
              size="small"
              icon={<FontAwesomeIcon icon={faLink} style={{ fontSize: 10 }} />}
              label={stepName(e.to)}
              onDelete={canWrite ? () => graph.removeEdge.mutate(e.id) : undefined}
              sx={{ fontSize: 11 }}
            />
          ))}
          {outgoing.length === 0 && (
            <Typography variant="caption" color="text.disabled">
              {t('empty.nothingYet')}
            </Typography>
          )}
        </Stack>
        {canWrite && candidates.length > 0 && (
          <Select
            size="small"
            displayEmpty
            value={connectTo}
            onChange={(e) => {
              const to = e.target.value
              if (to) {
                graph.addEdge.mutate(
                  { from: step.id, to },
                  { onSuccess: () => setConnectTo('') },
                )
              }
            }}
            sx={{ mt: 0.75, minWidth: 180, fontSize: 12, '& .MuiSelect-select': { py: 0.5 } }}
          >
            <MenuItem value="" disabled>
              {t('stepCard.connectTo')}
            </MenuItem>
            {candidates.map((s) => (
              <MenuItem key={s.id} value={s.id} sx={{ fontSize: 12 }}>
                {s.title}
              </MenuItem>
            ))}
          </Select>
        )}
      </Box>
    </Paper>
  )
}
