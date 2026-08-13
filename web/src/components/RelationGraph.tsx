import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Typography } from '@mui/material'
import { useTheme } from '@mui/material/styles'
import { useQuery } from '@tanstack/react-query'
import { faArrowsToDot, faMagnifyingGlassMinus, faMagnifyingGlassPlus } from '@fortawesome/free-solid-svg-icons'
import { api } from '../api/client'
import { thumbnailSrc } from '../api/thumbnails'
import { ToolBtn } from './canvas/ToolBtn'
import { useNav } from '../state/nav'
import { EntityCard, type CardBadge } from './entitycard/EntityCard'
import { iconForItem } from './icons'

// A node in the relationship graph. navId (lineage/item id) drives navigation;
// cvId (componentVersionId) drives the thumbnail; absent navId = not navigable.
export interface GraphNode {
  key: string
  navId?: string
  cvId?: string
  name: string
  kind: string
  secondary?: string
  // --- local (non-APS) references: a task, a chat channel, a whiteboard, a
  // job or a batch that mentions this document. ---
  // soft draws the edge dashed and mutes the node: a chat mention is a weaker
  // relationship than an assembly occurrence, and the graph shouldn't imply
  // otherwise by drawing them identically.
  soft?: boolean
  // count is how many references live inside that container (three cards on
  // one board, forty messages in one channel), shown as a badge.
  count?: number
  // kindLabel overrides the raw kind string under the name, for kinds that
  // need a translated label rather than a capitalized enum token.
  kindLabel?: string
  // detail is the tooltip's second line for a node that has no hub location to
  // resolve (a chat excerpt, a project name, a step title). Local nodes use it
  // instead of the APS location lookup, which they have no lineage id for.
  detail?: string
  // openable marks a node that answers a click without being a hub document —
  // it opens the record's own dialog (the fls: card dialogs) instead of moving
  // the browser.
  openable?: boolean
}

// Node geometry. A node is the app's shared document card (EntityCard), so it
// is wide and short rather than the tall 100px tile this graph used to draw its
// own version of — H tracks the card's painted height (thumbnail + padding +
// halo) and is what the edge endpoints and fit-to-view bounds are computed
// from.
const W = 300
const H = 88
const HGAP = W + 28
const VGAP = 172
// Relations wrap into rows of at most COLS. A document can be referenced from
// a dozen places once local sources (chat, whiteboards, tasks, jobs, batches)
// join the graph, and one unbounded row would either run thousands of pixels
// wide or fit-to-view down to an unreadable 30%. Rows stack away from the
// focus, so the first row — the structural parents — stays nearest it. Cards
// are three times the width of the old tiles, so a row holds proportionally
// fewer before fit-to-view stops being readable.
const COLS = 3
const ROWGAP = H + 56
// The viewport grows to fill whatever vertical space its tab gives it (see
// the flex:1 below); MIN_VIEW_H is just a floor so a very short panel still
// shows a usable canvas (and scrolls) rather than collapsing.
const MIN_VIEW_H = 260

// vertical cubic bezier from an upper point to a lower point (Drawflow-style)
function edgePath(sx: number, sy: number, ex: number, ey: number): string {
  const cy = Math.abs(ey - sy) * 0.5
  return `M ${sx} ${sy} C ${sx} ${sy + cy} ${ex} ${ey - cy} ${ex} ${ey}`
}

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))

// slot places relation i of n in graph space: rows of COLS, each row centred on
// the focus and pushed one ROWGAP further from it. Layout and edge geometry
// both read from here so they can never drift apart.
function slot(i: number, n: number, direction: 'down' | 'up'): { x: number; y: number } {
  const row = Math.floor(i / COLS)
  const col = i % COLS
  const inRow = Math.min(COLS, n - row * COLS)
  const depth = VGAP + row * ROWGAP
  return {
    x: inRow > 1 ? (col - (inRow - 1) / 2) * HGAP : 0,
    y: direction === 'down' ? depth : -depth,
  }
}

