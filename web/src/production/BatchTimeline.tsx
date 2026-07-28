import { Box, Tooltip, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  CONNECTOR_OPACITY,
  CONNECTOR_W,
  NODE_R,
  RAIL_ALPHA,
  RAIL_W,
  RING_W,
  TAG_ANGLE,
  TAG_FONT_SIZE,
  TAG_OFFSET,
} from '../components/graphstyle'
import { batchKindLabel, batchStatusLabel } from '../i18n/enums'
import type { ProdBatch } from './types'

// BatchTimeline lays a job's runs along a chronological axis, on two lanes —
// prove-out (top) and production (bottom, rust-orange) — modelled on the
// History graph's lane layout (index-spaced dots, angled date tags, hand-drawn
// SVG). A connector threads the runs in order; clicking a dot selects it. Dot
// and stroke weights come from graphstyle.ts so this reads as the same chart
// as the design History graph; only the spacing below is local, since runs are
// far sparser than saves.

const COL_GAP = 60
const LANE_GAP = 34
const PAD_X = 60
const PAD_TOP = 20
const LABEL_H = 56 // band under the bottom lane for the angled date tags
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
  const prodAccent = theme.palette.secondary.main
  const axis = theme.palette.text.secondary

  // Chronological order (oldest → newest) drives the x positions.
  const ordered = useMemo(
    () => [...batches].sort((a, b) => new Date(a.runAt).getTime() - new Date(b.runAt).getTime()),
    [batches],
  )

  const laneY = (i: number) => PAD_TOP + i * LANE_GAP + NODE_R
  const yOf = (kind: string) =>
    laneY(kind === 'production' ? LANES.indexOf('production') : LANES.indexOf('prove'))
  const xOf = (i: number) => PAD_X + i * COL_GAP

  const width = Math.max(PAD_X * 2 + (ordered.length - 1) * COL_GAP, 200)
  // Tags hang below the bottom lane, as the History graph's do below dev.
  const tagY = laneY(LANES.length - 1) + NODE_R + TAG_OFFSET
  const height = laneY(LANES.length - 1) + LABEL_H
  const colorOf = (kind: string) => (kind === 'production' ? prodAccent : accent)

  if (ordered.length === 0) return null

  return (
    <Box
      sx={{
        borderBottom: 1,
        borderColor: 'divider',
        overflowX: 'auto',
        flexShrink: 0,
        bgcolor: 'background.canvas',
      }}
    >
      <svg width={width} height={height} style={{ display: 'block' }}>
        {/* lane labels + rails */}
        {LANES.map((lane) => {
          const y = laneY(LANES.indexOf(lane))
          return (
            <g key={lane}>
              {/* A single run makes a zero-length rail, which a round cap would
                  draw as a stray blob — skip it, as the History graph does. */}
              {ordered.length > 1 && (
                <line
                  x1={PAD_X}
                  y1={y}
                  x2={xOf(ordered.length - 1)}
                  y2={y}
                  stroke={alpha(axis, RAIL_ALPHA)}
                  strokeWidth={RAIL_W}
                  strokeLinecap="round"
                />
              )}
              <text
                x={8}
                y={y + 3}
                fontSize={TAG_FONT_SIZE}
                fill={lane === 'production' ? prodAccent : axis}
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
            strokeOpacity={CONNECTOR_OPACITY}
            strokeWidth={CONNECTOR_W}
            strokeLinejoin="round"
            strokeLinecap="round"
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
              {/* Selection reads as a halo rather than a bigger dot, so every
                  run stays the same size as a History-graph commit. */}
              {sel && (
                <circle
                  cx={x}
                  cy={y}
                  r={NODE_R + RING_W + 1.5}
                  fill="none"
                  stroke={col}
                  strokeWidth={RING_W}
                  strokeOpacity={CONNECTOR_OPACITY}
                />
              )}
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
                  r={NODE_R}
                  fill={col}
                  stroke={theme.palette.background.paper}
                  strokeWidth={RING_W}
                />
              </Tooltip>
              {/* angled date tag under the bottom lane */}
              <text
                x={x}
                y={tagY}
                fontSize={TAG_FONT_SIZE}
                fill={axis}
                textAnchor="end"
                transform={`rotate(${TAG_ANGLE} ${x} ${tagY})`}
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
