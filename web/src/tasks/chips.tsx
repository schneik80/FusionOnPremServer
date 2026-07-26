import { Box, Chip, Tooltip } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { taskPriorityLabel, taskStatusLabel } from '../i18n/enums'
import type { TaskPriority, TaskStatus } from './types'

// Shared status/priority affordances for the task list, board, details and
// cards, so the color language stays consistent everywhere.

export const STATUS_COLOR: Record<
  TaskStatus,
  'default' | 'info' | 'warning' | 'success'
> = {
  todo: 'default',
  inprogress: 'info',
  blocked: 'warning',
  done: 'success',
}

export const PRIORITY_COLOR: Record<
  TaskPriority,
  'default' | 'info' | 'warning' | 'error'
> = {
  low: 'default',
  medium: 'info',
  high: 'warning',
  urgent: 'error',
}

export function StatusChip({ status, size = 'small' }: { status: TaskStatus; size?: 'small' | 'medium' }) {
  const { t } = useTranslation('tasks')
  return <Chip label={taskStatusLabel(t, status)} color={STATUS_COLOR[status]} size={size} variant="outlined" />
}

export function PriorityChip({ priority, size = 'small' }: { priority: TaskPriority; size?: 'small' | 'medium' }) {
  const { t } = useTranslation('tasks')
  return <Chip label={taskPriorityLabel(t, priority)} color={PRIORITY_COLOR[priority]} size={size} variant="outlined" />
}

// PriorityDot is the compact form for dense rows and board cards.
export function PriorityDot({ priority }: { priority: TaskPriority }) {
  const { t } = useTranslation('tasks')
  const palette: Record<TaskPriority, string> = {
    low: 'text.disabled',
    medium: 'info.main',
    high: 'warning.main',
    urgent: 'error.main',
  }
  return (
    <Tooltip title={t('chips.priorityTooltip', { priority: taskPriorityLabel(t, priority) })}>
      <Box
        component="span"
        sx={{
          width: 8,
          height: 8,
          borderRadius: '50%',
          bgcolor: palette[priority],
          flexShrink: 0,
          display: 'inline-block',
        }}
      />
    </Tooltip>
  )
}

// fmtDue renders a YYYY-MM-DD due date compactly; isOverdue drives the
// error styling for past-due open tasks.
export function fmtDue(due: string): string {
  if (!due) return ''
  const d = new Date(due + 'T00:00:00')
  if (Number.isNaN(d.getTime())) return due
  const sameYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: sameYear ? undefined : 'numeric',
  })
}

export function isOverdue(due: string | undefined, status: TaskStatus): boolean {
  if (!due || status === 'done') return false
  const d = new Date(due + 'T23:59:59')
  return !Number.isNaN(d.getTime()) && d.getTime() < Date.now()
}
