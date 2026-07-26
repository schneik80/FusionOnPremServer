import { faArrowUpRightFromSquare, faListCheck } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, Tooltip, Typography } from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { useTask } from '../../api/queries'
import { taskStatusLabel } from '../../i18n/enums'
import { STATUS_COLOR, fmtDue, isOverdue } from '../../tasks/chips'
import { TaskViewDialog } from '../../tasks/TaskViewDialog'
import { taskDisplayId } from '../../tasks/types'
import type { TaskRef } from './taskref'

// TaskCard is the unfurled form of a TaskRef (see taskref.ts) — the task
// sibling of DocumentCard: a compact link-preview card with a status-tinted
// icon, the task number and title, and a second line of project · status ·
// assignee · due. It hydrates itself from the shared task query (falling
// back to the title captured in the token while loading) and opens the
// task's details in a dialog on click — a card can sit in any project's
// chat or wiki, so it must not depend on the browser's nav state.
//
// A deleted task's 404 is a designed state, not an error: chat logs and
// published wiki pages keep their tokens forever, so the card degrades to
// a muted, non-clickable "task not found".
//
// Built entirely from span elements so it is valid inside a <p> — markdown
// paragraphs and chat text bodies are its two homes.
export function TaskCard({ taskRef }: { taskRef: TaskRef }) {
  const { t } = useTranslation('browse')
  const taskQ = useTask(taskRef.projectId, taskRef.taskId)
  const [open, setOpen] = useState(false)

  const task = taskQ.data
  const gone = taskQ.error instanceof ApiError && taskQ.error.status === 404
  const title = task?.title ?? taskRef.title
  const statusColor = task ? STATUS_COLOR[task.status] : 'default'

  const subtitle = gone
    ? t('taskCard.notFound')
    : task
      ? [
          task.projectName,
          taskStatusLabel(t, task.status),
          task.assignee?.name || task.assignee?.email,
          task.dueDate ? t('taskCard.due', { date: fmtDue(task.dueDate) }) : undefined,
        ]
          .filter(Boolean)
          .join(' · ')
      : taskQ.isLoading
        ? t('common:loading')
        : t('taskCard.unavailable')

  const card = (
    <Box
      component="span"
      role={gone ? undefined : 'button'}
      tabIndex={gone ? undefined : 0}
      onClick={gone ? undefined : () => setOpen(true)}
      onKeyDown={
        gone
          ? undefined
          : (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                setOpen(true)
              }
            }
      }
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 1.25,
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
        bgcolor: 'background.paper',
        px: 1,
        py: 0.75,
        my: 0.25,
        maxWidth: 'min(420px, 100%)',
        cursor: gone ? 'default' : 'pointer',
        verticalAlign: 'middle',
        userSelect: 'none',
        opacity: gone ? 0.6 : 1,
        transition: 'border-color 120ms',
        '&:hover, &:focus-visible': gone
          ? undefined
          : {
              borderColor: 'primary.main',
              '& .taskcard-go': { opacity: 1 },
            },
      }}
    >
      <Box
        component="span"
        sx={{
          width: 40,
          height: 40,
          flexShrink: 0,
          borderRadius: 0.5,
          bgcolor: 'action.hover',
          color: statusColor === 'default' ? 'text.secondary' : `${statusColor}.main`,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <FontAwesomeIcon icon={faListCheck} style={{ fontSize: 17 }} />
      </Box>
      <Box component="span" sx={{ display: 'inline-flex', flexDirection: 'column', minWidth: 0 }}>
        <Typography
          component="span"
          variant="subtitle2"
          noWrap
          sx={{ lineHeight: 1.3, textDecoration: task?.status === 'done' ? 'line-through' : undefined }}
        >
          {task ? `${taskDisplayId(task)} ${title}` : title}
        </Typography>
        <Typography
          component="span"
          variant="caption"
          noWrap
          color={task && isOverdue(task.dueDate, task.status) ? 'error.main' : 'text.secondary'}
        >
          {subtitle}
        </Typography>
      </Box>
      <Box
        component="span"
        className="taskcard-go"
        sx={{
          ml: 0.5,
          color: 'primary.main',
          opacity: 0,
          transition: 'opacity 120ms',
          flexShrink: 0,
          display: 'inline-flex',
        }}
      >
        <FontAwesomeIcon icon={faArrowUpRightFromSquare} style={{ fontSize: 12 }} />
      </Box>
    </Box>
  )

  return (
    <>
      {gone ? card : <Tooltip title={t('taskCard.open')}>{card}</Tooltip>}
      {open && (
        <TaskViewDialog
          open={open}
          projectId={taskRef.projectId}
          taskId={taskRef.taskId}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}
