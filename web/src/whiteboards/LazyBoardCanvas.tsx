import { Box, CircularProgress } from '@mui/material'
import { Suspense, lazy } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorBoundary } from '../components/ErrorBoundary'

// tldraw is a large dependency — code-split so it only loads when someone
// actually opens a board, keeping it out of the app's entry chunk. The lazy()
// call lives at module scope so the two hosts (the Whiteboards tab and the
// fls:whiteboard card's dialog) share ONE chunk and one component identity;
// declaring it per host would mean two chunks and a remount on every swap.
const WhiteboardCanvas = lazy(() =>
  import('./WhiteboardCanvas').then((m) => ({ default: m.WhiteboardCanvas })),
)

// LazyBoardCanvas is the canvas plus the boundary and fallback every host
// needs: a board that fails to load must not take its host down with it, and
// the reset key is the board id so switching boards clears a previous error.
export function LazyBoardCanvas({
  projectId,
  boardId,
  canWrite,
}: {
  projectId: string
  boardId: string
  canWrite: boolean
}) {
  const { t } = useTranslation('whiteboards')
  return (
    <ErrorBoundary label={t('errorLabel')} resetKey={boardId}>
      <Suspense
        fallback={
          <Box sx={{ flex: 1, display: 'grid', placeItems: 'center' }}>
            <CircularProgress size={22} />
          </Box>
        }
      >
        <WhiteboardCanvas key={boardId} projectId={projectId} boardId={boardId} canWrite={canWrite} />
      </Suspense>
    </ErrorBoundary>
  )
}
