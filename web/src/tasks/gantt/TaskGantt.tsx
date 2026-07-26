import {
  faCompress,
  faLocationCrosshairs,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  alpha,
  Box,
  Button,
  IconButton,
  Paper,
  Popover,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useTaskMutations } from '../../api/queries'
import { PriorityDot } from '../chips'
import { TaskViewDialog } from '../TaskViewDialog'
import { taskDisplayId, type Task, type TaskCaps, type TaskList } from '../types'
import { BacklogRail } from './BacklogRail'
import { GanttArrows, type ArrowHit } from './GanttArrows'
import { GanttStageBar, GanttTaskBar } from './GanttBar'
import { GanttGrid } from './GanttGrid'
import { GanttHeader } from './GanttHeader'
import {
  BAR_H,
  HEADER_H,
  ROW_H,
  addDays,
  barRect,
  buildScale,
  dateToX,
  dayWidth,
  durationDays,
  parseDay,
  rowModel,
  scheduledSplit,
  unitToFit,
  type BarRect,
  type TimeUnit,
} from './ganttMath'
import { useGanttDrag } from './useGanttDrag'
import { localizeApiError } from '../../i18n/apiError'

// Width of the sticky label column between the backlog rail and the chart.
const LABEL_W = 230

// TaskGantt is the third task view: backlog rail | sticky label column |
// scrolling schedule chart. One overflow:auto container with CSS-sticky
// header and labels — no JS scroll syncing (the upstream library's jank came
// from syncing three panes through React state).
export function TaskGantt({
  projectId,
  tasks,
  caps,
  loading,
  error,
  unit,
  onUnitChange,
}: {
  projectId: string
  tasks: Task[]
  caps?: TaskCaps
  loading: boolean
  error: Error | null
  unit: TimeUnit
  onUnitChange: (u: TimeUnit) => void
}) {
  const { t } = useTranslation('tasks')
  const theme = useTheme()
  const qc = useQueryClient()
  const muts = useTaskMutations(projectId)
  const writable = caps?.write ?? false
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [openTaskId, setOpenTaskId] = useState<string | null>(null)
  const [arrowMenu, setArrowMenu] = useState<ArrowHit | null>(null)
  const [hovered, setHovered] = useState<{ task: Task; rect: BarRect } | null>(null)
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const svgRef = useRef<SVGSVGElement | null>(null)
  // One "today" per mount: a midnight rollover mid-session is not worth a
  // re-render loop.
  const today = useMemo(() => new Date(), [])

  const { backlog } = useMemo(() => scheduledSplit(tasks), [tasks])
  const scale = useMemo(() => buildScale(tasks, unit, today), [tasks, unit, today])
  const rows = useMemo(() => rowModel(tasks, collapsed), [tasks, collapsed])

  const { drag, startBarDrag, startStageDrag, startBacklogDrag } = useGanttDrag({
    projectId,
    scale,
    tasks,
    writable,
    svgRef,
    onClickTask: setOpenTaskId,
  })

  // Bar rectangles with the in-flight drag preview applied. Row ORDER stays
  // frozen to committed data during a drag (rowModel sorts by start date —
  // re-sorting mid-drag would make rows jump under the pointer); only the
  // dragged geometry moves.
  const stageShiftPx =
    drag?.action === 'stageMove' && drag.shiftDays ? drag.shiftDays * dayWidth(scale) : 0
  const stageShiftIds = useMemo(
    () => new Set(drag?.action === 'stageMove' ? drag.stageTaskIds ?? [] : []),
    [drag],
  )
  const rects = useMemo(() => {
    const m = new Map<string, BarRect>()
    rows.forEach((row, i) => {
      if (row.kind !== 'task' || !row.task) return
      const t = row.task
      let start = row.start
      let end = row.end
      if (drag && drag.taskId === t.id && drag.startDate && drag.endDate) {
        start = parseDay(drag.startDate)
        end = parseDay(drag.endDate)
      }
      const r = barRect(scale, { start, end }, i)
      if (stageShiftIds.has(t.id)) {
        m.set(t.id, { ...r, x1: r.x1 + stageShiftPx, x2: r.x2 + stageShiftPx, mid: r.mid })
      } else {
        m.set(t.id, r)
      }
    })
    return m
  }, [rows, scale, drag, stageShiftIds, stageShiftPx])

  const bodyH = Math.max(rows.length, 1) * ROW_H

  // Auto-center today on mount and when the zoom unit changes.
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const x = LABEL_W + dateToX(scale, today) - el.clientWidth / 2
    el.scrollLeft = Math.max(0, x)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scale.unit])

  const zoomToFit = () => {
    const el = scrollRef.current
    if (!el) return
    // The scroll-on-unit-change effect recenters after the switch.
    onUnitChange(unitToFit(tasks, Math.max(200, el.clientWidth - LABEL_W)))
  }

  const scrollToToday = () => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTo({ left: Math.max(0, LABEL_W + dateToX(scale, today) - el.clientWidth / 2), behavior: 'smooth' })
  }

  const removeDependency = () => {
    if (!arrowMenu) return
    const target = tasks.find((t) => t.id === arrowMenu.toId)
    setArrowMenu(null)
    if (!target) return
    const dependsOn = target.dependsOn.filter((id) => id !== arrowMenu.fromId)
    qc.setQueryData<TaskList>(['tasks', projectId], (cur) =>
      cur
        ? { ...cur, tasks: cur.tasks.map((t) => (t.id === target.id ? { ...t, dependsOn } : t)) }
        : cur,
    )
    muts.update.mutate(
      { taskId: target.id, patch: { dependsOn } },
      { onError: () => void qc.invalidateQueries({ queryKey: ['tasks', projectId] }) },
    )
  }

  const toggleStage = (stage: string) =>
    setCollapsed((cur) => {
      const next = new Set(cur)
      if (next.has(stage)) next.delete(stage)
      else next.add(stage)
      return next
    })

  if (error) {
    return (
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Typography variant="body2" color="error">
          {localizeApiError(t, error)}
        </Typography>
      </Box>
    )
  }

  const linkSource = drag?.action === 'link' && drag.linkFromId ? rects.get(drag.linkFromId) : null

  return (
    <Box sx={{ flex: 1, minHeight: 0, display: 'flex' }}>
      <BacklogRail
        tasks={backlog}
        loading={loading}
        error={null}
        writable={writable}
        onOpen={setOpenTaskId}
        onDragStart={startBacklogDrag}
      />

      <Box sx={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        {/* View toolbar: zoom unit, fit, today. */}
        <Stack
          direction="row"
          alignItems="center"
          spacing={1}
          sx={{ px: 1, py: 0.5, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
        >
          <Typography variant="caption" color="text.secondary" sx={{ flex: 1 }} noWrap>
            {writable ? t('gantt.hintWritable') : t('gantt.hintReadOnly')}
          </Typography>
          <Tooltip title={t('gantt.scrollToToday')}>
            <IconButton size="small" onClick={scrollToToday}>
              <FontAwesomeIcon icon={faLocationCrosshairs} style={{ fontSize: 13 }} />
            </IconButton>
          </Tooltip>
          <Tooltip title={t('gantt.zoomToFit')}>
            <IconButton size="small" onClick={zoomToFit}>
              <FontAwesomeIcon icon={faCompress} style={{ fontSize: 13 }} />
            </IconButton>
          </Tooltip>
          <ToggleButtonGroup
            size="small"
            exclusive
            value={unit}
            onChange={(_, v) => v && onUnitChange(v)}
            sx={{ '& .MuiToggleButton-root': { py: 0.1, px: 1, textTransform: 'none' } }}
          >
            <ToggleButton value="day">{t('gantt.unitDay')}</ToggleButton>
            <ToggleButton value="week">{t('gantt.unitWeek')}</ToggleButton>
            <ToggleButton value="month">{t('gantt.unitMonth')}</ToggleButton>
          </ToggleButtonGroup>
        </Stack>

        {/* The single scroll container (sticky header + sticky labels). */}
        <Box ref={scrollRef} sx={{ flex: 1, minHeight: 0, overflow: 'auto', position: 'relative' }}>
          <Box sx={{ width: LABEL_W + scale.width, position: 'relative' }}>
            {/* Header row */}
            <Box sx={{ position: 'sticky', top: 0, zIndex: 5, display: 'flex' }}>
              <Box
                sx={{
                  position: 'sticky',
                  left: 0,
                  zIndex: 6,
                  width: LABEL_W,
                  flexShrink: 0,
                  height: HEADER_H,
                  bgcolor: 'background.paper',
                  borderRight: 1,
                  borderBottom: 1,
                  borderColor: 'divider',
                  display: 'flex',
                  alignItems: 'flex-end',
                  px: 1.5,
                  pb: 0.5,
                }}
              >
                <Typography
                  variant="subtitle2"
                  sx={{ color: 'text.secondary', textTransform: 'uppercase', letterSpacing: 0.5, fontSize: 11 }}
                >
                  {t('gantt.scheduleColumn')}
                </Typography>
              </Box>
              <GanttHeader scale={scale} today={today} />
            </Box>

            {/* Body row: labels + chart. */}
            <Box sx={{ display: 'flex' }}>
              <Box
                sx={{
                  position: 'sticky',
                  left: 0,
                  zIndex: 4,
                  width: LABEL_W,
                  flexShrink: 0,
                  bgcolor: 'background.paper',
                  borderRight: 1,
                  borderColor: 'divider',
                }}
              >
                {rows.map((row) => (
                  <RowLabel
                    key={row.key}
                    row={row}
                    onOpen={setOpenTaskId}
                    onToggle={toggleStage}
                  />
                ))}
                {rows.length === 0 && <Box sx={{ height: bodyH }} />}
              </Box>

              <Box sx={{ position: 'relative' }}>
                <svg
                  ref={svgRef}
                  width={scale.width}
                  height={bodyH}
                  style={{ display: 'block', touchAction: 'none' }}
                >
                  <GanttGrid scale={scale} rowCount={rows.length} today={today} />
                  <GanttArrows
                    tasks={tasks}
                    rects={rects}
                    writable={writable}
                    onArrowClick={(hit) => writable && setArrowMenu(hit)}
                  />
                  {rows.map((row, i) => {
                    if (row.kind === 'stage') {
                      const r = barRect(scale, row, i)
                      const shifted =
                        drag?.action === 'stageMove' && drag.stage === row.stage
                          ? { ...r, x1: r.x1 + stageShiftPx, x2: r.x2 + stageShiftPx, mid: r.mid }
                          : r
                      return (
                        <GanttStageBar
                          key={row.key}
                          stage={row.stage!}
                          rect={shifted}
                          progress={row.progress}
                          writable={writable}
                          dragging={drag?.action === 'stageMove' && drag.stage === row.stage}
                          onPointerDown={(e) =>
                            startStageDrag(e, row.stage!, (row.tasks ?? []).map((t) => t.id))
                          }
                          onToggle={() => toggleStage(row.stage!)}
                        />
                      )
                    }
                    const t = row.task!
                    const r = rects.get(t.id)!
                    const isDragTask = drag?.taskId === t.id
                    const progress =
                      isDragTask && drag?.action === 'progress' && drag.progress !== undefined
                        ? drag.progress
                        : t.status === 'done'
                          ? 100
                          : t.progress ?? 0
                    const dueX = t.dueDate ? dateToX(scale, addDays(parseDay(t.dueDate), 1)) : null
                    const overdue =
                      !!t.dueDate && t.status !== 'done' && !!t.endDate && t.endDate > t.dueDate
                    return (
                      <GanttTaskBar
                        key={row.key}
                        task={t}
                        rect={r}
                        progress={progress}
                        dueX={dueX}
                        overdue={overdue}
                        writable={writable}
                        selected={drag?.action === 'link' ? drag.linkTargetId === t.id : false}
                        dragging={isDragTask}
                        onPointerDown={(e, action) => startBarDrag(e, t, action)}
                        onClick={() => setOpenTaskId(t.id)}
                        onHover={(h) => setHovered(h && !drag ? { task: t, rect: r } : null)}
                      />
                    )
                  })}
                  {/* Live link line while dragging a dependency. */}
                  {linkSource && drag && (
                    <g pointerEvents="none">
                      <path
                        d={`M ${linkSource.x2} ${linkSource.mid} L ${drag.pointerX} ${drag.pointerY}`}
                        stroke={theme.palette.primary.main}
                        strokeWidth={1.5}
                        strokeDasharray="4 3"
                        fill="none"
                      />
                      <circle cx={drag.pointerX} cy={drag.pointerY} r={3.5} fill={theme.palette.primary.main} />
                    </g>
                  )}
                  {/* Ghost bar while dragging a backlog task onto the chart. */}
                  {drag?.action === 'schedule' && drag.moved && drag.startDate && drag.endDate && (
                    <g pointerEvents="none" opacity={0.7}>
                      <rect
                        x={dateToX(scale, parseDay(drag.startDate))}
                        y={drag.pointerY - BAR_H / 2}
                        width={
                          dateToX(scale, addDays(parseDay(drag.endDate), 1)) -
                          dateToX(scale, parseDay(drag.startDate))
                        }
                        height={BAR_H}
                        rx={4}
                        fill={alpha(theme.palette.primary.main, 0.35)}
                        stroke={theme.palette.primary.main}
                        strokeDasharray="4 3"
                      />
                    </g>
                  )}
                </svg>

                {/* Hover card (suppressed while dragging). */}
                {hovered && !drag && (
                  <HoverCard task={hovered.task} rect={hovered.rect} chartWidth={scale.width} />
                )}

                {!loading && rows.length === 0 && (
                  <Box
                    sx={{
                      position: 'absolute',
                      inset: 0,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      pointerEvents: 'none',
                    }}
                  >
                    <Typography variant="body2" color="text.secondary" sx={{ px: 2, textAlign: 'center' }}>
                      {writable ? t('gantt.emptyWritable') : t('gantt.emptyReadOnly')}
                    </Typography>
                  </Box>
                )}
              </Box>
            </Box>
          </Box>
        </Box>
      </Box>

      {/* Arrow click → remove-dependency confirm. */}
      <Popover
        open={!!arrowMenu}
        onClose={() => setArrowMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={arrowMenu ? { left: arrowMenu.x, top: arrowMenu.y } : undefined}
      >
        {arrowMenu && (
          <Box sx={{ p: 1.5 }}>
            <Typography variant="body2" sx={{ mb: 1 }}>
              <Trans
                t={t}
                i18nKey="gantt.removeDependencyConfirm"
                values={{
                  from: label(tasks, arrowMenu.fromId),
                  to: label(tasks, arrowMenu.toId),
                }}
                components={{ b: <b /> }}
              />
            </Typography>
            <Stack direction="row" spacing={1} justifyContent="flex-end">
              <Button size="small" onClick={() => setArrowMenu(null)} sx={{ textTransform: 'none' }}>
                {t('common:cancel')}
              </Button>
              <Button
                size="small"
                color="error"
                variant="contained"
                onClick={removeDependency}
                sx={{ textTransform: 'none' }}
              >
                {t('common:remove')}
              </Button>
            </Stack>
          </Box>
        )}
      </Popover>

      {openTaskId && (
        <TaskViewDialog
          open
          projectId={projectId}
          taskId={openTaskId}
          onClose={() => setOpenTaskId(null)}
        />
      )}
    </Box>
  )
}

function label(tasks: Task[], id: string): string {
  const t = tasks.find((x) => x.id === id)
  return t ? taskDisplayId(t) : id
}

// RowLabel is one line of the sticky label column: stage rows carry the
// collapse chevron and roll-up %, task rows the dot + id + title.
function RowLabel({
  row,
  onOpen,
  onToggle,
}: {
  row: ReturnType<typeof rowModel>[number]
  onOpen: (taskId: string) => void
  onToggle: (stage: string) => void
}) {
  if (row.kind === 'stage') {
    return (
      <Box
        onClick={() => onToggle(row.stage!)}
        sx={{
          height: ROW_H,
          display: 'flex',
          alignItems: 'center',
          gap: 0.75,
          px: 1,
          cursor: 'pointer',
          '&:hover': { bgcolor: 'action.hover' },
          borderBottom: 1,
          borderColor: 'divider',
        }}
      >
        <Box
          component="span"
          sx={{
            display: 'inline-block',
            transition: 'transform 120ms',
            transform: row.collapsed ? 'rotate(-90deg)' : 'none',
            fontSize: 10,
            color: 'text.secondary',
          }}
        >
          ▼
        </Box>
        <Typography variant="body2" noWrap sx={{ fontWeight: 600, flex: 1 }}>
          {row.stage}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          {row.progress}%
        </Typography>
      </Box>
    )
  }
  const t = row.task!
  return (
    <Box
      onClick={() => onOpen(t.id)}
      sx={{
        height: ROW_H,
        display: 'flex',
        alignItems: 'center',
        gap: 0.75,
        pl: t.stage ? 3 : 1,
        pr: 1,
        cursor: 'pointer',
        '&:hover': { bgcolor: 'action.hover' },
        borderBottom: 1,
        borderColor: 'divider',
      }}
    >
      <PriorityDot priority={t.priority} />
      <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0 }}>
        {taskDisplayId(t)}
      </Typography>
      <Typography
        variant="body2"
        noWrap
        sx={{
          textDecoration: t.status === 'done' ? 'line-through' : undefined,
          color: t.status === 'done' ? 'text.secondary' : undefined,
        }}
      >
        {t.title}
      </Typography>
    </Box>
  )
}

// HoverCard is the rich bar tooltip: title, span + duration, progress,
// assignee, dependency count. Positioned in chart coordinates so it scrolls
// with the content.
function HoverCard({ task, rect, chartWidth }: { task: Task; rect: BarRect; chartWidth: number }) {
  const { t } = useTranslation('tasks')
  const w = 240
  const left = Math.min(Math.max(4, rect.x2 + 10), Math.max(4, chartWidth - w - 4))
  const lines = [
    t('hoverCard.spanLine', { range: fmtRange(task), days: durationDays(task) }),
    task.milestone
      ? t('hoverCard.milestone')
      : t('hoverCard.progress', { value: task.status === 'done' ? 100 : task.progress ?? 0 }),
    task.assignee?.name || task.assignee?.email,
    task.dependsOn.length ? t('hoverCard.deps', { count: task.dependsOn.length }) : undefined,
    task.dueDate ? t('hoverCard.due', { date: task.dueDate }) : undefined,
  ].filter(Boolean) as string[]
  return (
    <Paper
      elevation={4}
      sx={{
        position: 'absolute',
        left,
        top: Math.max(2, rect.y - 6),
        width: w,
        p: 1.25,
        pointerEvents: 'none',
        zIndex: 3,
      }}
    >
      <Typography variant="body2" sx={{ fontWeight: 600, mb: 0.25 }} noWrap>
        {taskDisplayId(task)} {task.title}
      </Typography>
      {lines.map((l) => (
        <Typography key={l} variant="caption" display="block" color="text.secondary">
          {l}
        </Typography>
      ))}
    </Paper>
  )
}

function fmtRange(t: Task): string {
  if (!t.startDate || !t.endDate) return ''
  const s = parseDay(t.startDate)
  const e = parseDay(t.endDate)
  const opts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' }
  return `${s.toLocaleDateString(undefined, opts)} → ${e.toLocaleDateString(undefined, opts)}`
}
