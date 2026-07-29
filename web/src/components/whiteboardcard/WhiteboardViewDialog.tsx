import { Dialog, DialogContent, DialogTitle, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useWhiteboard } from '../../api/queries'
import { LazyBoardCanvas } from '../../whiteboards/LazyBoardCanvas'
import { boardDisplayId } from '../../whiteboards/types'
import type { WhiteboardRef } from './wbref'

// WhiteboardViewDialog opens a board where the reader already is, the way
// TaskCard opens TaskViewDialog: a card can sit in any project's chat, wiki or
// task body, so taking the user to the Whiteboards tab would mean relocating
// the whole browser to another project. The board is the real, live canvas —
// same sync room, same permissions — not a preview.
//
// A second canvas for a board already open on the Whiteboards tab is the same
// situation as the board being open in two browser tabs, which the room
// protocol is built for (per-record last-write-wins, keyed by client id).
export function WhiteboardViewDialog({
  whiteboardRef,
  onClose,
}: {
  whiteboardRef: WhiteboardRef
  onClose: () => void
}) {
  const { t } = useTranslation('whiteboards')
  const q = useWhiteboard(whiteboardRef.projectId, whiteboardRef.boardId)
  const board = q.data?.board ?? null

  return (
    <Dialog open onClose={onClose} maxWidth="lg" fullWidth>
      <DialogTitle sx={{ display: 'flex', alignItems: 'baseline', gap: 1, pb: 1 }}>
        <Typography component="span" variant="h6" noWrap sx={{ minWidth: 0 }}>
          {board?.name ?? whiteboardRef.name}
        </Typography>
        {board && (
          <Typography component="span" variant="caption" color="text.secondary">
            {boardDisplayId(board)} · {board.projectName || whiteboardRef.projectName}
          </Typography>
        )}
      </DialogTitle>
      {/* The canvas sizes itself to this box; it must be a flex container with
          a real height or tldraw measures zero. 78vh keeps the dialog's title
          and the page's own chrome visible around it. */}
      <DialogContent dividers sx={{ p: 0, height: '78vh', display: 'flex', minHeight: 0 }}>
        {board ? (
          <LazyBoardCanvas
            projectId={whiteboardRef.projectId}
            boardId={whiteboardRef.boardId}
            canWrite={q.data?.canWrite ?? false}
          />
        ) : (
          <Typography
            variant="body2"
            color="text.secondary"
            sx={{ flex: 1, display: 'grid', placeItems: 'center', p: 3, textAlign: 'center' }}
          >
            {q.isLoading ? t('common:loading') : t('viewDialog.unavailable')}
          </Typography>
        )}
      </DialogContent>
    </Dialog>
  )
}
