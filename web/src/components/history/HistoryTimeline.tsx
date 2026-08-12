import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Button, Checkbox, FormControlLabel, Stack, Tooltip, Typography } from '@mui/material'
import { alpha, darken, useTheme } from '@mui/material/styles'
import type { VersionSummary } from '../../api/types'
import { NODE_R, RING_W } from '../graphstyle'
import DayRow from './DayRow'
import GapLabel from './GapLabel'
import ThreadOverlay from './ThreadOverlay'
import {
  bucketByDay,
  DAY_ROWS_CAP,
  gapBetween,
  GUTTER_W,
  indexBase,
  MIN_VIEW_H,
  plotWidth,
  threadWidth,
} from './historyLayout'

// A design's version history as a stack of day rows, newest at the top.
//
// Each row is one calendar day, split into a track per author, with an identity
// avatar in a sticky left gutter. Alternating row bands and an elapsed-time
// label between rows ("Next day", "3 months and 2 days later") carry what the
// old single strip could not: the shape of a working day, and how long the
// design sat untouched.
//
// Two x mappings share that skeleton, behind one checkbox:
//   • off (default) — the row is a 00:00→24:00 clock fitted to the panel, so
//     noon is the same column in every row and nothing scrolls sideways.
//   • on — every save sits on one continuous chronological axis, COL_GAP apart,
//     with a polyline threading them in order. Empty time costs no width, so a
//     long history scrolls both ways inside a height-bounded box; that is the
//     trade the toggle makes. The author gutter freezes against the left edge
//     so a track never loses its face.
//
// The layout maths lives in historyLayout.ts (pure, unit-tested); this file
// measures the panel and decides what to render.

const SEED_W = 720 // pre-measurement width, so the first paint is not a flash