export default function RelationGraph({
  focus,
  relations,
  direction,
  onNavigate,
}: {
  focus: { name: string; kind: string; cvId?: string; itemId?: string }
  relations: GraphNode[]
  direction: 'down' | 'up' // uses = children below; whereUsed = parents above
  onNavigate: (n: GraphNode) => void
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const edgeColor = theme.palette.text.secondary
  // Drawings render their preview keyed by item id + the current project's altId
  // (best-effort: a related doc in another project falls back to its kind icon).
  const projectAltId = useNav().project?.altId

  // --- layout (depth-1: focus centred, relations fanned into rows) ---
  const placed = useMemo(() => {
    const rels = relations.map((r, i) => {
      const { x, y } = slot(i, relations.length, direction)
      return { ...r, isFocus: false, x, y }
    })
    // navId on the focus drives its thumbnail (drawings need the item id); it
    // stays non-navigable because canNav also checks !isFocus.
    return [{ key: '__focus__', name: focus.name, kind: focus.kind, cvId: focus.cvId, navId: focus.itemId, isFocus: true, x: 0, y: 0 }, ...rels]
  }, [focus, relations, direction])

  const PAD = 36
  const bounds = useMemo(() => {
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const p of placed) {
      minX = Math.min(minX, p.x - W / 2)
      maxX = Math.max(maxX, p.x + W / 2)
      minY = Math.min(minY, p.y - H / 2)
      maxY = Math.max(maxY, p.y + H / 2)
    }
    if (!isFinite(minX)) return { minX: 0, minY: 0, w: W, h: H }
    return { minX: minX - PAD, minY: minY - PAD, w: maxX - minX + PAD * 2, h: maxY - minY + PAD * 2 }
  }, [placed])
  const ox = -bounds.minX
  const oy = -bounds.minY

  const edges = useMemo(
    () =>
      relations.map((r, i) => {
        const s = slot(i, relations.length, direction)
        const rx = s.x + ox
        const ry = s.y + oy
        const fx = 0 + ox
        const fy = 0 + oy
        // always draw from the upper endpoint down to the lower one
        const d =
          direction === 'down'
            ? edgePath(fx, fy + H / 2, rx, ry - H / 2)
            : edgePath(rx, ry + H / 2, fx, fy - H / 2)
        return { d, soft: !!r.soft }
      }),
    [relations, direction, ox, oy],
  )

  // --- pan / zoom / fit ---
  const vpRef = useRef<HTMLDivElement>(null)
  const [view, setView] = useState({ scale: 1, tx: 0, ty: 0 })
  const drag = useRef<{ x: number; y: number } | null>(null)

  const fit = () => {
    const vp = vpRef.current
    if (!vp) return
    const scale = clamp(Math.min(vp.clientWidth / bounds.w, vp.clientHeight / bounds.h), 0.3, 1.5)
    setView({
      scale,
      tx: (vp.clientWidth - bounds.w * scale) / 2,
      ty: (vp.clientHeight - bounds.h * scale) / 2,
    })
  }
  // re-fit whenever the content changes
  useEffect(fit, [bounds.w, bounds.h]) // eslint-disable-line react-hooks/exhaustive-deps

  // ...and whenever the viewport itself resizes, so the graph keeps filling
  // the vertical space its tab gives it (panel drag, window resize, tab show).
  const fitRef = useRef(fit)
  fitRef.current = fit
  useEffect(() => {
    const vp = vpRef.current
    if (!vp || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => fitRef.current())
    ro.observe(vp)
    return () => ro.disconnect()
  }, [])

  const zoomAt = (factor: number, cx: number, cy: number) =>
    setView((v) => {
      const scale = clamp(v.scale * factor, 0.3, 2)
      const k = scale / v.scale
      return { scale, tx: cx - (cx - v.tx) * k, ty: cy - (cy - v.ty) * k }
    })

  const onWheel = (e: React.WheelEvent) => {
    e.preventDefault()
    const rect = vpRef.current?.getBoundingClientRect()
    if (!rect) return
    zoomAt(e.deltaY < 0 ? 1.12 : 0.89, e.clientX - rect.left, e.clientY - rect.top)
  }

  return (
    <Box
      ref={vpRef}
      onWheel={onWheel}
      onMouseDown={(e) => {
        drag.current = { x: e.clientX, y: e.clientY }
      }}
      onMouseMove={(e) => {
        if (!drag.current) return
        const dx = e.clientX - drag.current.x
        const dy = e.clientY - drag.current.y
        drag.current = { x: e.clientX, y: e.clientY }
        setView((v) => ({ ...v, tx: v.tx + dx, ty: v.ty + dy }))
      }}
      onMouseUp={() => (drag.current = null)}
      onMouseLeave={() => (drag.current = null)}
      sx={{
        position: 'relative',
        flex: 1,
        minHeight: MIN_VIEW_H,
        overflow: 'hidden',
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
        bgcolor: 'background.canvas',
        cursor: drag.current ? 'grabbing' : 'grab',
        userSelect: 'none',
      }}
    >
      <Box
        sx={{
          position: 'absolute',
          top: 0,
          left: 0,
          width: bounds.w,
          height: bounds.h,
          transformOrigin: '0 0',
          transform: `translate(${view.tx}px, ${view.ty}px) scale(${view.scale})`,
        }}
      >
        <svg width={bounds.w} height={bounds.h} style={{ position: 'absolute', inset: 0, overflow: 'visible' }}>
          {edges.map((e, i) => (
            <path
              key={i}
              d={e.d}
              fill="none"
              stroke={edgeColor}
              strokeOpacity={e.soft ? 0.4 : 0.6}
              strokeWidth={1.5}
              strokeDasharray={e.soft ? '4 4' : undefined}
            />
          ))}
        </svg>
        {placed.map((p) => (
          <NodeBox
            key={p.key}
            node={p}
            left={p.x + ox - W / 2}
            top={p.y + oy - H / 2}
            projectAltId={projectAltId}
            onNavigate={onNavigate}
          />
        ))}
      </Box>

      {/* toolbar */}
      <Box sx={{ position: 'absolute', top: 8, right: 8, display: 'flex', gap: 0.5, alignItems: 'center' }}>
        <ToolBtn label={t('relation.zoomOut')} icon={faMagnifyingGlassMinus} onClick={() => zoomAt(0.83, (vpRef.current?.clientWidth ?? 0) / 2, (vpRef.current?.clientHeight ?? 0) / 2)} />
        <Typography variant="caption" sx={{ minWidth: 34, textAlign: 'center', fontVariantNumeric: 'tabular-nums', color: 'text.secondary' }}>
          {Math.round(view.scale * 100)}%
        </Typography>
        <ToolBtn label={t('relation.zoomIn')} icon={faMagnifyingGlassPlus} onClick={() => zoomAt(1.2, (vpRef.current?.clientWidth ?? 0) / 2, (vpRef.current?.clientHeight ?? 0) / 2)} />
        <ToolBtn label={t('relation.fitToView')} icon={faArrowsToDot} onClick={fit} />
      </Box>
    </Box>
  )
}


