import {
  DndContext,
  DragOverlay,
  PointerSensor,
  pointerWithin,
  rectIntersection,
  useDroppable,
  useSensor,
  useSensors,
  type CollisionDetection,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
} from '@dnd-kit/core'
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { Box, Paper, Stack, Typography } from '@mui/material'
import UserAvatar from '../components/UserAvatar'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useTaskMutations } from '../api/queries'
import { taskStatusLabel } from '../i18n/enums'
import { PriorityDot, fmtDue, isOverdue } from './chips'
import { TaskViewDialog } from './TaskViewDialog'
import {
  STATUSES,
  taskDisplayId,
  type Task,
  type TaskCaps,
  type TaskList,
  type TaskStatus,
} from './types'
import { localizeApiError } from '../i18n/apiError'

// Pointer-based collision detection. closestCorners (and closestCenter)
// measure the DRAGGED card's corners against each column's, so once columns
// are compressed narrower than the ~240px card, a middle column loses the
// corner-distance contest to its neighbours and never registers as the drop
// target (visible on narrow windows). pointerWithin keys off the cursor
// position instead, so column width is irrelevant; rectIntersection is the
// fallback for the gaps between droppables. A tightly-fitting card rect
// sorts ahead of its enclosing column, so hovering a card still yields the
// card (precise reorder) while empty column space yields the column.
const collisionDetection: CollisionDetection = (args) => {
  const pointer = pointerWithin(args)
  return pointer.length > 0 ? pointer : rectIntersection(args)
}

// TaskKanban is the project tab's board view: one column per status, cards
// ordered by rank. Drag between columns changes status; drag within a
// column reorders (both are the same PATCH {status, rank} — a drop
// computes its rank as the midpoint of its new neighbours). The move is
// applied optimistically to the tasks cache so the card doesn't snap back
// while the PATCH is in flight; settle-side invalidation reconciles.
// Clicking a card (PointerSensor's activation distance keeps clicks from
// starting drags) opens the task's details dialog.
export function TaskKanban({
  projectId,
  tasks,
  caps,
  loading,
  error,
}: {
  projectId: string
  tasks: Task[]
  caps?: TaskCaps
  loading: boolean
  error: Error | null
}) {
  const { t } = useTranslation('tasks')
  const qc = useQueryClient()
  const muts = useTaskMutations(projectId)
  const [openTaskId, setOpenTaskId] = useState<string | null>(null)
  const [activeId, setActiveId] = useState<string | null>(null)
  const [overColumn, setOverColumn] = useState<TaskStatus | null>(null)
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  const canWrite = caps ? caps.write : true

  const byStatus = new Map<TaskStatus, Task[]>(STATUSES.map((s) => [s, []]))
  for (const t of tasks) byStatus.get(t.status)?.push(t)
  for (const list of byStatus.values()) list.sort((a, b) => a.rank - b.rank || a.num - b.num)

  const activeTask = activeId ? tasks.find((t) => t.id === activeId) ?? null : null

  // The over target resolves to either a column surface ("col:<status>") or
  // a card; either way we want the column it belongs to. With cards present,
  // closestCorners reports the card — not the column droppable — so a
  // populated column's own isOver never fires. Resolving to the column here
  // (and driving the highlight from it) is what makes every column light up.
  function columnOf(overId: string | null): TaskStatus | null {
    if (!overId) return null
    if (overId.startsWith('col:')) return overId.slice(4) as TaskStatus
    return tasks.find((t) => t.id === overId)?.status ?? null
  }

  function handleDragStart(ev: DragStartEvent) {
    setActiveId(String(ev.active.id))
  }

  function handleDragOver(ev: DragOverEvent) {
    setOverColumn(columnOf(ev.over ? String(ev.over.id) : null))
  }

  function handleDragEnd(ev: DragEndEvent) {
    setActiveId(null)
    setOverColumn(null)
    const { active, over } = ev
    if (!over || active.id === over.id) return
    const task = tasks.find((t) => t.id === active.id)
    if (!task) return

    // The drop target is either a column surface ("col:<status>") or a card.
    const status = columnOf(String(over.id))
    if (!status) return
    const overTaskId = String(over.id).startsWith('col:') ? null : String(over.id)

    // Insertion point among the target column's cards (dragged card removed).
    const column = (byStatus.get(status) ?? []).filter((t) => t.id !== task.id)
    let index = column.length // column drop → append
    if (overTaskId) {
      index = column.findIndex((t) => t.id === overTaskId)
      if (index < 0) index = column.length
      // Dragging downward within a column lands *after* the card it's over
      // (the slot the card vacated), matching the sortable preview.
      else if (status === task.status && task.rank < column[index].rank) index += 1
    }
    const before = column[index - 1]?.rank
    const after = column[index]?.rank
    const rank =
      before === undefined && after === undefined
        ? 1024
        : before === undefined
          ? after! - 1024
          : after === undefined
            ? before + 1024
            : (before + after) / 2

    if (status === task.status && rank === task.rank) return

    // Optimistic: move the card in the cache now; the PATCH settles it.
    qc.setQueryData<TaskList>(['tasks', projectId], (cur) =>
      cur
        ? {
            ...cur,
            tasks: cur.tasks.map((t) => (t.id === task.id ? { ...t, status, rank } : t)),
          }
        : cur,
    )
    muts.update.mutate(
      { taskId: task.id, patch: { status, rank } },
      { onError: () => void qc.invalidateQueries({ queryKey: ['tasks', projectId] }) },
    )
  }

  if (error) {
    return (
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Typography variant="body2" color="error">
          {localizeApiError(t, error)}
        </Typography>
      </Box>
    )
  }

  return (
    <>
      <DndContext
        sensors={sensors}
        collisionDetection={collisionDetection}
        onDragStart={handleDragStart}
        onDragOver={handleDragOver}
        onDragEnd={handleDragEnd}
        onDragCancel={() => {
          setActiveId(null)
          setOverColumn(null)
        }}
      >
        <Box sx={{ flex: 1, minHeight: 0, display: 'flex', gap: 1, p: 1, overflowX: 'auto' }}>
          {STATUSES.map((status) => (
            <BoardColumn
              key={status}
              status={status}
              tasks={byStatus.get(status) ?? []}
              disabled={!canWrite}
              loading={loading}
              over={activeId !== null && overColumn === status}
              onOpen={setOpenTaskId}
            />
          ))}
        </Box>
        {/* The dragged card is rendered here, in a floating layer above the
            columns, so it is never clipped by a column's scroll container. */}
        <DragOverlay>{activeTask ? <CardFace task={activeTask} dragging /> : null}</DragOverlay>
      </DndContext>
      {openTaskId && (
        <TaskViewDialog
          open
          projectId={projectId}
          taskId={openTaskId}
          onClose={() => setOpenTaskId(null)}
        />
      )}
    </>
  )
}

