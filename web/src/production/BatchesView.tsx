import { faFlask, faPlus } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  List,
  ListItemButton,
  Stack,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from '@mui/material'
import { alpha } from '@mui/material/styles'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useBatchMutations } from '../api/queries'
import { batchKindLabel } from '../i18n/enums'
import { BatchDetail, PRODUCTION_ACCENT } from './BatchDetail'
import { BatchTimeline } from './BatchTimeline'
import type { Job } from './types'

// BatchesView is the third job view: a sub-rail of runs plus the selected
// batch's detail. Creating a batch freezes the plan documents' versions
// server-side; the detail lets the user supply documents against placeholders
// and record as-run artifacts, all version-pinned.
export function BatchesView({
  projectId,
  jobId,
  job,
  canWrite,
  canModerate,
  myId,
}: {
  projectId: string
  jobId: string
  job: Job
  canWrite: boolean
  canModerate: boolean
  myId: string
}) {
  const { t } = useTranslation('production')
  const { createBatch } = useBatchMutations(projectId, jobId)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)

  // Newest batch first.
  const batches = [...job.batches].sort((a, b) => b.num - a.num)
  const selected = batches.find((b) => b.id === selectedId) ?? batches[0] ?? null

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex' }}>
      {/* batch sub-rail */}
      <Box
        sx={{
          width: 210,
          flexShrink: 0,
          borderRight: 1,
          borderColor: 'divider',
          display: 'flex',
          flexDirection: 'column',
          minHeight: 0,
        }}
      >
        <Stack
          direction="row"
          alignItems="center"
          sx={{ px: 1, py: 0.75, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
        >
          <Typography variant="subtitle2" sx={{ flex: 1, pl: 0.5 }}>
            {t('batchesView.title')}
          </Typography>
          {canWrite && (
            <Tooltip title={t('batchesView.newBatch')}>
              <Button
                size="small"
                variant="contained"
                onClick={() => setCreateOpen(true)}
                startIcon={<FontAwesomeIcon icon={faPlus} style={{ fontSize: 11 }} />}
                sx={{ py: 0.25, textTransform: 'none' }}
              >
                {t('batchesView.new')}
              </Button>
            </Tooltip>
          )}
        </Stack>
        <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
          {batches.length === 0 ? (
            <Typography variant="caption" color="text.secondary" sx={{ p: 2, display: 'block' }}>
              {t('batchesView.emptyRail')}
            </Typography>
          ) : (
            <List dense disablePadding>
              {batches.map((b) => (
                <ListItemButton
                  key={b.id}
                  selected={b.id === selected?.id}
                  onClick={() => setSelectedId(b.id)}
                  sx={{
                    flexDirection: 'column',
                    alignItems: 'flex-start',
                    gap: 0.25,
                    py: 0.75,
                    transition: 'background-color .1s',
                  }}
                >
                  <Stack direction="row" spacing={0.75} alignItems="center" sx={{ width: '100%' }}>
                    <FontAwesomeIcon
                      icon={faFlask}
                      style={{
                        fontSize: 11,
                        color: b.kind === 'production' ? PRODUCTION_ACCENT : undefined,
                      }}
                    />
                    <Typography variant="body2" fontWeight={600} noWrap sx={{ flex: 1 }}>
                      {b.name}
                    </Typography>
                  </Stack>
                  <Typography variant="caption" color="text.secondary">
                    {batchKindLabel(t, b.kind)} · {new Date(b.runAt).toLocaleDateString()}
                  </Typography>
                </ListItemButton>
              ))}
            </List>
          )}
        </Box>
      </Box>

      {/* timeline + selected batch */}
      <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
        <BatchTimeline
          batches={batches}
          selectedId={selected?.id ?? null}
          onSelect={setSelectedId}
        />
        {selected ? (
          <BatchDetail
            key={selected.id}
            projectId={projectId}
            jobId={jobId}
            jobName={job.name}
            batch={selected}
            canWrite={canWrite}
            canModerate={canModerate}
            myId={myId}
            onDeleted={() => setSelectedId(null)}
          />
        ) : (
          <Box sx={{ flex: 1, display: 'grid', placeItems: 'center', color: 'text.secondary', fontSize: 13, px: 3, textAlign: 'center' }}>
            {canWrite ? t('batchesView.emptyCanWrite') : t('batchesView.emptyReadOnly')}
          </Box>
        )}
      </Box>

      <CreateBatchDialog
        open={createOpen}
        pending={createBatch.isPending}
        onClose={() => setCreateOpen(false)}
        onCreate={(name, kind) =>
          createBatch.mutate(
            { name, kind },
            { onSuccess: (b) => { setSelectedId(b.id); setCreateOpen(false) } },
          )
        }
      />
    </Box>
  )
}

function CreateBatchDialog({
  open,
  pending,
  onClose,
  onCreate,
}: {
  open: boolean
  pending: boolean
  onClose: () => void
  onCreate: (name: string, kind: string) => void
}) {
  const { t } = useTranslation('production')
  const [name, setName] = useState('')
  const [kind, setKind] = useState<'prove' | 'production'>('prove')

  return (
    <Dialog open={open} onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('createBatch.title')}</DialogTitle>
      <DialogContent>
        <TextField
          autoFocus
          fullWidth
          size="small"
          label={t('createBatch.nameLabel')}
          placeholder={t('createBatch.namePlaceholder')}
          value={name}
          onChange={(e) => setName(e.target.value)}
          sx={{ mt: 1 }}
        />
        <ToggleButtonGroup
          size="small"
          exclusive
          value={kind}
          onChange={(_, v) => v && setKind(v)}
          sx={{ mt: 2, '& .MuiToggleButton-root': { textTransform: 'none', px: 2 } }}
        >
          <ToggleButton value="prove">{t('createBatch.proveOut')}</ToggleButton>
          <ToggleButton
            value="production"
            sx={{ '&.Mui-selected': { color: PRODUCTION_ACCENT, borderColor: alpha(PRODUCTION_ACCENT, 0.5) } }}
          >
            {t('createBatch.production')}
          </ToggleButton>
        </ToggleButtonGroup>
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 2 }}>
          {t('createBatch.freezeNote')}
        </Typography>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} sx={{ textTransform: 'none' }}>
          {t('common:cancel')}
        </Button>
        <Button
          variant="contained"
          disabled={!name.trim() || pending}
          onClick={() => onCreate(name.trim(), kind)}
          sx={{ textTransform: 'none' }}
        >
          {t('createBatch.create')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
