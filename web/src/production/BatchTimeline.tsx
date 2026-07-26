import { Box, Tooltip, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { batchKindLabel, batchStatusLabel } from '../i18n/enums'
import { PRODUCTION_ACCENT } from './BatchDetail'
import type { ProdBatch } from './types'

// BatchTimeline lays a job's runs along a chronological axis, on two lanes —
// prove-out (top) and production (bottom, rust-orange) — modelled on the
// History graph's lane layout (index-spaced dots, angled date tags, hand-drawn
// SVG). A faint connector threads the runs in order; clicking a dot selects it.

const DOT_R = 6
const COL_GAP = 60
const LANE_GAP = 34
const PAD_X = 60
const PAD_TOP = 20
const LABEL_H = 40
const LANES = ['prove', 'production'] as const

function tag(s: string): string {
  const d = new Date(s)
  if (isNaN(d.getTime())) return ''
  return d.toLocaleDateString([], { month: 'short', day: 'numeric', year: '2-digit' })
}

export function BatchTimeline({
  batches,
  selectedId,
  onSelect,
}: {
  batches: ProdBatch[]
  selectedId: string | null
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation('production')
  const theme = useTheme()
  const accent = theme.palette.primary.main
  const axis = theme.palette.text.secondary

  // Chronological order (oldest → newest) drives the x positions.
  const ordered = useMemo(
    () => [...batches].sort((a, b) => new Date(a.runAt).getTime() - new Date(b.runAt).getTime()),
    [batches],
  )

  const yOf = (kind: string) =>
    PAD_TOP + (kind === 'production' ? LANES.indexOf('production') : LANES.indexOf('prove')) * LANE_GAP + DOT_R
  const xOf = (i: number) => PAD_X + i * COL_GAP

  const width = Math.max(PAD_X * 2 + (ordered.length - 1) * COL_GAP, 200)
  const height = PAD_TOP + LANES.length * LANE_GAP + LABEL_H
  const colorOf = (kind: string) => (kind === 'production' ? PRODUCTION_ACCENT : accent)

  if (ordered.length === 0) return null

  return (
    <Box
      sx={{
        borderBottom: 1,
        borderColor: 'divider',
        overflowX: 'auto',
        flexShrink: 0,
        bgcolor: alpha(theme.palette.text.primary, theme.palette.mode === 'dark' ? 0.02 : 0.015),
      }}
    >
      <svg width={width} height={height} style={{ display: 'block' }}>
        {/* lane labels + rails */}
        {LANES.map((lane) => {
          const y = PAD_TOP + LANES.indexOf(lane) * LANE_GAP + DOT_R
          return (
            <g key={lane}>
              <line
                x1={PAD_X}
                y1={y}
                x2={xOf(ordered.length - 1)}
                y2={y}
                stroke={axis}
                strokeOpacity={0.2}
                strokeWidth={1}
              />
              <text
                x={8}
                y={y + 3}
                fontSize={9}
                fill={lane === 'production' ? PRODUCTION_ACCENT : axis}
                style={{ textTransform: 'capitalize', fontWeight: 600 }}
              >
                {batchKindLabel(t, lane)}
              </text>
            </g>
          )
        })}

        {/* chronological connector across lanes */}
        {ordered.length > 1 && (
          <polyline
            points={ordered.map((b, i) => `${xOf(i)},${yOf(b.kind)}`).join(' ')}
            fill="none"
            stroke={axis}
            strokeOpacity={0.35}
            strokeWidth={1.25}
          />
        )}

        {/* dots + date tags */}
        {ordered.map((b, i) => {
          const x = xOf(i)
          const y = yOf(b.kind)
          const sel = b.id === selectedId
          const col = colorOf(b.kind)
          return (
            <g key={b.id} style={{ cursor: 'pointer' }} onClick={() => onSelect(b.id)}>
              <Tooltip
                title={
                  <Box sx={{ py: 0.25 }}>
                    <Typography variant="caption" sx={{ fontWeight: 600, display: 'block' }}>
                      {b.name}
                    </Typography>
                    <Typography variant="caption" sx={{ display: 'block', opacity: 0.85 }}>
                      {batchKindLabel(t, b.kind)} · {batchStatusLabel(t, b.status)} ·{' '}
                      {new Date(b.runAt).toLocaleString()}
                    </Typography>
                  </Box>
                }
                arrow
                placement="top"
              >
                <circle
                  cx={x}
                  cy={y}
                  r={sel ? DOT_R + 2.5 : DOT_R}
                  fill={col}
                  stroke={theme.palette.background.paper}
                  strokeWidth={sel ? 2.5 : 1.5}
                  style={{ transition: 'r .1s' }}
                />
              </Tooltip>
              {/* number badge in-dot for larger selected dot */}
              <text
                x={x}
                y={height - LABEL_H + 16}
                fontSize={9}
                fill={axis}
                textAnchor="middle"
                transform={`rotate(-30 ${x} ${height - LABEL_H + 16})`}
              >
                {tag(b.runAt)}
              </text>
            </g>
          )
        })}
      </svg>
    </Box>
  )
}
