import { faDiagramProject, faPaperclip, faPen, faTrash } from '@fortawesome/free-solid-svg-icons'
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
import { useAuthMe, useTaskMutations } from '../api/queries'
import { docRefFromItem, encodeDocRef } from '../components/doccard/docref'
import { HubBrowserDialog } from '../components/hubbrowser/HubBrowserDialog'
import { RefCard } from '../components/RefCard'
import { ProductionRefDialog } from '../production/ProductionRefDialog'
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
  const me = useAuthMe().data?.user
  const muts = useTaskMutations(task.projectId)
  const [editOpen, setEditOpen] = useState(false)
  const [attachOpen, setAttachOpen] = useState(false)
  const [prodPickOpen, setProdPickOpen] = useState(false)

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
    if (!window.confirm(`Delete ${taskDisplayId(task)} "${task.title}"? This cannot be undone.`)) return
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
          <Tooltip title="Edit task">
            <IconButton size="small" onClick={() => setEditOpen(true)} aria-label="Edit task">
              <FontAwesomeIcon icon={faPen} style={{ fontSize: 14 }} />
            </IconButton>
          </Tooltip>
        )}
        {canDelete && (
          <Tooltip title="Delete task">
            <IconButton size="small" onClick={confirmDelete} aria-label="Delete task">
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
        <FieldRow label="Status" value={<StatusChip status={task.status} />} />
        <FieldRow label="Priority" value={<PriorityChip priority={task.priority} />} />
        <FieldRow label="Project" value={task.projectName} />
        <FieldRow
          label="Assignee"
          value={task.assignee ? task.assignee.name || task.assignee.email : undefined}
        />
        <FieldRow
          label="Due"
          value={
            task.dueDate ? (
              <Typography
                component="span"
                variant="body2"
                color={isOverdue(task.dueDate, task.status) ? 'error.main' : undefined}
              >
                {fmtDue(task.dueDate)}
                {isOverdue(task.dueDate, task.status) ? ' (overdue)' : ''}
              </Typography>
            ) : undefined
          }
        />
        <FieldRow
          label="Schedule"
          value={
            task.startDate && task.endDate ? (
              <Typography component="span" variant="body2">
                {fmtDue(task.startDate)} → {fmtDue(task.endDate)}
                {task.milestone ? ' (milestone)' : ''}
                {task.stage ? ` · ${task.stage}` : ''}
                {!task.milestone && (task.progress ?? 0) > 0 ? ` · ${task.progress}%` : ''}
              </Typography>
            ) : undefined
          }
        />
        <FieldRow
          label="Depends on"
          value={task.dependsOn.length ? task.dependsOn.map((id) => `T-${id.replace(/^t/, '')}`).join(', ') : undefined}
        />
        <FieldRow label="Created by" value={task.createdBy.name || task.createdBy.email} />
        <FieldRow label="Created" value={fmtChatTime(task.createdAt)} />
        <FieldRow label="Updated" value={fmtChatTime(task.updatedAt)} />
      </Box>

      {task.description && (
        <Box sx={{ mt: 2 }}>
          <Typography variant="overline" color="text.secondary">
            Description
          </Typography>
          <Markdown>{task.description}</Markdown>
        </Box>
      )}

      <Box sx={{ mt: 2 }}>
        <Stack direction="row" alignItems="center" spacing={1}>
          <Typography variant="overline" color="text.secondary" sx={{ flex: 1 }}>
            Attached documents
          </Typography>
          {canWrite && (
            <>
              <Button
                size="small"
                startIcon={<FontAwesomeIcon icon={faPaperclip} style={{ fontSize: 12 }} />}
                onClick={() => setAttachOpen(true)}
                disabled={muts.update.isPending}
              >
                Attach
              </Button>
              <Button
                size="small"
                startIcon={<FontAwesomeIcon icon={faDiagramProject} style={{ fontSize: 12 }} />}
                onClick={() => setProdPickOpen(true)}
                disabled={muts.update.isPending}
              >
                Link job/batch
              </Button>
            </>
          )}
        </Stack>
        {task.docRefs.length === 0 ? (
          <Typography variant="body2" color="text.secondary">
            Nothing attached.
          </Typography>
        ) : (
          <Stack spacing={0.5} alignItems="flex-start">
            {task.docRefs.map((token) => (
              <Stack key={token} direction="row" alignItems="center" spacing={0.5} sx={{ maxWidth: '100%' }}>
                <RefCard token={token} />
                {canWrite && (
                  <Tooltip title="Remove attachment">
                    <IconButton
                      size="small"
                      onClick={() => removeDoc(token)}
                      disabled={muts.update.isPending}
                      aria-label="Remove attachment"
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
          title="Attach a document"
          pickLabel="Attach"
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
