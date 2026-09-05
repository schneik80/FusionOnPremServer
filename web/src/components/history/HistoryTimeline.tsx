import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Button, Checkbox, FormControlLabel, Stack, Tooltip, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { useItemHistory } from '../../api/queries'
import type { VersionSummary } from '../../api/types'
import { NODE_R, RING_W } from '../graphstyle'
import DayRow from './DayRow'
import GapLabel from './GapLabel'
import ThreadOverlay from './ThreadOverlay'
import {
  applyHistoryMarkers,
  bucketByDay,
  CHANGE_R,
  DAY_ROWS_CAP,
  gapBetween,
  GUTTER_W,
  indexBase,
  MIN_VIEW_H,
  plotWidth,
  threadWidth,
  toEvents,
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
// The v3 history (one call per document viewed, beside the details call)
// feeds two things. Always: the milestone name and release label on each save,
// which is what fills a released dot's ring — v2 has an isMilestone flag and
// nothing else. Behind the second checkbox, "Show other changes": the edits
// that made no version — property changes, part numbers — each with its own
// author, so someone who only ever edited properties gets a track too. They
// ride the same bucketing as saves.
//
// The layout maths lives in historyLayout.ts (pure, unit-tested); this file
// measures the panel and decides what to render.

const SEED_W = 720 // pre-measurement width, so the first paint is not a flash

export default function HistoryTimeline({
  versions,
  hubId,
  itemId,
}: {
  versions: VersionSummary[]
  hubId: string | null
  itemId: string
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const [thread, setThread] = useState(false)
  const [showChanges, setShowChanges] = useState(false)
  const [showAll, setShowAll] = useState(false)
  const [hovered, setHovered] = useState<string | null>(null)
  const historyQ = useItemHistory(hubId, itemId)
  const changes = showChanges ? historyQ.data?.changes : undefined
  const marked = useMemo(() => applyHistoryMarkers(versions, historyQ.data?.saves), [versions, historyQ.data])

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

  const allRows = useMemo(() => bucketByDay(toEvents(marked, changes)), [marked, changes])
  const capped = !showAll && allRows.length > DAY_ROWS_CAP
  const rows = capped ? allRows.slice(0, DAY_ROWS_CAP) : allRows

  const shownEvents = useMemo(() => rows.reduce((n, r) => n + r.count, 0), [rows])
  // The cap drops the oldest days, so the thread axis has to start from the
  // oldest index that survived rather than from zero.
  const base = useMemo(() => indexBase(rows), [rows])
  const stackW = thread ? GUTTER_W + threadWidth(shownEvents) : viewW
  const plotW = thread ? threadWidth(shownEvents) : plotWidth(viewW)

  const hasMilestone = marked.some((v) => v.isMilestone)
  const hasRelease = marked.some((v) => !!v.revision)
  const hasShare = marked.some((v) => !!v.publicShare)
  const hasChange = (changes?.length ?? 0) > 0

  // The caption: "N versions", and with the toggle on, what the history
  // fetch is doing — its count, or that it is loading, or that it failed. The
  // failure is inline and small on purpose: the saves-only history is still
  // right, only the extras (rings, release fills) are missing — a hub that is
  // not Collaborative Editing has no v3 history to give.
  const versionCount = t('history.versionCount', { count: versions.length })
  const caption = !showChanges
    ? versionCount
    : t('history.captionWithChanges', {
        versions: versionCount,
        changes: historyQ.data
          ? historyQ.data.changes.length
            ? t('history.otherChanges', { count: historyQ.data.changes.length })
            : t('history.noChanges')
          : historyQ.isError
            ? t('history.changesFailed')
            : t('history.changesLoading'),
      })

  return (
    <Stack spacing={1} sx={{ minHeight: 0, ...(thread ? { height: '100%' } : null) }}>
      <Stack
        direction="row"
        spacing={1}
        alignItems="center"
        justifyContent="space-between"
        flexWrap="wrap"
      >
        <Typography variant="caption" color={historyQ.isError && showChanges ? 'warning.main' : 'text.secondary'}>
          {caption}
        </Typography>
        <Stack direction="row" spacing={1.5} alignItems="center" flexWrap="wrap">
          <Tooltip title={t('history.showChangesHint')} placement="top">
            <FormControlLabel
              control={
                <Checkbox
                  size="small"
                  checked={showChanges}
                  onChange={(e) => setShowChanges(e.target.checked)}
                />
              }
              label={
                <Typography variant="caption" color="text.secondary">
                  {t('history.showChanges')}
                </Typography>
              }
              sx={{ mr: 0 }}
            />
          </Tooltip>
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
          <LegendItem
            color={theme.palette.text.secondary}
            halo={theme.palette.primary.main}
            label={t('history.milestones')}
          />
        )}
        {hasRelease && (
          <LegendItem
            color={theme.palette.primary.main}
            halo={theme.palette.primary.main}
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
        {showChanges && hasChange && (
          <LegendItem color={theme.palette.text.secondary} open label={t('history.otherChangesLegend')} />
        )}
      </Stack>
    </Stack>
  )
}

// The legend swatches draw the decoration, not just a colour chip, because the
// markers differ by shape (halo, outer ring, open ring) as well as hue. `halo`
// and `ring` are colours, not flags: a milestone's ring is the accent while its
// dot stays grey, so the two cannot be derived from one another.
function LegendItem({
  color,
  label,
  halo,
  ring,
  open,
}: {
  color: string
  label: string
  halo?: string
  ring?: string
  /** An edit that made no version: a small open ring instead of a filled dot. */
  open?: boolean
}) {
  const theme = useTheme()
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
            stroke={alpha(halo, 0.8)}
            strokeWidth={RING_W}
          />
        )}
        {open ? (
          <circle cx={c} cy={c} r={CHANGE_R} fill={theme.palette.background.paper} stroke={color} strokeWidth={RING_W} />
        ) : (
          <circle cx={c} cy={c} r={NODE_R - 1} fill={color} />
        )}
      </svg>
      <Typography variant="caption" color="text.secondary">
        {label}
      </Typography>
    </Stack>
  )
}
