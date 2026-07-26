import {
  Alert,
  Autocomplete,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControlLabel,
  MenuItem,
  Slider,
  Stack,
  Switch,
  TextField,
  Typography,
} from '@mui/material'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useChatMembers, useTaskMutations, useTasks } from '../api/queries'
import { taskPriorityLabel, taskStatusLabel } from '../i18n/enums'
import {
  PRIORITIES,
  STATUSES,
  taskDisplayId,
  type Task,
  type TaskPriority,
  type TaskStatus,
  type TaskUser,
} from './types'

// TaskEditDialog is the full create/edit form. The assignee picker feeds
// off the same project roster chat uses (useChatMembers) — no parallel
// membership source. Create mode needs hubId/projectName because the
// project's task file self-describes for cross-project listings.
export function TaskEditDialog({
  open,
  onClose,
  projectId,
  hubId,
  projectName,
  task,
  onSaved,
}: {
  open: boolean
  onClose: () => void
  projectId: string
  hubId: string
  projectName: string
  task?: Task // edit mode when present
  onSaved?: (t: Task) => void
}) {
  const { t } = useTranslation('tasks')
  const muts = useTaskMutations(projectId)
  const membersQ = useChatMembers(projectId, open)
  // The project's tasks feed the dependency picker and the stage
  // suggestions; the query is already warm from the tab.
  const tasksQ = useTasks(open ? projectId : null, open)

  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [status, setStatus] = useState<TaskStatus>('todo')
  const [priority, setPriority] = useState<TaskPriority>('medium')
  const [dueDate, setDueDate] = useState('')
  const [assigneeId, setAssigneeId] = useState('')
  const [startDate, setStartDate] = useState('')
  const [endDate, setEndDate] = useState('')
  const [milestone, setMilestone] = useState(false)
  const [progress, setProgress] = useState(0)
  const [stage, setStage] = useState('')
  const [dependsOn, setDependsOn] = useState<string[]>([])

  // Re-seed the form each time the dialog opens (it stays mounted across
  // opens in some hosts).
  useEffect(() => {
    if (!open) return
    setTitle(task?.title ?? '')
    setDescription(task?.description ?? '')
    setStatus(task?.status ?? 'todo')
    setPriority(task?.priority ?? 'medium')
    setDueDate(task?.dueDate ?? '')
    setAssigneeId(task?.assignee?.id ?? '')
    setStartDate(task?.startDate ?? '')
    setEndDate(task?.endDate ?? '')
    setMilestone(task?.milestone ?? false)
    setProgress(task?.progress ?? 0)
    setStage(task?.stage ?? '')
    setDependsOn(task?.dependsOn ?? [])
    muts.create.reset()
    muts.update.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, task?.id])

  const members = membersQ.data ?? []
  // Keep a group-derived assignee (not on the individual roster) pickable.
  const extraAssignee =
    task?.assignee && !members.some((m) => m.userId === task.assignee!.id) ? task.assignee : null

  const pending = muts.create.isPending || muts.update.isPending
  const err = (task ? muts.update.error : muts.create.error) as Error | null

  const projectTasks = tasksQ.data?.tasks ?? []
  // Existing stage names, for the freeSolo suggestions.
  const stageOptions = [...new Set(projectTasks.map((t) => t.stage).filter(Boolean))] as string[]
  // Dependency options: every other task in the project.
  const depOptions = projectTasks.filter((t) => t.id !== task?.id)
  const depLabel = (id: string) => {
    const t = projectTasks.find((x) => x.id === id)
    return t ? `${taskDisplayId(t)} ${t.title}` : id
  }

  // A one-sided schedule is invalid — block Save and say why.
  const effEnd = milestone && startDate ? startDate : endDate
  const oneSided = !!startDate !== !!effEnd
  const badOrder = !!startDate && !!effEnd && effEnd < startDate

  function resolveAssignee(): TaskUser | undefined {
    if (!assigneeId) return undefined
    const m = members.find((mm) => mm.userId === assigneeId)
    if (m) return { id: m.userId, name: m.name, email: m.email }
    if (extraAssignee && extraAssignee.id === assigneeId) return extraAssignee
    return { id: assigneeId }
  }

  function save() {
    const assignee = resolveAssignee()
    const done = (t: Task) => {
      onClose()
      onSaved?.(t)
    }
    const scheduled = !!startDate && !!effEnd
    if (task) {
      muts.update.mutate(
        {
          taskId: task.id,
          patch: {
            title,
            description,
            status,
            priority,
            ...(dueDate ? { dueDate } : { clearDueDate: true }),
            ...(assignee ? { assignee } : { clearAssignee: true }),
            // Unschedule = clear both date fields; the task returns to the
            // Gantt backlog.
            ...(scheduled
              ? { startDate, endDate: effEnd, milestone }
              : { clearSchedule: true }),
            progress,
            stage,
            dependsOn,
          },
        },
        { onSuccess: done },
      )
    } else {
      muts.create.mutate(
        {
          hubId,
          projectName,
          title,
          description,
          status,
          priority,
          dueDate,
          assignee,
          ...(scheduled ? { startDate, endDate: effEnd, milestone } : {}),
          progress,
          stage,
          dependsOn,
        },
        { onSuccess: done },
      )
    }
  }

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle>
        {task ? t('editDialog.editTitle', { id: taskDisplayId(task) }) : t('editDialog.createTitle')}
      </DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{ mt: 0.5 }}>
          {err && <Alert severity="error">{err.message}</Alert>}
          <TextField
            label={t('editDialog.titleLabel')}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            autoFocus
            fullWidth
            size="small"
            inputProps={{ maxLength: 200 }}
          />
          <TextField
            label={t('editDialog.descriptionLabel')}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            fullWidth
            size="small"
            multiline
            minRows={3}
            maxRows={10}
          />
          <Stack direction="row" spacing={2}>
            <TextField
              select
              label={t('editDialog.statusLabel')}
              value={status}
              onChange={(e) => setStatus(e.target.value as TaskStatus)}
              size="small"
              sx={{ flex: 1 }}
            >
              {STATUSES.map((s) => (
                <MenuItem key={s} value={s}>
                  {taskStatusLabel(t, s)}
                </MenuItem>
              ))}
            </TextField>
            <TextField
              select
              label={t('editDialog.priorityLabel')}
              value={priority}
              onChange={(e) => setPriority(e.target.value as TaskPriority)}
              size="small"
              sx={{ flex: 1 }}
            >
              {PRIORITIES.map((p) => (
                <MenuItem key={p} value={p}>
                  {taskPriorityLabel(t, p)}
                </MenuItem>
              ))}
            </TextField>
          </Stack>
          <Stack direction="row" spacing={2}>
            <TextField
              label={t('editDialog.dueDateLabel')}
              type="date"
              value={dueDate}
              onChange={(e) => setDueDate(e.target.value)}
              size="small"
              sx={{ flex: 1 }}
              InputLabelProps={{ shrink: true }}
            />
            <TextField
              select
              label={t('editDialog.assigneeLabel')}
              value={assigneeId}
              onChange={(e) => setAssigneeId(e.target.value)}
              size="small"
              sx={{ flex: 1 }}
              disabled={membersQ.isLoading}
            >
              <MenuItem value="">{t('editDialog.unassigned')}</MenuItem>
              {extraAssignee && (
                <MenuItem value={extraAssignee.id}>
                  {extraAssignee.name || extraAssignee.email || extraAssignee.id}
                </MenuItem>
              )}
              {members.map((m) => (
                <MenuItem key={m.userId} value={m.userId}>
                  {m.name || m.email || m.userId}
                </MenuItem>
              ))}
            </TextField>
          </Stack>

          <Divider textAlign="left">
            <Typography variant="caption" color="text.secondary">
              {t('editDialog.scheduleSection')}
            </Typography>
          </Divider>
          <Stack direction="row" spacing={2} alignItems="center">
            <TextField
              label={t('editDialog.startDateLabel')}
              type="date"
              value={startDate}
              onChange={(e) => setStartDate(e.target.value)}
              size="small"
              sx={{ flex: 1 }}
              InputLabelProps={{ shrink: true }}
              error={oneSided && !startDate}
            />
            <TextField
              label={milestone ? t('editDialog.endDateMilestoneLabel') : t('editDialog.endDateLabel')}
              type="date"
              value={milestone ? startDate : endDate}
              onChange={(e) => setEndDate(e.target.value)}
              size="small"
              sx={{ flex: 1 }}
              InputLabelProps={{ shrink: true }}
              disabled={milestone}
              error={(oneSided && !effEnd) || badOrder}
              helperText={badOrder ? t('editDialog.endsBeforeStarts') : undefined}
            />
            <FormControlLabel
              control={
                <Switch
                  size="small"
                  checked={milestone}
                  onChange={(e) => setMilestone(e.target.checked)}
                  disabled={!startDate}
                />
              }
              label={<Typography variant="body2">{t('editDialog.milestoneLabel')}</Typography>}
              sx={{ flexShrink: 0, mr: 0 }}
            />
          </Stack>
          {oneSided && (
            <Typography variant="caption" color="text.secondary">
              {t('editDialog.oneSidedHint')}
            </Typography>
          )}
          {!milestone && (
            <Stack direction="row" spacing={2} alignItems="center">
              <Typography variant="body2" color="text.secondary" sx={{ width: 90, flexShrink: 0 }}>
                {t('editDialog.progressLabel', { value: progress })}
              </Typography>
              <Slider
                size="small"
                value={progress}
                onChange={(_, v) => setProgress(v as number)}
                step={5}
                min={0}
                max={100}
                valueLabelDisplay="auto"
              />
            </Stack>
          )}
          <Stack direction="row" spacing={2}>
            <Autocomplete
              freeSolo
              options={stageOptions}
              value={stage}
              onInputChange={(_, v) => setStage(v)}
              size="small"
              sx={{ flex: 1 }}
              renderInput={(params) => (
                <TextField {...params} label={t('editDialog.stageLabel')} placeholder={t('editDialog.stagePlaceholder')} />
              )}
            />
            <Autocomplete
              multiple
              options={depOptions.map((t) => t.id)}
              getOptionLabel={depLabel}
              value={dependsOn}
              onChange={(_, v) => setDependsOn(v)}
              size="small"
              sx={{ flex: 1.4 }}
              renderTags={(value, getTagProps) =>
                value.map((id, index) => (
                  <Chip
                    {...getTagProps({ index })}
                    key={id}
                    label={depLabel(id)}
                    size="small"
                  />
                ))
              }
              renderInput={(params) => (
                <TextField {...params} label={t('editDialog.dependsOnLabel')} placeholder={t('editDialog.dependsOnPlaceholder')} />
              )}
            />
          </Stack>
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={pending}>
          {t('common:cancel')}
        </Button>
        <Button
          variant="contained"
          onClick={save}
          disabled={pending || !title.trim() || oneSided || badOrder}
        >
          {task ? t('common:save') : t('common:create')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
