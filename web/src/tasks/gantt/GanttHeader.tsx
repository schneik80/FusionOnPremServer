import { useTheme } from '@mui/material'
import { HEADER_H, dateToX, headerBands, type GanttScale } from './ganttMath'

// GanttHeader is the two-band calendar strip: coarse unit (month/year) on
// top, one label per column below. The parent keeps it position:sticky; this
// is just the SVG. A small "today" cap marks where the today line starts.
export function GanttHeader({ scale, today }: { scale: GanttScale; today: Date }) {
  const theme = useTheme()
  const { top, bottom } = headerBands(scale)
  const bandH = HEADER_H / 2
  const todayX = dateToX(scale, today)
  const showToday = todayX > 0 && todayX < scale.width

  return (
    <svg
      width={scale.width}
      height={HEADER_H}
      style={{ display: 'block', background: theme.palette.background.paper }}
    >
      {top.map((b) => (
        <g key={`t${b.x}`}>
          <line x1={b.x} y1={0} x2={b.x} y2={bandH} stroke={theme.palette.divider} />
          <text
            x={b.x + b.w / 2}
            y={bandH / 2}
            textAnchor="middle"
            dominantBaseline="central"
            fontSize={11}
            fontWeight={600}
            fill={theme.palette.text.secondary}
          >
            {b.w > 60 ? b.label : ''}
          </text>
        </g>
      ))}
      {bottom.map((b) => (
        <g key={`b${b.x}`}>
          <line x1={b.x} y1={bandH} x2={b.x} y2={HEADER_H} stroke={theme.palette.divider} />
          <text
            x={b.x + b.w / 2}
            y={bandH + bandH / 2}
            textAnchor="middle"
            dominantBaseline="central"
            fontSize={10}
            fill={theme.palette.text.secondary}
          >
            {b.label}
          </text>
        </g>
      ))}
      <line x1={0} y1={bandH} x2={scale.width} y2={bandH} stroke={theme.palette.divider} />
      <line x1={0} y1={HEADER_H - 0.5} x2={scale.width} y2={HEADER_H - 0.5} stroke={theme.palette.divider} />
      {showToday && (
        <g>
          <rect
            x={todayX - 17}
            y={HEADER_H - 15}
            width={34}
            height={13}
            rx={3}
            fill={theme.palette.primary.main}
          />
          <text
            x={todayX}
            y={HEADER_H - 8.5}
            textAnchor="middle"
            dominantBaseline="central"
            fontSize={8.5}
            fontWeight={600}
            fill={theme.palette.primary.contrastText}
          >
            Today
          </text>
        </g>
      )}
    </svg>
  )
}