export default function HistoryTimeline({ versions }: { versions: VersionSummary[] }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const [thread, setThread] = useState(false)
  const [showAll, setShowAll] = useState(false)
  const [hovered, setHovered] = useState<number | null>(null)

  // One observer for the whole stack: every row draws at the same measured
  // width, which is what keeps the clock axis aligned from row to row.
  const boxRef = useRef<HTMLDivElement | null>(null)
  const [viewW, setViewW] = useState(SEED_W)
  useEffect(() => {
    const el = boxRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect?.width ?? 0
      if (w > 0) setViewW((prev) => (Math.abs(prev - w) > 0.5 ? w : prev))
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const allRows = useMemo(() => bucketByDay(versions), [versions])
  const capped = !showAll && allRows.length > DAY_ROWS_CAP
  const rows = capped ? allRows.slice(0, DAY_ROWS_CAP) : allRows

  const shownVersions = useMemo(() => rows.reduce((n, r) => n + r.count, 0), [rows])
  // The cap drops the oldest days, so the thread axis has to start from the
  // oldest index that survived rather than from zero.
  const base = useMemo(() => indexBase(rows), [rows])
  const stackW = thread ? GUTTER_W + threadWidth(shownVersions) : viewW
  const plotW = thread ? threadWidth(shownVersions) : plotWidth(viewW)

  const hasMilestone = versions.some((v) => v.isMilestone)
  const hasRelease = versions.some((v) => !!v.revision)
  const hasShare = versions.some((v) => !!v.publicShare)

  return (
    <Stack spacing={1} sx={{ minHeight: 0, ...(thread ? { height: '100%' } : null) }}>
      <Stack
        direction="row"
        spacing={1}
        alignItems="center"
        justifyContent="space-between"
        flexWrap="wrap"
      >
        <Typography variant="caption" color="text.secondary">
          {t('history.versionCount', { count: versions.length })}
        </Typography>
        <Tooltip title={t('history.showThreadHint')} placement="top">
          <FormControlLabel
            control={
              <Checkbox
                size="small"
                checked={thread}
                onChange={(e) => setThread(e.target.checked)}
              />
            }
            label={
              <Typography variant="caption" color="text.secondary">
                {t('history.showThread')}
              </Typography>
            }
            sx={{ mr: 0 }}
          />
        </Tooltip>
      </Stack>

      {/* The scroll container.
          Day view is fitted to the panel and never overflows sideways, so it
          grows downward and page-scrolls with the tab, like the list it reads
          as.
          Thread view owns BOTH axes and must therefore be height-bounded. It
          shipped unbounded, which meant the box was as tall as its content — so
          its horizontal scrollbar sat at the bottom of an 8,000px element,
          nowhere near the viewport unless you had already scrolled to the end
          of the history. Bounding the height pins both bars to the edges of
          something you can actually see, and puts them on one box so the themed
          scrollbar corner applies (see theme.ts). */}
      <Box
        ref={boxRef}
        sx={{
          position: 'relative',
          ...(thread
            ? { flex: 1, minHeight: MIN_VIEW_H, overflow: 'auto' }
            : { overflow: 'hidden' }),
          border: 1,
          borderColor: 'divider',
          borderRadius: 1,
        }}
      >
        <Box sx={{ position: 'relative', width: stackW, minWidth: '100%' }}>
          {rows.map((row, i) => {
            const gap = i > 0 ? gapBetween(rows[i - 1], row) : null
            return (
              <Fragment key={row.day || 'undated'}>
                {gap && <GapLabel gap={gap} viewW={viewW} />}
                <DayRow
                  row={row}
                  viewW={viewW}
                  plotW={plotW}
                  thread={thread}
                  base={base}
                  band={i % 2 === 1}
                  hovered={hovered}
                  onHover={setHovered}
                />
              </Fragment>
            )
          })}
          {thread && <ThreadOverlay rows={rows} width={stackW} base={base} />}
        </Box>
      </Box>

      {capped && (
        <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap">
          <Typography variant="caption" color="text.secondary">
            {t('history.showingDays', { count: DAY_ROWS_CAP })}
          </Typography>
          <Button size="small" onClick={() => setShowAll(true)}>
            {t('history.showAllDays', { count: allRows.length })}
          </Button>
        </Stack>
      )}

      {/* Legend — only the markers that occur in this history. */}
      <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" sx={{ rowGap: 0.5 }}>
        <LegendItem color={theme.palette.text.secondary} label={t('history.saves')} />
        {hasMilestone && (
          <LegendItem color={theme.palette.primary.main} halo label={t('history.milestones')} />
        )}
        {hasRelease && (
          <LegendItem
            color={darken(theme.palette.primary.main, 0.25)}
            halo
            label={t('history.releases')}
          />
        )}
        {hasShare && (
          <LegendItem
            color={theme.palette.text.secondary}
            ring={theme.palette.secondary.main}
            label={t('history.publicShares')}
          />
        )}
      </Stack>
    </Stack>
  )
}

// The legend swatches draw the decoration, not just a colour chip, because the
// markers now differ by shape (halo, outer ring) as well as hue.
function LegendItem({
  color,
  label,
  halo,
  ring,
}: {
  color: string
  label: string
  halo?: boolean
  ring?: string
}) {
  const size = (NODE_R + RING_W + 3) * 2
  const c = size / 2
  return (
    <Stack direction="row" spacing={0.75} alignItems="center">
      <svg width={size} height={size} aria-hidden style={{ display: 'block' }}>
        {ring && (
          <circle cx={c} cy={c} r={NODE_R + RING_W + 1.5} fill="none" stroke={ring} strokeWidth={RING_W} />
        )}
        {halo && (
          <circle
            cx={c}
            cy={c}
            r={NODE_R + RING_W + 1.5}
            fill="none"
            stroke={alpha(color, 0.8)}
            strokeWidth={RING_W}
          />
        )}
        <circle cx={c} cy={c} r={NODE_R - 1} fill={color} />
      </svg>
      <Typography variant="caption" color="text.secondary">
        {label}
      </Typography>
    </Stack>
  )
}
