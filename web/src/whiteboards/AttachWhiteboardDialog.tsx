import { faMagnifyingGlass } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  InputAdornment,
  List,
  ListItemButton,
  TextField,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useWhiteboards } from '../api/queries'
import { foldSearch } from '../fmt/graphemes'
import { boardDisplayId } from './types'
import type { Whiteboard } from './types'

// AttachWhiteboardDialog picks one of the current project's boards — the
// whiteboard sibling of AttachTaskDialog. Chat's composer, the wiki editor's
// toolbar and a task's attachments all use it to drop an fls:whiteboard token
// into their content. Like AttachTaskDialog it hands back the board itself and
// lets the caller encode, because the wiki needs the markdown form of the same
// token.
export function AttachWhiteboardDialog({
  open,
  projectId,
  onClose,
  onPick,
}: {
  open: boolean
  projectId: string | null
  onClose: () => void
  onPick: (board: Whiteboard) => void
}) {
  const { t } = useTranslation('whiteboards')
  const boardsQ = useWhiteboards(projectId, open)
  const [search, setSearch] = useState('')

  const q = foldSearch(search.trim())
  // The list already arrives newest-first; searching must not reorder it.
  const boards = (boardsQ.data?.whiteboards ?? []).filter(
    (b) => !q || foldSearch(b.name).includes(q),
  )

  return (
    <Dialog open={open} onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('attachDialog.title')}</DialogTitle>
      <DialogContent sx={{ p: 0, display: 'flex', flexDirection: 'column', height: 420 }}>
        <Box sx={{ px: 2, pb: 1 }}>
          <TextField
            fullWidth
            size="small"
            autoFocus
            placeholder={t('attachDialog.searchPlaceholder')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            InputProps={{
              startAdornment: (
                <InputAdornment position="start">
                  <FontAwesomeIcon icon={faMagnifyingGlass} style={{ fontSize: 13 }} />
                </InputAdornment>
              ),
            }}
          />
        </Box>
        <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto', borderTop: 1, borderColor: 'divider' }}>
          {boardsQ.isLoading ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', p: 3 }}>
              <CircularProgress size={22} />
            </Box>
          ) : boardsQ.error ? (
            <Typography variant="body2" color="error" sx={{ p: 2, textAlign: 'center' }}>
              {(boardsQ.error as Error).message}
            </Typography>
          ) : boards.length === 0 ? (
            <Typography variant="body2" color="text.secondary" sx={{ p: 2, textAlign: 'center' }}>
              {q ? t('attachDialog.emptySearch') : t('attachDialog.empty')}
            </Typography>
          ) : (
            <List dense disablePadding>
              {boards.map((b) => (
                <ListItemButton key={b.id} onClick={() => onPick(b)} sx={{ py: 0.75 }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Typography variant="body2" fontWeight={600} noWrap>
                      {b.name}
                    </Typography>
                    <Typography variant="caption" color="text.secondary" noWrap>
                      {boardDisplayId(b)} · {new Date(b.updatedAt).toLocaleDateString()}
                      {b.updatedBy.name ? ` · ${b.updatedBy.name}` : ''}
                    </Typography>
                  </Box>
                </ListItemButton>
              ))}
            </List>
          )}
        </Box>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common:cancel')}</Button>
      </DialogActions>
    </Dialog>
  )
}
