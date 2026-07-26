import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Stack, Tooltip, Typography } from '@mui/material'
import { alpha, darken, useTheme } from '@mui/material/styles'
import { thumbnailSrc } from '../api/thumbnails'
import type { VersionSummary } from '../api/types'

// HistoryGraph renders a design's version history as a horizontal, GitHub-style
// branch graph. Lanes, top → bottom (only drawn when populated):
//   • share (public shares) — versions with a public share  [reserved; no API source yet]
//   • main (releases)    — versions carrying a revision  [reserved; no API source yet]
//   • release (milestones) — versions flagged isMilestone
//   • dev (saves)        — every version; always present
// A milestone is a commit promoted from dev → release, drawn as a vertical merge
// connector up to a release-lane dot; a release is promoted release → main the
// same way. A public share hangs a rust-orange dot on the top lane, connected
// straight down to its save. The date-time of each save is its "tag" under the
// dev dot, and each column carries a tooltip with the version's thumbnail and
// metadata.

type Lane = 'share' | 'main' | 'release' | 'dev'

// SHARE_COLOR is the rust-orange used for the public-share lane and dot — set
// apart from the primary-accent release/milestone lanes.
const SHARE_COLOR = '#b7410e'

const NODE_R = 7 // commit dot radius
const COL_GAP = 38 // horizontal spacing between saves
const LANE_GAP = 56 // vertical spacing between lanes
const PAD_X = COL_GAP / 2 + 12 // left/right padding (keeps edge columns in-bounds)
const PAD_TOP = 24 // padding above the topmost lane
const LABEL_H = 66 // band under the dev lane for the angled date-time tags

function tagLabel(s?: string): string {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d.getTime())) return s
  return d.toLocaleString([], {
    month: 'short',
    day: 'numeric',
    year: '2-digit',
    hour: 'numeric',
    minute: '2-digit',
  })
}

function fmtFull(s?: string): string {
  if (!s) return '—'
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString()
}

