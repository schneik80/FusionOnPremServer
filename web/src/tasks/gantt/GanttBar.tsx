import { alpha, useTheme, type Theme } from '@mui/material'
import { useState } from 'react'
import type { Task, TaskStatus } from '../types'
import { HANDLE_W, type BarRect } from './ganttMath'

// Drag actions a bar can start; the hook interprets them.
export type BarAction = 'move' | 'resizeStart' | 'resizeEnd' | 'progress' | 'link' | 'stageMove'

// statusBarColor keeps the Gantt in the same color language as the chips:
// todo=neutral, inprogress=info, blocked=warning, done=success.
export function statusBarColor(theme: Theme, status: TaskStatus): string {
  switch (status) {
    case 'inprogress':
      return theme.palette.info.main
    case 'blocked':
      return theme.palette.warning.main
    case 'done':
      return theme.palette.success.main
    default:
      return theme.palette.text.secondary
  }
}

// GanttTaskBar draws one scheduled task: milestone diamond or a rounded bar
// with a solid progress fill, plus its due-date flag. Handles (resize /
// progress / link) appear on hover when the caller may write.
export function GanttTaskBar({
  task,
  rect,
  progress,
  dueX,
  overdue,
  writable,
  selected,
  dragging,
  onPointerDown,
  onClick,
  onHover,
}: {
  task: Task
  rect: BarRect
  progress: number
  dueX: number | null
  overdue: boolean
  writable: boolean
  selected: boolean
  dragging: boolean
  onPointerDown: (e: React.PointerEvent, action: BarAction) => void
  onClick: () => void
  onHover: (hovering: boolean) => void
}) {
  const theme = useTheme()
  const [hover, setHover] = useState(false)
  const color = statusBarColor(theme, task.status)
  const done = task.status === 'done'
  const stroke = overdue ? theme.palette.error.main : selected ? theme.palette.primary.main : color
  const showHandles = writable && (hover || dragging)
  const w = Math.max(2, rect.x2 - rect.x1)

  const hoverProps = {
    onPointerEnter: () => {
      setHover(true)
      onHover(true)
    },
    onPointerLeave: () => {
      setHover(false)
      onHover(false)
    },
  }

  if (task.milestone) {
    const cx = (rect.x1 + rect.x2) / 2
    const r = rect.h / 2
    return (
      <g {...hoverProps} data-taskid={task.id}>
        <path
          d={`M ${cx} ${rect.mid - r} L ${cx + r} ${rect.mid} L ${cx} ${rect.mid + r} L ${cx - r} ${rect.mid} Z`}
          fill={done ? alpha(color, 0.6) : color}
          stroke={stroke}
          strokeWidth={selected || overdue ? 2 : 1}
          cursor={writable ? 'grab' : 'pointer'}
          onPointerDown={(e) => onPointerDown(e, 'move')}
          onClick={onClick}
        />
        {dueX !== null && <DueFlag x={dueX} rect={rect} overdue={overdue} theme={theme} />}
      </g>
    )
  }

  const progW = (Math.min(100, Math.max(0, progress)) / 100) * w
  return (
    <g {...hoverProps} data-taskid={task.id} opacity={done ? 0.65 : 1}>
      <rect
        x={rect.x1}
        y={rect.y}
        width={w}
        height={rect.h}
        rx={4}
        fill={alpha(color, 0.25)}
        stroke={stroke}
        strokeWidth={selected || overdue ? 1.8 : 1}
        cursor={writable ? 'grab' : 'pointer'}
        onPointerDown={(e) => onPointerDown(e, 'move')}
        onClick={onClick}
      />
      {progW > 0 && (
        <rect
          x={rect.x1}
          y={rect.y}
          width={progW}
          height={rect.h}
          rx={4}
          fill={alpha(color, 0.75)}
          pointerEvents="none"
        />
      )}
      {w > 54 && (
        <text
          x={rect.x1 + 6}
          y={rect.mid}
          dominantBaseline="central"
          fontSize={10.5}
          fill={theme.palette.getContrastText(theme.palette.background.default)}
          pointerEvents="none"
          style={{ userSelect: 'none' }}
        >
          {truncate(task.title, w - 12)}
        </text>
      )}
      {showHandles && (
        <>
          <rect
            x={rect.x1}
            y={rect.y}
            width={HANDLE_W}
            height={rect.h}
            rx={2}
            fill={alpha(theme.palette.primary.main, 0.6)}
            cursor="ew-resize"
            onPointerDown={(e) => onPointerDown(e, 'resizeStart')}
          />
          <rect
            x={rect.x2 - HANDLE_W}
            y={rect.y}
            width={HANDLE_W}
            height={rect.h}
            rx={2}
            fill={alpha(theme.palette.primary.main, 0.6)}
            cursor="ew-resize"
            onPointerDown={(e) => onPointerDown(e, 'resizeEnd')}
          />
          {/* Progress handle: a small triangle riding the fill edge. */}
          <polygon
            points={`${rect.x1 + progW},${rect.y + rect.h} ${rect.x1 + progW - 5},${rect.y + rect.h + 7} ${rect.x1 + progW + 5},${rect.y + rect.h + 7}`}
            fill={theme.palette.primary.main}
            cursor="ew-resize"
            onPointerDown={(e) => onPointerDown(e, 'progress')}
          />
          {/* Link handle: drag from here onto another bar to add a dependency. */}
          <circle
            cx={rect.x2 + 8}
            cy={rect.mid}
            r={5}
            fill={theme.palette.background.paper}
            stroke={theme.palette.primary.main}
            strokeWidth={1.5}
            cursor="crosshair"
            onPointerDown={(e) => onPointerDown(e, 'link')}
          />
        </>
      )}
      {dueX !== null && <DueFlag x={dueX} rect={rect} overdue={overdue} theme={theme} />}
    </g>
  )
}

