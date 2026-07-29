import {
  faChalkboard,
  faDiagramProject,
  faPaperclip,
  faPen,
  faTrash,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Alert,
  Box,
  Button,
  Chip,
  IconButton,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthMe, useTaskMutations } from '../api/queries'
import { docRefFromItem, encodeDocRef } from '../components/doccard/docref'
import { HubBrowserDialog } from '../components/hubbrowser/HubBrowserDialog'
import { RefCard } from '../components/RefCard'
import { encodeWhiteboardRef, whiteboardRefFromBoard } from '../components/whiteboardcard/wbref'
import { ProductionRefDialog } from '../production/ProductionRefDialog'
import { AttachWhiteboardDialog } from '../whiteboards/AttachWhiteboardDialog'
import { Markdown } from '../wiki/Markdown'
import { fmtChatTime } from '../chat/fmt'
import { PriorityChip, StatusChip, fmtDue, isOverdue } from './chips'
import { TaskEditDialog } from './TaskEditDialog'
import { taskDisplayId, type Task, type TaskCaps } from './types'

// TaskDetails is the shared detail pane for one task — the project Tasks
// tab, the cross-project Tasks screen, and the TaskViewDialog (task cards)
// all render it. It owns its own mutations (a Task carries its projectId),
// so hosts only supply the task and, when they know them, the caller's
// capabilities; without caps the write affordances stay enabled and a 403
// surfaces from the server (the cross-project screen has no cheap way to
// know per-project roles).
export function TaskDetails({
  task,
  caps,
  onDeleted,
}: {
  task: Task
  caps?: TaskCaps
  onDeleted?: () => void
}) {
  const { t } = useTranslation('tasks')
  const me = useAuthMe().data?.user
  const muts = useTaskMutations(task.projectId)
  const [editOpen, setEditOpen] = useState(false)
  const [attachOpen, setAttachOpen] = useState(false)
  const [prodPickOpen, setProdPickOpen] = useState(false)
  const [boardPickOpen, setBoardPickOpen] = useState(false)

  const addRefToken = (token: string) => {
    if (task.docRefs.includes(token)) return
    muts.update.mutate({ taskId: task.id, patch: { docRefs: [...task.docRefs, token] } })
  }

  const canWrite = caps ? caps.write : true
  const canDelete = (caps?.moderate ?? false) || (!!me && me.id === task.createdBy.id)

  const mutErr = (muts.update.error ?? muts.remove.error) as Error | null

  function removeDoc(ref: string) {
    muts.update.mutate({
      taskId: task.id,
      patch: { docRefs: task.docRefs.filter((r) => r !== ref) },
    })
  }

  function confirmDelete() {
    if (!window.confirm(t('details.deleteConfirm', { id: taskDisplayId(task), title: task.title }))) return
    muts.remove.mutate(task.id, { onSuccess: onDeleted })
  }

  return (
    <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto', p: 2 }}>
      <Stack direction="row" spacing={1} alignItems="flex-start">
        <Chip label={taskDisplayId(task)} size="small" variant="outlined" sx={{ mt: 0.25, flexShrink: 0 }} />
        <Typography variant="h6" sx={{ flex: 1, minWidth: 0, lineHeight: 1.3, wordBreak: 'break-word' }}>
          {task.title}
        </Typography>
        {canWrite && (
          <Tooltip title={t('details.editTask')}>
            <IconButton size="small" onClick={() => setEditOpen(true)} aria-label={t('details.editTask')}>
              <FontAwesomeIcon icon={faPen} style={{ fontSize: 14 }} />
            </IconButton>
          </Tooltip>
        )}
        {canDelete && (
          <Tooltip title={t('details.deleteTask')}>
            <IconButton size="small" onClick={confirmDelete} aria-label={t('details.deleteTask')}>
              <FontAwesomeIcon icon={faTrash} style={{ fontSize: 14 }} />
            </IconButton>
          </Tooltip>
        )}
      </Stack>

      {mutErr && (
        <Alert severity="error" sx={{ mt: 1.5 }} onClose={() => { muts.update.reset(); muts.remove.reset() }}>
          {mutErr.message}
        </Alert>
      )}

      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'minmax(84px, auto) 1fr',
          columnGap: 2,
          rowGap: 0.75,
          mt: 2,
        }}
      >
        <FieldRow label={t('details.statusLabel')} value={<StatusChip status={task.status} />} />
        <FieldRow label={t('details.priorityLabel')} value={<PriorityChip priority={task.priority} />} />
        <FieldRow label={t('details.projectLabel')} value={task.projectName} />
        <FieldRow
          label={t('details.assigneeLabel')}
          value={task.assignee ? task.assignee.name || task.assignee.email : undefined}
        />
        <FieldRow
          label={t('details.dueLabel')}
          value={
            task.dueDate ? (
              <Typography
                component="span"
                variant="body2"
                color={isOverdue(task.dueDate, task.status) ? 'error.main' : undefined}
              >
                {fmtDue(task.dueDate)}
                {isOverdue(task.dueDate, task.status) ? ` ${t('details.overdueSuffix')}` : ''}
              </Typography>
            ) : undefined
          }
        />
        <FieldRow
          label={t('details.scheduleLabel')}
          value={
            task.startDate && task.endDate ? (
              <Typography component="span" variant="body2">
                {fmtDue(task.startDate)} → {fmtDue(task.endDate)}
                {task.milestone ? ` ${t('details.milestoneSuffix')}` : ''}
                {task.stage ? ` · ${task.stage}` : ''}
                {!task.milestone && (task.progress ?? 0) > 0 ? ` · ${task.progress}%` : ''}
              </Typography>
            ) : undefined
          }
        />
        <FieldRow
          label={t('details.dependsOnLabel')}
          value={task.dependsOn.length ? task.dependsOn.map((id) => `T-${id.replace(/^t/, '')}`).join(', ') : undefined}
        />
        <FieldRow label={t('details.createdByLabel')} value={task.createdBy.name || task.createdBy.email} />
        <FieldRow label={t('details.createdLabel')} value={fmtChatTime(task.createdAt)} />
        <FieldRow label={t('details.updatedLabel')} value={fmtChatTime(task.updatedAt)} />
      </Box>

      {task.description && (
        <Box sx={{ mt: 2 }}>
          <Typography variant="overline" color="text.secondary">
            {t('details.descriptionHeading')}
          </Typography>
          <Markdown>{task.description}</Markdown>
        </Box>
      )}

      <Box sx={{ mt: 2 }}>
        <Stack direction="row" alignItems="center" spacing={1}>
          <Typography variant="overline" color="text.secondary" sx={{ flex: 1 }}>
            {t('details.attachedHeading')}
          </Typography>
          {canWrite && (
            <>
              <Button
                size="small"
                startIcon={<FontAwesomeIcon icon={faPaperclip} style={{ fontSize: 12 }} />}
                onClick={() => setAttachOpen(true)}
                disabled={muts.update.isPending}
              >
                {t('details.attach')}
              </Button>
              <Button
                size="small"
                startIcon={<FontAwesomeIcon icon={faDiagramProject} style={{ fontSize: 12 }} />}
                onClick={() => setProdPickOpen(true)}
                disabled={muts.update.isPending}
              >
                {t('details.linkJobBatch')}
              </Button>
              <Button
                size="small"
                startIcon={<FontAwesomeIcon icon={faChalkboard} style={{ fontSize: 12 }} />}
                onClick={() => setBoardPickOpen(true)}
                disabled={muts.update.isPending}
              >
                {t('details.linkWhiteboard')}
              </Button>
            </>
          )}
        </Stack>
        {task.docRefs.length === 0 ? (
          <Typography variant="body2" color="text.secondary">
            {t('details.nothingAttached')}
          </Typography>
        ) : (
          <Stack spacing={0.5} alignItems="flex-start">
            {task.docRefs.map((token) => (
              <Stack key={token} direction="row" alignItems="center" spacing={0.5} sx={{ maxWidth: '100%' }}>
                <RefCard token={token} />
                {canWrite && (
                  <Tooltip title={t('details.removeAttachment')}>
                    <IconButton
                      size="small"
                      onClick={() => removeDoc(token)}
                      disabled={muts.update.isPending}
                      aria-label={t('details.removeAttachment')}
                    >
                      <FontAwesomeIcon icon={faTrash} style={{ fontSize: 12 }} />
                    </IconButton>
                  </Tooltip>
                )}
              </Stack>
            ))}
          </Stack>
        )}
      </Box>

      {editOpen && (
        <TaskEditDialog
          open={editOpen}
          onClose={() => setEditOpen(false)}
          projectId={task.projectId}
          hubId={task.hubId}
          projectName={task.projectName}
          task={task}
        />
      )}
      {attachOpen && (
        <HubBrowserDialog
          open={attachOpen}
          hubId={task.hubId || null}
          title={t('details.attachDocTitle')}
          pickLabel={t('details.attach')}
          onClose={() => setAttachOpen(false)}
          onPick={(pick) => {
            setAttachOpen(false)
            if (!pick.item) return
            addRefToken(encodeDocRef(docRefFromItem(pick.hubId, pick.item)))
          }}
        />
      )}
      {prodPickOpen && (
        <ProductionRefDialog
          open={prodPickOpen}
          projectId={task.projectId}
          hubId={task.hubId}
          projectName={task.projectName}
          onClose={() => setProdPickOpen(false)}
          onPick={(token) => addRefToken(token)}
        />
      )}
      {boardPickOpen && (
        <AttachWhiteboardDialog
          open={boardPickOpen}
          projectId={task.projectId}
          onClose={() => setBoardPickOpen(false)}
          onPick={(board) => {
            setBoardPickOpen(false)
            addRefToken(encodeWhiteboardRef(whiteboardRefFromBoard(board)))
          }}
        />
      )}
    </Box>
  )
}

function FieldRow({ label, value }: { label: string; value: ReactNode }) {
  if (value === undefined || value === '' || value === null) return null
  return (
    <>
      <Typography variant="caption" color="text.secondary" sx={{ pt: 0.25 }}>
        {label}
      </Typography>
      <Typography component="div" variant="body2" sx={{ wordBreak: 'break-word' }}>
        {value}
      </Typography>
    </>
  )
}