export default function HistoryGraph({
  versions,
}: {
  versions: VersionSummary[]
  projectAltId?: string
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const accent = theme.palette.primary.main
  const laneColor: Record<Lane, string> = {
    dev: theme.palette.text.secondary,
    release: accent,
    main: darken(accent, 0.25),
    share: SHARE_COLOR,
  }
  const ringColor = theme.palette.background.paper

  // Oldest → newest, left → right (git convention: newest on the right).
  const ordered = useMemo(() => [...versions].slice().reverse(), [versions])

  const hasRelease = ordered.some((v) => v.isMilestone)
  const hasMain = ordered.some((v) => !!v.revision)
  const hasShare = ordered.some((v) => !!v.publicShare)

  // Lanes present, top → bottom. dev is always the bottom lane.
  const lanes: Lane[] = []
  if (hasShare) lanes.push('share')
  if (hasMain) lanes.push('main')
  if (hasRelease) lanes.push('release')
  lanes.push('dev')

  const yOf = (lane: Lane) => PAD_TOP + lanes.indexOf(lane) * LANE_GAP
  const xOf = (i: number) => PAD_X + i * COL_GAP

  const width = PAD_X * 2 + Math.max(0, ordered.length - 1) * COL_GAP
  const height = yOf('dev') + LABEL_H

  const devY = yOf('dev')
  const [hovered, setHovered] = useState<number | null>(null)

  // Lane rails: a polyline through the dots that live on each lane. dev spans the
  // full width; release/main span between their first and last marked versions.
  const railFor = (pred: (v: VersionSummary) => boolean): [number, number] | null => {
    const idxs = ordered.map((v, i) => (pred(v) ? i : -1)).filter((i) => i >= 0)
    if (idxs.length === 0) return null
    return [idxs[0], idxs[idxs.length - 1]]
  }
  const releaseRail = hasRelease ? railFor((v) => !!v.isMilestone) : null
  const mainRail = hasMain ? railFor((v) => !!v.revision) : null
  const shareRail = hasShare ? railFor((v) => !!v.publicShare) : null

  return (
    <Stack spacing={1} sx={{ minHeight: 0 }}>
      <Typography variant="caption" color="text.secondary">
        {t('history.versionCount', { count: ordered.length })}
      </Typography>

      {/* Graph — scrolls horizontally when wider than the panel. */}
      <Box sx={{ overflowX: 'auto', overflowY: 'hidden', py: 1 }}>
        <Box sx={{ position: 'relative', width, height }}>
          <svg width={width} height={height} style={{ display: 'block' }}>
            {/* lane rails */}
            {ordered.length > 1 && (
              <line
                x1={xOf(0)}
                y1={devY}
                x2={xOf(ordered.length - 1)}
                y2={devY}
                stroke={alpha(laneColor.dev, 0.5)}
                strokeWidth={3}
                strokeLinecap="round"
              />
            )}
            {releaseRail && releaseRail[0] !== releaseRail[1] && (
              <line
                x1={xOf(releaseRail[0])}
                y1={yOf('release')}
                x2={xOf(releaseRail[1])}
                y2={yOf('release')}
                stroke={alpha(laneColor.release, 0.5)}
                strokeWidth={3}
                strokeLinecap="round"
              />
            )}
            {mainRail && mainRail[0] !== mainRail[1] && (
              <line
                x1={xOf(mainRail[0])}
                y1={yOf('main')}
                x2={xOf(mainRail[1])}
                y2={yOf('main')}
                stroke={alpha(laneColor.main, 0.5)}
                strokeWidth={3}
                strokeLinecap="round"
              />
            )}
            {shareRail && shareRail[0] !== shareRail[1] && (
              <line
                x1={xOf(shareRail[0])}
                y1={yOf('share')}
                x2={xOf(shareRail[1])}
                y2={yOf('share')}
                stroke={alpha(laneColor.share, 0.5)}
                strokeWidth={3}
                strokeLinecap="round"
              />
            )}

            {/* share connectors: a public share hangs straight down to its save */}
            {ordered.map((v, i) =>
              v.publicShare ? (
                <line
                  key={`s-${i}`}
                  x1={xOf(i)}
                  y1={yOf('share')}
                  x2={xOf(i)}
                  y2={devY}
                  stroke={laneColor.share}
                  strokeWidth={2}
                  strokeOpacity={0.8}
                  strokeDasharray="3 3"
                />
              ) : null,
            )}

            {/* merge connectors: dev → release at milestones, release → main at releases */}
            {ordered.map((v, i) =>
              v.isMilestone ? (
                <line
                  key={`m-${i}`}
                  x1={xOf(i)}
                  y1={devY}
                  x2={xOf(i)}
                  y2={yOf('release')}
                  stroke={laneColor.release}
                  strokeWidth={2}
                  strokeOpacity={0.8}
                />
              ) : null,
            )}
            {ordered.map((v, i) =>
              v.revision ? (
                <line
                  key={`r-${i}`}
                  x1={xOf(i)}
                  y1={yOf('release')}
                  x2={xOf(i)}
                  y2={yOf('main')}
                  stroke={laneColor.main}
                  strokeWidth={2}
                  strokeOpacity={0.8}
                />
              ) : null,
            )}

            {/* dots */}
            {ordered.map((v, i) => (
              <g key={`d-${i}`}>
                {/* dev dot (every save) */}
                <circle
                  cx={xOf(i)}
                  cy={devY}
                  r={NODE_R}
                  fill={laneColor.dev}
                  stroke={ringColor}
                  strokeWidth={hovered === i ? 2 : 0}
                />
                {/* milestone dot on the release lane */}
                {v.isMilestone && (
                  <circle
                    cx={xOf(i)}
                    cy={yOf('release')}
                    r={NODE_R}
                    fill={laneColor.release}
                    stroke={ringColor}
                    strokeWidth={2}
                  />
                )}
                {/* release dot on the main lane */}
                {v.revision && (
                  <circle
                    cx={xOf(i)}
                    cy={yOf('main')}
                    r={NODE_R}
                    fill={laneColor.main}
                    stroke={ringColor}
                    strokeWidth={2}
                  />
                )}
                {/* public-share dot on the top lane */}
                {v.publicShare && (
                  <circle
                    cx={xOf(i)}
                    cy={yOf('share')}
                    r={NODE_R}
                    fill={laneColor.share}
                    stroke={ringColor}
                    strokeWidth={2}
                  />
                )}
              </g>
            ))}

            {/* angled date-time tags under the dev lane */}
            {ordered.map((v, i) => {
              const tx = xOf(i)
              const ty = devY + NODE_R + 12
              return (
                <text
                  key={`t-${i}`}
                  x={tx}
                  y={ty}
                  textAnchor="end"
                  transform={`rotate(-35 ${tx} ${ty})`}
                  fontSize={9}
                  fill={theme.palette.text.secondary}
                  style={{ fontWeight: hovered === i ? 600 : 400 }}
                >
                  {tagLabel(v.createdOn)}
                </text>
              )
            })}
          </svg>

          {/* transparent per-version hit columns carrying the tooltip */}
          {ordered.map((v, i) => (
            <Tooltip key={`h-${i}`} title={<VersionTooltip v={v} />} placement="top" arrow>
              <Box
                onMouseEnter={() => setHovered(i)}
                onMouseLeave={() => setHovered((h) => (h === i ? null : h))}
                sx={{
                  position: 'absolute',
                  top: 0,
                  left: xOf(i) - COL_GAP / 2,
                  width: COL_GAP,
                  height,
                  cursor: 'default',
                  bgcolor: hovered === i ? alpha(accent, 0.08) : 'transparent',
                  transition: 'background-color .1s',
                }}
              />
            </Tooltip>
          ))}
        </Box>
      </Box>

      {/* Legend — only the lanes that are present. */}
      <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" sx={{ rowGap: 0.5 }}>
        <LegendItem color={laneColor.dev} label={t('history.saves')} />
        {hasRelease && <LegendItem color={laneColor.release} label={t('history.milestones')} />}
        {hasMain && <LegendItem color={laneColor.main} label={t('history.releases')} />}
        {hasShare && <LegendItem color={laneColor.share} label={t('history.publicShares')} />}
      </Stack>
    </Stack>
  )
}

function LegendItem({ color, label }: { color: string; label: string }) {
  return (
    <Stack direction="row" spacing={0.75} alignItems="center">
      <Box sx={{ width: 10, height: 10, borderRadius: '50%', bgcolor: color }} />
      <Typography variant="caption" color="text.secondary">
        {label}
      </Typography>
    </Stack>
  )
}

// VersionTooltip shows the version's thumbnail (rendered directly — no polling,
// 404s fall back to no image) plus its metadata: number, milestone/release
// markers, timestamp, author, and any save comment.
function VersionTooltip({ v }: { v: VersionSummary }) {
  const { t } = useTranslation('details')
  const [imgFailed, setImgFailed] = useState(false)
  const thumb = v.rootComponentVersionId
    ? thumbnailSrc({ kind: 'design', cvId: v.rootComponentVersionId })
    : null
  const showThumb = !!thumb && !imgFailed

  return (
    <Box sx={{ py: 0.25, maxWidth: 200 }}>
      {showThumb && (
        <Box
          component="img"
          src={thumb!}
          alt=""
          onError={() => setImgFailed(true)}
          sx={{
            display: 'block',
            width: '100%',
            maxHeight: 140,
            objectFit: 'contain',
            borderRadius: 0.5,
            mb: 0.5,
            bgcolor: alpha('#000', 0.15),
          }}
        />
      )}
      <Typography variant="caption" sx={{ fontWeight: 600, display: 'block' }}>
        {t('history.versionShort', { number: v.number })}
        {v.isMilestone ? ` · ${t('history.milestone')}` : ''}
        {v.revision ? ` · ${t('history.release', { revision: v.revision })}` : ''}
      </Typography>
      {v.publicShare && (
        <Typography variant="caption" sx={{ display: 'block', color: SHARE_COLOR, fontWeight: 600 }}>
          {t('history.publicShare')}
        </Typography>
      )}
      <Typography variant="caption" sx={{ display: 'block', opacity: 0.85 }}>
        {fmtFull(v.createdOn)}
        {v.createdBy ? ` · ${v.createdBy}` : ''}
      </Typography>
      {v.comment && (
        <Typography variant="caption" sx={{ display: 'block', mt: 0.5, opacity: 0.95 }}>
          {v.comment}
        </Typography>
      )}
    </Box>
  )
}