// GanttStageBar is the derived aggregate bar over a stage's children: a thin
// span with end caps and a read-only roll-up fill. Dragging it moves every
// child (one atomic shift).
export function GanttStageBar({
  stage,
  rect,
  progress,
  writable,
  dragging,
  onPointerDown,
  onToggle,
}: {
  stage: string
  rect: BarRect
  progress: number
  writable: boolean
  dragging: boolean
  onPointerDown: (e: React.PointerEvent, action: BarAction) => void
  onToggle: () => void
}) {
  const theme = useTheme()
  const color = theme.palette.primary.main
  const w = Math.max(2, rect.x2 - rect.x1)
  const h = 10
  const y = rect.y + 2
  const progW = (Math.min(100, Math.max(0, progress)) / 100) * w
  return (
    <g data-stage={stage} opacity={dragging ? 0.85 : 1}>
      <rect
        x={rect.x1}
        y={y}
        width={w}
        height={h}
        rx={3}
        fill={alpha(color, 0.3)}
        stroke={alpha(color, 0.7)}
        cursor={writable ? 'grab' : 'default'}
        onPointerDown={(e) => onPointerDown(e, 'stageMove')}
        onClick={onToggle}
      />
      {progW > 0 && (
        <rect x={rect.x1} y={y} width={progW} height={h} rx={3} fill={alpha(color, 0.8)} pointerEvents="none" />
      )}
      {/* End caps: the classic project-bar teeth. */}
      <path
        d={`M ${rect.x1} ${y + h} L ${rect.x1} ${y + h + 5} L ${rect.x1 + 6} ${y + h} Z`}
        fill={alpha(color, 0.7)}
        pointerEvents="none"
      />
      <path
        d={`M ${rect.x2} ${y + h} L ${rect.x2} ${y + h + 5} L ${rect.x2 - 6} ${y + h} Z`}
        fill={alpha(color, 0.7)}
        pointerEvents="none"
      />
    </g>
  )
}

function DueFlag({
  x,
  rect,
  overdue,
  theme,
}: {
  x: number
  rect: BarRect
  overdue: boolean
  theme: Theme
}) {
  const color = overdue ? theme.palette.error.main : theme.palette.text.secondary
  return (
    <g pointerEvents="none">
      <line x1={x} y1={rect.y - 3} x2={x} y2={rect.y + rect.h + 3} stroke={color} strokeWidth={1.5} />
      <path d={`M ${x} ${rect.y - 3} L ${x + 7} ${rect.y} L ${x} ${rect.y + 3} Z`} fill={color} />
    </g>
  )
}

// truncate clips a label to the pixels available using real text
// measurement (a per-char estimate breaks on CJK, whose glyphs are ~2×
// Latin width, and code-unit slicing can split surrogate pairs). One shared
// canvas context; grapheme-safe binary search on the cut point.
let measureCtx: CanvasRenderingContext2D | null = null
function measure(s: string): number {
  if (!measureCtx) {
    measureCtx = document.createElement('canvas').getContext('2d')
    if (!measureCtx) return s.length * 5.8 // canvas unavailable (tests)
    measureCtx.font = '10.5px "Montserrat", system-ui, sans-serif'
  }
  return measureCtx.measureText(s).width
}

function truncate(s: string, px: number): string {
  if (measure(s) <= px) return s
  const parts = graphemeSplit(s)
  const ell = '…'
  let lo = 0
  let hi = parts.length
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1
    if (measure(parts.slice(0, mid).join('') + ell) <= px) lo = mid
    else hi = mid - 1
  }
  return lo === 0 ? '' : parts.slice(0, lo).join('') + ell
}

const segmenter =
  typeof Intl !== 'undefined' && 'Segmenter' in Intl
    ? new Intl.Segmenter(undefined, { granularity: 'grapheme' })
    : null
function graphemeSplit(s: string): string[] {
  return segmenter ? [...segmenter.segment(s)].map((x) => x.segment) : Array.from(s)
}
