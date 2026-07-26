import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Stack,
  TextField,
} from '@mui/material'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useTaskMutations } from '../api/queries'
import type { Task } from './types'

// QuickTaskDialog is the streamlined create used from chat and the wiki:
// just a title (and optional description), everything else defaulted
// (todo/medium, no assignee). The created task flows back so the caller
// can drop its fls:task token into the draft. Creation is strictly
// click/Enter-driven — never effect-driven — so StrictMode's double
// invocation can't mint duplicates.
export function QuickTaskDialog({
  open,
  onClose,
  projectId,
  hubId,
  projectName,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  projectId: string
  hubId: string
  projectName: string
  onCreated: (task: Task) => void
}) {
  const { t } = useTranslation('tasks')
  const muts = useTaskMutations(projectId)
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')

  useEffect(() => {
    if (!open) return
    setTitle('')
    setDescription('')
    muts.create.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const err = muts.create.error as Error | null

  function create() {
    if (!title.trim() || muts.create.isPending) return
    muts.create.mutate(
      { hubId, projectName, title, description },
      {
        onSuccess: (t) => {
          onClose()
          onCreated(t)
        },
      },
    )
  }

  return (
    <Dialog open={open} onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('quickDialog.title', { project: projectName })}</DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{ mt: 0.5 }}>
          {err && <Alert severity="error">{err.message}</Alert>}
          <TextField
            label={t('quickDialog.titleLabel')}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            autoFocus
            fullWidth
            size="small"
            inputProps={{ maxLength: 200 }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                create()
              }
            }}
          />
          <TextField
            label={t('quickDialog.descriptionLabel')}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            fullWidth
            size="small"
            multiline
            minRows={2}
            maxRows={6}
          />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={muts.create.isPending}>
          {t('common:cancel')}
        </Button>
        <Button variant="contained" onClick={create} disabled={muts.create.isPending || !title.trim()}>
          {t('common:create')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
