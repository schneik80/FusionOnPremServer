import { alpha, useTheme } from '@mui/material'
import { ROW_H, dateToX, isWeekend, type GanttScale } from './ganttMath'

// GanttGrid is the chart background: weekend shading (day/week scales),
// column rules, row rules and the today line. Pure <g>, rendered inside the
// body SVG under the bars.
export function GanttGrid({
  scale,
  rowCount,
  today,
}: {
  scale: GanttScale
  rowCount: number
  today: Date
}) {
  const theme = useTheme()
  const height = Math.max(1, rowCount) * ROW_H
  const weekendFill = alpha(theme.palette.text.primary, theme.palette.mode === 'dark' ? 0.05 : 0.04)
  const todayX = dateToX(scale, today)
  const showToday = todayX > 0 && todayX < scale.width

  return (
    <g>
      {scale.unit !== 'month' &&
        scale.cols.map((c, i) => {
          if (scale.unit === 'day') {
            return isWeekend(c.start) ? (
              <rect
                key={i}
                x={i * scale.colWidth}
                y={0}
                width={scale.colWidth}
                height={height}
                fill={weekendFill}
              />
            ) : null
          }
          // Week columns: shade the trailing Sat+Sun fraction (2/7).
          const w = (scale.colWidth * 2) / 7
          return (
            <rect
              key={i}
              x={(i + 1) * scale.colWidth - w}
              y={0}
              width={w}
              height={height}
              fill={weekendFill}
            />
          )
        })}
      {scale.cols.map((_, i) => (
        <line
          key={i}
          x1={i * scale.colWidth}
          y1={0}
          x2={i * scale.colWidth}
          y2={height}
          stroke={theme.palette.divider}
          strokeOpacity={0.5}
        />
      ))}
      {Array.from({ length: rowCount + 1 }, (_, i) => (
        <line
          key={i}
          x1={0}
          y1={i * ROW_H}
          x2={scale.width}
          y2={i * ROW_H}
          stroke={theme.palette.divider}
          strokeOpacity={0.35}
        />
      ))}
      {showToday && (
        <line
          x1={todayX}
          y1={0}
          x2={todayX}
          y2={height}
          stroke={theme.palette.primary.main}
          strokeWidth={1.5}
          strokeOpacity={0.9}
        />
      )}
    </g>
  )
}
