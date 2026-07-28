import { faListCheck } from '@fortawesome/free-solid-svg-icons'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { useTask } from '../../api/queries'
import { fmtDate } from '../../fmt'
import { taskPriorityLabel, taskStatusLabel } from '../../i18n/enums'
import { STATUS_COLOR, fmtDue, isOverdue } from '../../tasks/chips'
import { TaskViewDialog } from '../../tasks/TaskViewDialog'
import { taskDisplayId } from '../../tasks/types'
import { EntityCard, type CardMeta } from '../entitycard/EntityCard'
import type { TaskRef } from './taskref'

// TaskCard is the unfurled form of a TaskRef (see taskref.ts) — the same shared
// EntityCard as a document card, fed from the shared task query and opening the
// task's details in a dialog. A card can sit in any project's chat or wiki, so
// it must not depend on the browser's nav state.
//
// A deleted task's 404 is a designed state, not an error: chat logs and
// published wiki pages keep their tokens forever, so the card degrades to a
// muted, non-clickable "task not found".
export function TaskCard({ taskRef }: { taskRef: TaskRef }) {
  const { t } = useTranslation('browse')
  const taskQ = useTask(taskRef.projectId, taskRef.taskId)
  const [open, setOpen] = useState(false)

  const task = taskQ.data
  const gone = taskQ.error instanceof ApiError && taskQ.error.status === 404
  const title = task?.title ?? taskRef.title
  const statusColor = task ? STATUS_COLOR[task.status] : 'default'
  const overdue = !!task && isOverdue(task.dueDate, task.status)

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

  const meta: CardMeta[] = task
    ? ([
        { label: t('card.priority'), value: taskPriorityLabel(t, task.priority) },
        task.assignee?.name || task.assignee?.email
          ? { label: t('card.assignee'), value: task.assignee!.name || task.assignee!.email! }
          : null,
        task.dueDate ? { label: t('card.due'), value: fmtDue(task.dueDate) } : null,
        task.createdAt
          ? { label: t('card.created'), value: fmtDate(task.createdAt, { dateStyle: 'medium' }) }
          : null,
        task.updatedAt
          ? {
              label: t('card.modified'),
              value: fmtDate(task.updatedAt, { dateStyle: 'medium', timeStyle: 'short' }),
            }
          : null,
        task.createdBy?.name ? { label: t('card.modifiedBy'), value: task.createdBy.name } : null,
      ].filter(Boolean) as CardMeta[])
    : []

  return (
    <>
      <EntityCard
        title={task ? `${taskDisplayId(task)} ${title}` : title}
        strikeTitle={task?.status === 'done'}
        subtitle={subtitle}
        subtitleColor={overdue ? 'error.main' : undefined}
        icon={faListCheck}
        iconColor={statusColor === 'default' ? undefined : `${statusColor}.main`}
        meta={meta}
        metaLoading={taskQ.isLoading}
        onNavigate={gone ? undefined : () => setOpen(true)}
        dimmed={gone}
        selectable
      />
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