function BoardColumn({
  status,
  tasks,
  disabled,
  loading,
  over,
  onOpen,
}: {
  status: TaskStatus
  tasks: Task[]
  disabled: boolean
  loading: boolean
  // Resolved by the parent so a populated column highlights too — its own
  // useDroppable().isOver never fires when the pointer lands on a card.
  over: boolean
  onOpen: (id: string) => void
}) {
  const { t } = useTranslation('tasks')
  // The droppable is the whole column (Paper), so the pointer test covers
  // the header and padding, not just the card list.
  const { setNodeRef } = useDroppable({ id: `col:${status}` })
  const isOver = over
  return (
    <Paper
      ref={setNodeRef}
      variant="outlined"
      sx={{
        width: 240,
        minWidth: 200,
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        bgcolor: 'action.hover',
        borderColor: isOver ? 'primary.main' : 'divider',
        transition: 'border-color 120ms',
      }}
    >
      <Stack direction="row" alignItems="center" spacing={0.75} sx={{ px: 1.25, py: 0.75 }}>
        <Typography variant="subtitle2" sx={{ fontSize: 12, textTransform: 'uppercase', letterSpacing: 0.5 }}>
          {taskStatusLabel(t, status)}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          {loading ? '…' : tasks.length}
        </Typography>
      </Stack>
      <SortableContext items={tasks.map((t) => t.id)} strategy={verticalListSortingStrategy}>
        <Box sx={{ flex: 1, minHeight: 60, overflowY: 'auto', px: 0.75, pb: 0.75 }}>
          <Stack spacing={0.75}>
            {tasks.map((t) => (
              <BoardCard key={t.id} task={t} disabled={disabled} onOpen={() => onOpen(t.id)} />
            ))}
          </Stack>
        </Box>
      </SortableContext>
    </Paper>
  )
}

function BoardCard({ task, disabled, onOpen }: { task: Task; disabled: boolean; onOpen: () => void }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: task.id,
    disabled,
  })
  return (
    <Box
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      onClick={onOpen}
      sx={{
        // While dragging, this stays as a dimmed placeholder in the column;
        // the floating DragOverlay copy is what follows the pointer.
        opacity: isDragging ? 0.4 : 1,
        transform: CSS.Transform.toString(transform),
        transition,
        cursor: disabled ? 'pointer' : 'grab',
      }}
    >
      <CardFace task={task} />
    </Box>
  )
}

// CardFace is the card's visual, shared by the in-column sortable card and
// the drag overlay so the picked-up card looks identical to its slot.
function CardFace({ task, dragging = false }: { task: Task; dragging?: boolean }) {
  const overdue = isOverdue(task.dueDate, task.status)
  const assigneeName = task.assignee?.name || task.assignee?.email || ''

  return (
    <Paper
      variant="outlined"
      sx={{
        p: 1,
        userSelect: 'none',
        cursor: dragging ? 'grabbing' : undefined,
        borderColor: dragging ? 'primary.main' : undefined,
        boxShadow: dragging ? 4 : 0,
        '&:hover': { borderColor: 'primary.main' },
      }}
    >
      <Typography
        variant="body2"
        sx={{
          lineHeight: 1.35,
          display: '-webkit-box',
          WebkitLineClamp: 2,
          WebkitBoxOrient: 'vertical',
          overflow: 'hidden',
          textDecoration: task.status === 'done' ? 'line-through' : undefined,
        }}
      >
        {task.title}
      </Typography>
      <Stack direction="row" alignItems="center" spacing={0.75} sx={{ mt: 0.75 }}>
        <Typography variant="caption" color="text.secondary">
          {taskDisplayId(task)}
        </Typography>
        <PriorityDot priority={task.priority} />
        {task.dueDate && (
          <Typography variant="caption" color={overdue ? 'error.main' : 'text.secondary'}>
            {fmtDue(task.dueDate)}
          </Typography>
        )}
        <Box sx={{ flex: 1 }} />
        {assigneeName && (
          <UserAvatar
            id={task.assignee?.id}
            name={assigneeName}
            size={20}
            tooltip={assigneeName}
          />
        )}
      </Stack>
    </Paper>
  )
}