type Placed = GraphNode & { isFocus: boolean; x: number; y: number }

function NodeBox({
  node,
  left,
  top,
  projectAltId,
  onNavigate,
}: {
  node: Placed
  left: number
  top: number
  projectAltId?: string
  onNavigate: (n: GraphNode) => void
}) {
  const { t } = useTranslation('details')
  const canNav = !node.isFocus && (!!node.navId || !!node.openable)

  const badges: CardBadge[] =
    node.count && node.count > 1
      ? [{ label: t('relation.refCount', { count: node.count }), tone: 'muted' }]
      : []

  // A hub document resolves its project/folder path on hover — one location
  // call per node would be a fan-out, so it stays inside the tooltip, which
  // only mounts when hovered. A local record (chat, whiteboard, task, job,
  // batch) has no lineage id to resolve and carries its context in `detail`.
  const tooltip = node.isFocus ? undefined : node.navId ? (
    <NodeTooltip navId={node.navId} name={node.name} />
  ) : (
    <LocalNodeTooltip name={node.name} detail={node.detail} />
  )

  return (
    <Box
      // Pressing a node must not start a canvas pan.
      onMouseDown={(e) => e.stopPropagation()}
      sx={{ position: 'absolute', left, top, width: W }}
    >
      <EntityCard
        title={node.name}
        subtitle={node.isFocus ? t('relation.thisDocument') : (node.kindLabel ?? node.kind)}
        thumbUrl={thumbnailSrc({ kind: node.kind, cvId: node.cvId, itemId: node.navId, projectAltId })}
        icon={iconForItem({ kind: node.kind, subtype: '' })}
        iconItem={{ kind: node.kind, subtype: '' }}
        badges={badges}
        tooltip={tooltip}
        // The focus is the subject of the view, not a destination; a soft
        // relation (a chat mention) is dashed so it never reads as structural.
        outline={node.isFocus ? 'accent' : node.soft ? 'soft' : undefined}
        onNavigate={canNav ? () => onNavigate(node) : undefined}
        selectable={canNav}
        fullWidth
      />
    </Box>
  )
}

// LocalNodeTooltip is the tooltip for a reference that lives in our own data.
// It shows what the caller already resolved server-side — the project, the
// channel excerpt, the step title — rather than making a lookup call.
function LocalNodeTooltip({ name, detail }: { name: string; detail?: string }) {
  return (
    <Box sx={{ py: 0.25, maxWidth: 260 }}>
      <Typography variant="caption" sx={{ fontWeight: 600, display: 'block' }}>
        {name}
      </Typography>
      {detail ? (
        <Typography variant="caption" sx={{ display: 'block', opacity: 0.85 }}>
          {detail}
        </Typography>
      ) : null}
    </Box>
  )
}

// NodeTooltip shows the node's location (project + folder path), resolved on
// hover, plus a hint that clicking navigates there.
function NodeTooltip({ navId, name }: { navId: string; name: string }) {
  const { t } = useTranslation('details')
  const nav = useNav()
  const locQ = useQuery({
    queryKey: ['location', nav.hubId, navId],
    queryFn: () => api.itemLocation(nav.hubId!, navId),
    enabled: !!nav.hubId,
    staleTime: 5 * 60 * 1000,
  })
  const loc = locQ.data
  const path = loc ? [loc.projectName, ...loc.folderPath.map((f) => f.name)].filter(Boolean).join('  ›  ') : null
  return (
    <Box sx={{ py: 0.25 }}>
      <Typography variant="caption" sx={{ fontWeight: 600, display: 'block' }}>
        {name}
      </Typography>
      <Typography variant="caption" sx={{ display: 'block', color: 'inherit', opacity: 0.85 }}>
        {locQ.isLoading ? t('relation.locating') : (path ?? t('relation.locationUnavailable'))}
      </Typography>
    </Box>
  )
}
