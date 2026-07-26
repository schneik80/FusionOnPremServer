import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Tooltip, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { useQuery } from '@tanstack/react-query'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faArrowsToDot,
  faMagnifyingGlassMinus,
  faMagnifyingGlassPlus,
  faUpRightFromSquare,
} from '@fortawesome/free-solid-svg-icons'
import { api } from '../api/client'
import { thumbnailSrc } from '../api/thumbnails'
import { ToolBtn } from './canvas/ToolBtn'
import { useNav } from '../state/nav'
import { ItemIcon } from './entityIcons'

// A node in the relationship graph. navId (lineage/item id) drives navigation;
// cvId (componentVersionId) drives the thumbnail; absent navId = not navigable.
export interface GraphNode {
  key: string
  navId?: string
  cvId?: string
  name: string
  kind: string
  secondary?: string
}

// node geometry — width matches the permissions LayersViz boxes for consistency
const W = 100
const H = 92
const HGAP = 124
const VGAP = 172
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
  const accent = theme.palette.primary.main
  const edgeColor = theme.palette.text.secondary
  // Drawings render their preview keyed by item id + the current project's altId
  // (best-effort: a related doc in another project falls back to its kind icon).
  const projectAltId = useNav().project?.altId

  // --- layout (depth-1: focus centred, relations fanned in one row) ---
  const placed = useMemo(() => {
    const n = relations.length
    const rels = relations.map((r, i) => ({
      ...r,
      isFocus: false,
      x: n > 1 ? (i - (n - 1) / 2) * HGAP : 0,
      y: direction === 'down' ? VGAP : -VGAP,
    }))
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
      relations.map((_, i) => {
        const rx = (relations.length > 1 ? (i - (relations.length - 1) / 2) * HGAP : 0) + ox
        const ry = (direction === 'down' ? VGAP : -VGAP) + oy
        const fx = 0 + ox
        const fy = 0 + oy
        // always draw from the upper endpoint down to the lower one
        return direction === 'down'
          ? edgePath(fx, fy + H / 2, rx, ry - H / 2)
          : edgePath(rx, ry + H / 2, fx, fy - H / 2)
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
        bgcolor: alpha(theme.palette.text.primary, theme.palette.mode === 'dark' ? 0.03 : 0.02),
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
          {edges.map((d, i) => (
            <path key={i} d={d} fill="none" stroke={edgeColor} strokeOpacity={0.6} strokeWidth={1.5} />
          ))}
        </svg>
        {placed.map((p) => (
          <NodeBox
            key={p.key}
            node={p}
            left={p.x + ox - W / 2}
            top={p.y + oy - H / 2}
            accent={accent}
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
  accent,
  projectAltId,
  onNavigate,
}: {
  node: Placed
  left: number
  top: number
  accent: string
  projectAltId?: string
  onNavigate: (n: GraphNode) => void
}) {
  const { t } = useTranslation('details')
  const [imgFailed, setImgFailed] = useState(false)
  const [hovered, setHovered] = useState(false)
  const canNav = !node.isFocus && !!node.navId
  const thumbSrc = thumbnailSrc({ kind: node.kind, cvId: node.cvId, itemId: node.navId, projectAltId })
  const showThumb = !!thumbSrc && !imgFailed

  const box = (
    <Box
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onMouseDown={(e) => e.stopPropagation()} // don't start a pan when pressing a node
      onClick={() => canNav && onNavigate(node)}
      sx={{
        position: 'absolute',
        left,
        top,
        width: W,
        height: H,
        p: 0.75,
        border: node.isFocus ? 2 : 1,
        borderRadius: 1,
        borderColor: node.isFocus ? accent : hovered && canNav ? alpha(accent, 0.6) : 'divider',
        bgcolor: hovered && canNav ? alpha(accent, 0.1) : 'background.paper',
        cursor: canNav ? 'pointer' : 'default',
        boxShadow: hovered && canNav ? 2 : 0,
        transition: 'border-color .1s, background-color .1s, box-shadow .1s',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 0.25,
        overflow: 'hidden',
      }}
    >
      {/* nav glyph on hover */}
      {canNav && (
        <Box sx={{ position: 'absolute', top: 3, right: 4, color: accent, opacity: hovered ? 1 : 0, transition: 'opacity .1s' }}>
          <FontAwesomeIcon icon={faUpRightFromSquare} style={{ fontSize: 10 }} />
        </Box>
      )}
      <Box sx={{ width: '100%', height: 46, display: 'grid', placeItems: 'center', flexShrink: 0 }}>
        {showThumb ? (
          <Box
            component="img"
            src={thumbSrc!}
            alt=""
            onError={() => setImgFailed(true)}
            sx={{ maxWidth: '100%', maxHeight: 46, objectFit: 'contain', borderRadius: 0.5 }}
          />
        ) : (
          <ItemIcon item={{ kind: node.kind }} style={{ fontSize: 22, color: alpha(accent, 0.7) }} />
        )}
      </Box>
      <Typography variant="caption" fontWeight={600} noWrap sx={{ width: '100%', textAlign: 'center', lineHeight: 1.2 }} title={node.name}>
        {node.name}
      </Typography>
      <Typography variant="caption" sx={{ fontSize: 9, color: 'text.secondary', textTransform: 'capitalize', lineHeight: 1 }}>
        {node.isFocus ? t('relation.thisDocument') : node.kind}
      </Typography>
    </Box>
  )

  if (!canNav) return box
  return (
    <Tooltip title={<NodeTooltip navId={node.navId!} name={node.name} />} placement="top" arrow>
      {box}
    </Tooltip>
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
      <Typography variant="caption" sx={{ display: 'flex', alignItems: 'center', gap: 0.5, mt: 0.5, opacity: 0.85 }}>
        <FontAwesomeIcon icon={faUpRightFromSquare} style={{ fontSize: 9 }} /> {t('relation.clickToOpen')}
      </Typography>
    </Box>
  )
}
