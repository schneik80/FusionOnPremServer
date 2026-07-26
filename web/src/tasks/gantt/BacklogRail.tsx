import { Box, ListItem, ListItemButton, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { APP_RAIL_WIDTH, Column } from '../../components/Column'
import { PriorityDot, fmtDue, isOverdue } from '../chips'
import { taskDisplayId, type Task } from '../types'

// BacklogRail lists the project's unscheduled tasks beside the chart. When
// the caller may write, a row can be dragged onto the chart to schedule it
// (pointerdown hands off to useGanttDrag; a plain click still opens the
// task). Rows echo TaskRow's dense layout without its status column — here
// everything is by definition unscheduled.
export function BacklogRail({
  tasks,
  loading,
  error,
  writable,
  onOpen,
  onDragStart,
}: {
  tasks: Task[]
  loading: boolean
  error: Error | null
  writable: boolean
  onOpen: (taskId: string) => void
  onDragStart: (e: React.PointerEvent, task: Task) => void
}) {
  const { t: tr } = useTranslation('tasks')
  const sorted = [...tasks].sort((a, b) => b.num - a.num)
  return (
    <Column
      title={tr('gantt.backlogTitle')}
      width={APP_RAIL_WIDTH}
      loading={loading}
      error={error}
      empty={!loading && tasks.length === 0}
      emptyText={tr('gantt.backlogEmpty')}
    >
      {sorted.map((t) => {
        const overdue = isOverdue(t.dueDate, t.status)
        const meta = [
          taskDisplayId(t),
          t.assignee?.name || t.assignee?.email,
          t.dueDate ? tr('row.due', { date: fmtDue(t.dueDate) }) : undefined,
        ]
          .filter(Boolean)
          .join(' · ')
        return (
          <ListItem key={t.id} disablePadding>
            <ListItemButton
              onClick={() => onOpen(t.id)}
              onPointerDown={(e) => writable && onDragStart(e, t)}
              sx={{
                alignItems: 'flex-start',
                py: 0.75,
                cursor: writable ? 'grab' : undefined,
                touchAction: 'none',
              }}
            >
              <Box sx={{ pt: 0.75, pr: 1, display: 'flex' }}>
                <PriorityDot priority={t.priority} />
              </Box>
              <Stack sx={{ minWidth: 0, flex: 1 }}>
                <Typography variant="body2" noWrap>
                  {t.title}
                </Typography>
                <Typography variant="caption" noWrap color={overdue ? 'error.main' : 'text.secondary'}>
                  {meta}
                </Typography>
              </Stack>
            </ListItemButton>
          </ListItem>
        )
      })}
    </Column>
  )
}
