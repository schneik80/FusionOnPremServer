import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { Box, Tooltip, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { fmtDate, getDateTimeFmt } from '../../fmt'
import { sameDay, today } from '../../fmt/dates'
import {
  AXIS_H,
  CHANGE_R,
  GUTTER_W,
  HEADER_H,
  hourTicks,
  declutter,
  plotWidth,
  ROW_PAD_Y,
  rowHeight,
  TRACK_H,
  trackY,
  xOfIndex,
  xOfMs,
} from './historyLayout'
import type { HistoryDay, HistoryDot, HistoryTrack } from './historyLayout'
import { CONNECTOR_OPACITY, NODE_R, RAIL_ALPHA, RAIL_W, RING_W, TAG_FONT_SIZE } from '../graphstyle'
import { userColor } from '../userColor'
import UserAvatar from '../UserAvatar'
import ChangeTooltip from './ChangeTooltip'
import VersionTooltip from './VersionTooltip'

// One day of a design's history: a date header, then one track per author who
// saved that day, each a rail of dots.
//
// The gutter (avatars) and the header are HTML rather than SVG so they can be
// position:sticky — in thread mode the plot runs thousands of pixels to the
// right and the "who" and "when" would otherwise scroll out of sight. Only the
// plot itself is drawn.

const HALO_R = NODE_R + RING_W + 1.5
const SHARE_R = HALO_R + 3
const HIT_R = NODE_R + 5
const DRIFT_VISIBLE = 3 // px a dot must be nudged before we mark its true time

export default function DayRow({
  row,
  viewW,
  plotW,
  thread,
  base,
  band,
  hovered,
  onHover,
}: {
  row: HistoryDay
  /** Visible width of the scroll container — what the sticky header pins to. */
  viewW: number
  /** Width of the plot area (day view: fitted to the panel; thread: the stack). */
  plotW: number
  thread: boolean
  /** Thread view: the index the axis starts from (see indexBase). */
  base: number
  band: boolean
  /** The hovered dot's key (see HistoryDot.key), threaded across rows. */
  hovered: string | null
  onHover: (key: string | null) => void
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()

  const withAxis = !thread
  const ticks = useMemo(() => (withAxis ? hourTicks(plotW) : []), [withAxis, plotW])
  const h = rowHeight(row.tracks.length, withAxis && ticks.length > 0)

  // Dot x positions, per track. Day view spaces by the clock and then relaxes
  // overlaps; thread view is already one COL_GAP apart by construction.
  const placed = useMemo(
    () =>
      row.tracks.map((track) => {
        const raw = track.dots.map((d) => (thread ? xOfIndex(d.index - base) : xOfMs(d.ms, viewW)))
        const x = thread ? raw : declutter(raw, 0, plotWidth(viewW))
        return { track, raw, x }
      }),
    [row.tracks, thread, base, viewW],
  )

  const bandColor = alpha(theme.palette.text.primary, theme.palette.mode === 'dark' ? 0.05 : 0.04)

  return (
    <Box sx={{ bgcolor: band ? bandColor : 'transparent', flexShrink: 0 }}>
      {/* Date header — sticky so it survives a horizontal scroll. */}
      <Box
        sx={{
          position: 'sticky',
          left: 0,
          width: viewW,
          height: HEADER_H,
          display: 'flex',
          alignItems: 'center',
          px: 1,
          gap: 1,
        }}
      >
        <Typography variant="caption" noWrap sx={{ fontWeight: 600 }}>
          {dayLabel(row, t)}
        </Typography>
        <Typography variant="caption" noWrap sx={{ color: 'text.disabled', fontSize: 10 }}>
          {rowTally(row, t)}
        </Typography>
      </Box>

      <Box sx={{ display: 'flex', alignItems: 'flex-start' }}>
        {/* Avatar gutter — a frozen column, not part of the plot.
            It sticks to the left edge so horizontal scrolling never takes a
            track's face away from it: however far right you scroll, every row
            still says who. zIndex 1 puts it above the thread overlay (which is
            positioned but z-auto), so the polyline passes underneath rather
            than across the avatars, and the opaque background means dots slide
            under it instead of through it.
            The right border only appears in thread view, where the plot can
            actually scroll under it — in day view nothing moves, so the rule
            would be a line for its own sake. */}
        <Box
          sx={{
            position: 'sticky',
            left: 0,
            zIndex: 1,
            width: GUTTER_W,
            flexShrink: 0,
            height: h,
            ...(thread ? { borderRight: 1, borderColor: 'divider' } : null),
            // Each avatar box is TRACK_H tall and centres its disc, so one
            // ROW_PAD_Y of lead-in lines them up with trackY().
            pt: `${ROW_PAD_Y}px`,
            // The gutter has to be opaque so dots never slide under it, and it
            // has to land on exactly the row's colour. bandColor is translucent,
            // so setting it here would paint it a second time over the row that
            // already has it and read a shade darker. Paint paper, then layer
            // the same band on top — the identical composite the row makes.
            bgcolor: 'background.paper',
            backgroundImage: band ? `linear-gradient(${bandColor}, ${bandColor})` : 'none',
          }}
        >
          {row.tracks.map((track) => (
            <TrackAvatar key={track.key} track={track} />
          ))}
        </Box>

        <svg
          width={plotW}
          height={h}
          role="img"
          aria-label={t('history.dayAria', { date: dayLabel(row, t), tally: rowTally(row, t) })}
          style={{ display: 'block', flexShrink: 0 }}
        >
          {/* Hour grid — day view only; in thread view x is sequence, not clock. */}
          {ticks.map((tick) => {
            const x = xOfMs(tick.hour * 3_600_000, viewW)
            return (
              <line
                key={`t-${tick.hour}`}
                x1={x}
                y1={4}
                x2={x}
                y2={h - (ticks.length ? AXIS_H : 0)}
                stroke={theme.palette.divider}
                strokeOpacity={tick.hour % 6 === 0 ? 0.7 : 0.35}
              />
            )
          })}

          {placed.map(({ track, raw, x }, ti) => {
            const y = trackY(ti)
            const rail = track.overflow
              ? theme.palette.text.secondary
              : userColor(track.key)
            return (
              <g key={track.key}>
                <line
                  x1={0}
                  y1={y}
                  x2={plotW}
                  y2={y}
                  stroke={alpha(rail, RAIL_ALPHA)}
                  strokeWidth={RAIL_W}
                  strokeLinecap="round"
                />
                {/* Where a dot had to be nudged to stay legible, a hairline
                    marks the time it actually happened. */}
                {x.map((cx, i) =>
                  Math.abs(cx - raw[i]) > DRIFT_VISIBLE ? (
                    <line
                      key={`drift-${i}`}
                      x1={raw[i]}
                      y1={y - NODE_R}
                      x2={raw[i]}
                      y2={y + NODE_R}
                      stroke={alpha(theme.palette.text.secondary, 0.3)}
                      strokeWidth={1}
                    />
                  ) : null,
                )}
                {track.dots.map((d, i) => (
                  <Dot
                    key={d.key}
                    dot={d}
                    cx={x[i]}
                    cy={y}
                    rail={rail}
                    hovered={hovered === d.key}
                    onHover={onHover}
                  />
                ))}
              </g>
            )
          })}

          {/* Hour labels under the last track. */}
          {ticks
            .filter((tick) => tick.label)
            .map((tick) => {
              const x = xOfMs(tick.hour * 3_600_000, viewW)
              return (
                <text
                  key={`l-${tick.hour}`}
                  x={x}
                  y={h - 6}
                  textAnchor={tick.hour === 0 ? 'start' : tick.hour === 24 ? 'end' : 'middle'}
                  fontSize={TAG_FONT_SIZE}
                  fill={theme.palette.text.secondary}
                >
                  {hourLabel(tick.hour)}
                </text>
              )
            })}
        </svg>
      </Box>
    </Box>
  )
}

// One event. The transparent circle comes first and is the hover target — it
// is wider than the dot so the tooltip is reachable without pixel-hunting, and
// fill="transparent" rather than "none" because "none" is not hit-testable.
//
// A save is a grey filled dot. A milestone keeps that grey dot and gains the
// accent ring; a release fills that same ring in. So the ring means "marked"
// and the fill means "released" — one step to learn rather than two similar
// hues to tell apart at 14 px, and a release still reads as the heavier of the
// two. A public share is an outer ring in the secondary colour. (Markers used
// to occupy their own lanes; vertical space now belongs to days and people.)
// An edit that made no version — a property changed, a milestone marked, a
// part number set — is a small open ring in the author's own rail colour, so
// it reads as a lighter event on the same track and can never be mistaken for
// a save.
function Dot({
  dot,
  cx,
  cy,
  rail,
  hovered,
  onHover,
}: {
  dot: HistoryDot
  cx: number
  cy: number
  /** The track's rail colour — the author's identity colour. */
  rail: string
  hovered: boolean
  onHover: (key: string | null) => void
}) {
  const theme = useTheme()
  const v = dot.v
  if (v.kind === 'change') {
    return (
      <Tooltip title={<ChangeTooltip change={v} />} placement="top" enterDelay={400} enterNextDelay={400}>
        <g
          onMouseEnter={() => onHover(dot.key)}
          onMouseLeave={() => onHover(null)}
          style={{ cursor: 'default' }}
        >
          <circle cx={cx} cy={cy} r={HIT_R} fill="transparent" />
          <circle
            cx={cx}
            cy={cy}
            r={CHANGE_R}
            fill={theme.palette.background.paper}
            stroke={rail}
            strokeWidth={hovered ? RING_W + 1 : RING_W}
          />
        </g>
      </Tooltip>
    )
  }
  const accent = theme.palette.primary.main
  const fill = v.revision ? accent : theme.palette.text.secondary
  const halo = accent

  return (
    // The tooltip's thumbnail is an ungated per-item APS call: a cold cvId
    // costs a GraphQL round trip plus an image fetch, and only the TIP
    // thumbnail is ever pre-warmed (by classify), so every version in here is
    // cold. Without a delay, brushing the cursor across a busy day fires one
    // pair of requests per dot passed over, which the per-minute cost quota
    // answers with 429s — and a 429 is exactly the "thumbnail missing" the
    // reader sees. The delay means only a dot you actually rest on spends
    // quota. Every other thumbnail in the app is gated the same way, by
    // useInView or a visible cap.
    <Tooltip
      title={<VersionTooltip v={v} />}
      placement="top"
      enterDelay={400}
      enterNextDelay={400}
    >
      <g
        onMouseEnter={() => onHover(dot.key)}
        onMouseLeave={() => onHover(null)}
        style={{ cursor: 'default' }}
      >
        <circle cx={cx} cy={cy} r={HIT_R} fill="transparent" />
        {v.publicShare && (
          <circle
            cx={cx}
            cy={cy}
            r={SHARE_R}
            fill="none"
            stroke={theme.palette.secondary.main}
            strokeWidth={RING_W}
            strokeOpacity={CONNECTOR_OPACITY}
          />
        )}
        {(v.isMilestone || v.revision) && (
          <circle
            cx={cx}
            cy={cy}
            r={HALO_R}
            fill="none"
            stroke={halo}
            strokeWidth={RING_W}
            strokeOpacity={CONNECTOR_OPACITY}
          />
        )}
        <circle
          cx={cx}
          cy={cy}
          r={NODE_R}
          fill={fill}
          stroke={theme.palette.background.paper}
          strokeWidth={hovered ? RING_W : 0}
        />
      </g>
    </Tooltip>
  )
}

// The identity disc for one track. Colour is a hash of the author key (see
// components/userColor.ts — the app's one non-theme colour), text colour is
// solved against it so light hues get dark initials.
function TrackAvatar({ track }: { track: HistoryTrack }) {
  const { t } = useTranslation('details')
  const label = track.overflow
    ? t('history.moreAuthors', { count: track.authorCount })
    : track.name
      ? t('history.savedBy', { name: track.name })
      : t('history.unknownAuthor')

  return (
    <Box
      sx={{
        height: TRACK_H,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      {/* The overflow track has no single identity, so it takes a neutral
          circle and a "+N" marker instead of an author's colour. */}
      <UserAvatar
        id={track.overflow ? undefined : track.key}
        name={track.name}
        size={22}
        tooltip={label}
        text={track.overflow ? `+${track.authorCount}` : undefined}
        sx={track.overflow ? { bgcolor: 'text.secondary' } : undefined}
      />
    </Box>
  )
}

function hourLabel(hour: number): string {
  // Formatted, not concatenated, so 6 reads "6 AM" in English and "06" in
  // German. 24 is midnight of the following day, drawn at the right edge.
  return getDateTimeFmt({ hour: 'numeric' }).format(new Date(2000, 0, 1, hour % 24))
}

// "5 saves" is a lie once the row also holds property edits, so the header
// counts what is actually on it: saves, changes, or both.
function rowTally(row: HistoryDay, t: TFunction): string {
  const saves = t('history.daySaves', { count: row.saves })
  const changes = t('history.dayChanges', { count: row.changes })
  if (!row.changes) return saves
  if (!row.saves) return changes
  return t('history.tally', { saves, changes })
}

function dayLabel(row: HistoryDay, t: (k: string) => string): string {
  if (!row.date) return t('history.unknownDate')
  const now = today()
  if (sameDay(row.date, now)) return t('history.today')
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1)
  if (sameDay(row.date, yesterday)) return t('history.yesterday')
  return fmtDate(row.date, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' })
}
