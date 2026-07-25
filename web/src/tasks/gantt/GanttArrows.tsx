import { useTheme } from '@mui/material'
import { useState } from 'react'
import type { Task } from '../types'
import { arrowHead, dependencyPath, type BarRect } from './ganttMath'

export interface ArrowHit {
  fromId: string
  toId: string
  x: number
  y: number
}

// GanttArrows renders every dependency whose two ends are both on the chart
// (an unscheduled predecessor shows as a count badge on the bar instead —
// the successor's card lists it). Clicking an arrow (when writable) offers
// to remove the dependency via the parent's popover.
export function GanttArrows({
  tasks,
  rects,
  writable,
  onArrowClick,
}: {
  tasks: Task[]
  rects: Map<string, BarRect>
  writable: boolean
  onArrowClick: (hit: ArrowHit, ev: React.MouseEvent) => void
}) {
  const theme = useTheme()
  const [hover, setHover] = useState<string | null>(null)

  const arrows: { key: string; fromId: string; toId: string; from: BarRect; to: BarRect }[] = []
  for (const t of tasks) {
    const to = rects.get(t.id)
    if (!to) continue
    for (const dep of t.dependsOn) {
      const from = rects.get(dep)
      if (!from) continue
      arrows.push({ key: `${dep}->${t.id}`, fromId: dep, toId: t.id, from, to })
    }
  }

  return (
    <g>
      {arrows.map((a) => {
        const active = hover === a.key
        const color = active ? theme.palette.primary.main : theme.palette.text.secondary
        return (
          <g
            key={a.key}
            onPointerEnter={() => setHover(a.key)}
            onPointerLeave={() => setHover(null)}
            cursor={writable ? 'pointer' : undefined}
            onClick={(ev) =>
              onArrowClick(
                { fromId: a.fromId, toId: a.toId, x: ev.clientX, y: ev.clientY },
                ev,
              )
            }
          >
            {/* Fat invisible hit area under the visible stroke. */}
            <path d={dependencyPath(a.from, a.to)} fill="none" stroke="transparent" strokeWidth={9} />
            <path
              d={dependencyPath(a.from, a.to)}
              fill="none"
              stroke={color}
              strokeOpacity={active ? 0.95 : 0.55}
              strokeWidth={active ? 2 : 1.5}
              pointerEvents="none"
            />
            <polygon points={arrowHead(a.to)} fill={color} fillOpacity={active ? 0.95 : 0.7} pointerEvents="none" />
          </g>
        )
      })}
    </g>
  )
}
