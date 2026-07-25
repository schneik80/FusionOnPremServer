import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTaskMutations } from '../../api/queries'
import type { Task, TaskList, TaskPatch } from '../types'
import type { BarAction } from './GanttBar'
import {
  addDays,
  dateToX,
  fmtDay,
  parseDay,
  snapDays,
  xToDate,
  type GanttScale,
} from './ganttMath'

// One drag at a time; everything the render needs to preview it lives here.
// Previews are LOCAL state layered over query data (never written into the
// cache mid-drag), so the 15 s poll can't stomp an in-flight drag.
export interface GanttDrag {
  action: BarAction | 'schedule'
  taskId?: string
  // move/resize/progress previews:
  startDate?: string
  endDate?: string
  progress?: number
  // stageMove:
  stage?: string
  stageTaskIds?: string[]
  shiftDays?: number
  // link:
  linkFromId?: string
  linkTargetId?: string
  // schedule (drag-from-backlog):
  ghostTask?: Task
  // pointer in body-SVG coordinates (link line, ghost bar):
  pointerX: number
  pointerY: number
  moved: boolean
}

const DRAG_THRESHOLD_PX = 4

// useGanttDrag is the gantt-task-react state machine rebuilt on Pointer
// Events: pointerdown records the grab, window-level move/up listeners
// preview and commit, Escape cancels, and every listener is removed in the
// effect cleanup (their StrictMode double-fire bug came from skipping that).
// Commits follow the Kanban optimistic pattern: setQueryData now, mutate,
// invalidate on error so the bar snaps back.
export function useGanttDrag({
  projectId,
  scale,
  tasks,
  writable,
  svgRef,
  onClickTask,
}: {
  projectId: string
  scale: GanttScale
  tasks: Task[]
  writable: boolean
  svgRef: React.RefObject<SVGSVGElement | null>
  onClickTask: (taskId: string) => void
}) {
  const qc = useQueryClient()
  const muts = useTaskMutations(projectId)
  const [drag, setDrag] = useState<GanttDrag | null>(null)
  // The pointerdown snapshot lives in a ref: move handlers need it without
  // re-subscribing, and it must survive re-renders untouched.
  const origin = useRef<{ clientX: number; clientY: number; drag: GanttDrag } | null>(null)

  const svgPoint = useCallback(
    (clientX: number, clientY: number) => {
      const box = svgRef.current?.getBoundingClientRect()
      return box ? { x: clientX - box.left, y: clientY - box.top } : { x: 0, y: 0 }
    },
    [svgRef],
  )

  const begin = useCallback(
    (e: React.PointerEvent, d: Omit<GanttDrag, 'pointerX' | 'pointerY' | 'moved'>) => {
      if (!writable) return
      if (e.button !== 0) return
      e.preventDefault()
      e.stopPropagation()
      const p = svgPoint(e.clientX, e.clientY)
      const full: GanttDrag = { ...d, pointerX: p.x, pointerY: p.y, moved: false }
      origin.current = { clientX: e.clientX, clientY: e.clientY, drag: full }
      setDrag(full)
    },
    [writable, svgPoint],
  )

  const startBarDrag = useCallback(
    (e: React.PointerEvent, task: Task, action: BarAction) => {
      if (action === 'stageMove') return // stages use startStageDrag
      begin(e, {
        action,
        taskId: task.id,
        startDate: task.startDate,
        endDate: task.endDate,
        progress: task.status === 'done' ? 100 : task.progress ?? 0,
        linkFromId: action === 'link' ? task.id : undefined,
      })
    },
    [begin],
  )

  const startStageDrag = useCallback(
    (e: React.PointerEvent, stage: string, stageTaskIds: string[]) => {
      begin(e, { action: 'stageMove', stage, stageTaskIds, shiftDays: 0 })
    },
    [begin],
  )

  const startBacklogDrag = useCallback(
    (e: React.PointerEvent, task: Task) => {
      begin(e, { action: 'schedule', taskId: task.id, ghostTask: task })
    },
    [begin],
  )

  // commit applies the finished drag through the optimistic cache pattern.
  const commit = useCallback(
    (d: GanttDrag) => {
      const invalidate = () => void qc.invalidateQueries({ queryKey: ['tasks', projectId] })
      const patchCache = (taskId: string, apply: (t: Task) => Task) =>
        qc.setQueryData<TaskList>(['tasks', projectId], (cur) =>
          cur ? { ...cur, tasks: cur.tasks.map((t) => (t.id === taskId ? apply(t) : t)) } : cur,
        )
      const task = tasks.find((t) => t.id === d.taskId)

      switch (d.action) {
        case 'move':
        case 'resizeStart':
        case 'resizeEnd': {
          if (!task || (d.startDate === task.startDate && d.endDate === task.endDate)) return
          const patch: TaskPatch = { startDate: d.startDate, endDate: d.endDate }
          patchCache(task.id, (t) => ({ ...t, ...patch }))
          muts.update.mutate({ taskId: task.id, patch }, { onError: invalidate })
          return
        }
        case 'progress': {
          if (!task || d.progress === undefined || d.progress === (task.progress ?? 0)) return
          patchCache(task.id, (t) => ({ ...t, progress: d.progress }))
          muts.update.mutate(
            { taskId: task.id, patch: { progress: d.progress } },
            { onError: invalidate },
          )
          return
        }
        case 'link': {
          const target = tasks.find((t) => t.id === d.linkTargetId)
          if (!d.linkFromId || !target || target.id === d.linkFromId) return
          if (target.dependsOn.includes(d.linkFromId)) return
          const dependsOn = [...target.dependsOn, d.linkFromId]
          // The server is the cycle authority: a rejected link invalidates
          // and the optimistic arrow vanishes (Kanban snap-back posture).
          patchCache(target.id, (t) => ({ ...t, dependsOn }))
          muts.update.mutate({ taskId: target.id, patch: { dependsOn } }, { onError: invalidate })
          return
        }
        case 'stageMove': {
          if (!d.shiftDays || !d.stageTaskIds?.length) return
          const days = d.shiftDays
          const ids = new Set(d.stageTaskIds)
          qc.setQueryData<TaskList>(['tasks', projectId], (cur) =>
            cur
              ? {
                  ...cur,
                  tasks: cur.tasks.map((t) =>
                    ids.has(t.id) && t.startDate && t.endDate
                      ? {
                          ...t,
                          startDate: fmtDay(addDays(parseDay(t.startDate), days)),
                          endDate: fmtDay(addDays(parseDay(t.endDate), days)),
                        }
                      : t,
                  ),
                }
              : cur,
          )
          muts.shift.mutate({ taskIds: d.stageTaskIds, days }, { onError: invalidate })
          return
        }
        case 'schedule': {
          if (!task || !d.startDate || !d.endDate) return
          const patch: TaskPatch = { startDate: d.startDate, endDate: d.endDate }
          patchCache(task.id, (t) => ({ ...t, ...patch }))
          muts.update.mutate({ taskId: task.id, patch }, { onError: invalidate })
          return
        }
      }
    },
    [muts.shift, muts.update, projectId, qc, tasks],
  )

  useEffect(() => {
    if (!drag) return
    const o = origin.current
    if (!o) return

    const onMove = (e: PointerEvent) => {
      e.preventDefault()
      const dx = e.clientX - o.clientX
      const dy = e.clientY - o.clientY
      const moved =
        o.drag.moved || Math.abs(dx) >= DRAG_THRESHOLD_PX || Math.abs(dy) >= DRAG_THRESHOLD_PX
      const p = svgPoint(e.clientX, e.clientY)
      const base = o.drag
      const days = snapDays(dx, scale)

      setDrag(() => {
        const next: GanttDrag = { ...base, pointerX: p.x, pointerY: p.y, moved }
        if (!moved) return next
        switch (base.action) {
          case 'move': {
            next.startDate = shiftDate(base.startDate, days)
            next.endDate = shiftDate(base.endDate, days)
            break
          }
          case 'resizeStart': {
            const start = shiftDate(base.startDate, days)
            next.startDate = start && base.endDate && start > base.endDate ? base.endDate : start
            break
          }
          case 'resizeEnd': {
            const end = shiftDate(base.endDate, days)
            next.endDate = end && base.startDate && end < base.startDate ? base.startDate : end
            break
          }
          case 'progress': {
            if (base.startDate && base.endDate) {
              const x1 = dateToX(scale, parseDay(base.startDate))
              const x2 = dateToX(scale, addDays(parseDay(base.endDate), 1))
              const frac = (p.x - x1) / Math.max(1, x2 - x1)
              next.progress = Math.min(100, Math.max(0, Math.round(frac * 20) * 5))
            }
            break
          }
          case 'link': {
            next.linkTargetId = barIdAtPoint(e.clientX, e.clientY, base.taskId)
            break
          }
          case 'stageMove': {
            next.shiftDays = days
            break
          }
          case 'schedule': {
            // Ghost bar: keep the task's current duration (default 3 days),
            // anchored at the day under the pointer.
            const dur = 2 // extra days beyond the start
            const start = xToDate(scale, p.x)
            next.startDate = fmtDay(start)
            next.endDate = fmtDay(addDays(start, dur))
            break
          }
        }
        return next
      })
      o.drag = { ...o.drag, moved }
    }

    const onUp = (e: PointerEvent) => {
      // Escape (or anything else) may already have torn the drag down; the
      // origin ref is the authoritative "still dragging" bit, so a stray
      // pointerup after cancel can never commit.
      if (!origin.current) return
      const last = latest.current
      cleanupState()
      if (!last) return
      if (!last.moved) {
        // A press that never crossed the threshold is a click.
        if ((last.action === 'move' || last.action === 'schedule') && last.taskId) {
          onClickTask(last.taskId)
        }
        return
      }
      if (last.action === 'link') {
        last.linkTargetId = barIdAtPoint(e.clientX, e.clientY, last.taskId)
      }
      if (last.action === 'schedule') {
        // Only schedule when released over the chart, not back on the rail.
        const el = document.elementFromPoint(e.clientX, e.clientY)
        if (!el || !svgRef.current?.contains(el)) return
      }
      commit(last)
    }

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') cleanupState() // revert: preview vanishes, nothing committed
    }

    const cleanupState = () => {
      origin.current = null
      setDrag(null)
    }

    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('keydown', onKey)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [drag !== null, scale, commit, onClickTask, svgPoint])

  // latest mirrors drag for the pointerup closure (state updates are async).
  const latest = useRef<GanttDrag | null>(null)
  latest.current = drag

  return { drag, startBarDrag, startStageDrag, startBacklogDrag }
}

function shiftDate(day: string | undefined, days: number): string | undefined {
  if (!day) return day
  return fmtDay(addDays(parseDay(day), days))
}

// barIdAtPoint hit-tests the DOM for a bar under the pointer (excluding the
// drag source) — simpler and more robust than per-bar enter/leave counting.
function barIdAtPoint(clientX: number, clientY: number, excludeId?: string): string | undefined {
  const el = document.elementFromPoint(clientX, clientY)
  const id = el?.closest('[data-taskid]')?.getAttribute('data-taskid') ?? undefined
  return id && id !== excludeId ? id : undefined
}
