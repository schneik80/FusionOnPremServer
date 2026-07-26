import {
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  Typography,
} from '@mui/material'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { useTask } from '../api/queries'
import { TaskDetails } from './TaskDetails'

// TaskViewDialog shows one task's full details in a dialog — the landing
// surface for fls:task cards, which can reference a task in any project
// (not just the one the browser is currently in), so it must work without
// nav context.
export function TaskViewDialog({
  open,
  projectId,
  taskId,
  onClose,
}: {
  open: boolean
  projectId: string
  taskId: string
  onClose: () => void
}) {
  const { t } = useTranslation('tasks')
  const taskQ = useTask(open ? projectId : null, taskId)
  const gone = taskQ.error instanceof ApiError && taskQ.error.status === 404

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogContent sx={{ p: 0, display: 'flex', minHeight: 200 }}>
        {taskQ.data ? (
          <TaskDetails task={taskQ.data} onDeleted={onClose} />
        ) : (
          <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', p: 3 }}>
            {taskQ.isLoading ? (
              <CircularProgress size={24} />
            ) : (
              <Typography variant="body2" color="text.secondary">
                {gone ? t('viewDialog.gone') : ((taskQ.error as Error | null)?.message ?? t('viewDialog.unavailable'))}
              </Typography>
            )}
          </Box>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common:close')}</Button>
      </DialogActions>
    </Dialog>
  )
}
