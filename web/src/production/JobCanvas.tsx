import {
  faArrowsToDot,
  faMagnifyingGlassMinus,
  faMagnifyingGlassPlus,
  faPlus,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, IconButton, TextField, Tooltip, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { useJobGraphMutations } from '../api/queries'
import { ToolBtn } from '../components/canvas/ToolBtn'
import { StepNumBadge } from './chips'
import type { Job, ProdStep } from './types'

// JobCanvas is the interactive flow editor: a pan/zoom SVG canvas (the engine
// is lifted from RelationGraph — view={scale,tx,ty}, wheel zoom-at-cursor,
// drag-pan, fit() via ResizeObserver) with draggable, positioned step nodes
// and hand-drawn cubic-bezier edges. Steps carry their own x/y (persisted), so
// a drag PATCHes the step on release; edges are drawn from a node's out-port to
// a target node. No graph library — inline SVG + MUI sx transitions, matching
// the rest of the app.

const W = 176
const H = 74
const PORT_R = 7
const PAD = 80
const MIN_VIEW_H = 320
const GAP = 64 // horizontal gap when a new node is chained off the selection

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))

// Horizontal cubic bezier from a right-hand out-port to a left-hand in-port —
// reads as a left-to-right process flow.
function edgePath(sx: number, sy: number, ex: number, ey: number): string {
  const cx = Math.max(40, Math.abs(ex - sx) * 0.5)
  return `M ${sx} ${sy} C ${sx + cx} ${sy} ${ex - cx} ${ey} ${ex} ${ey}`
}

type Graph = ReturnType<typeof useJobGraphMutations>

export function JobCanvas({
  job,
  canWrite,
  graph,
  selectedStepId,
  onSelectStep,
}: {
  job: Job
  canWrite: boolean
  graph: Graph
  selectedStepId: string | null
  onSelectStep: (id: string | null) => void
}) {
  const { t } = useTranslation('production')
  const theme = useTheme()
  const accent = theme.palette.primary.main
  const edgeColor = theme.palette.text.secondary

  const vpRef = useRef<HTMLDivElement>(null)
  const [view, setView] = useState({ scale: 1, tx: 0, ty: 0 })

  // Interaction refs. Exactly one of pan / nodeDrag / edgeDraw is active.
  const pan = useRef<{ x: number; y: number } | null>(null)
  const nodeDrag = useRef<{
    id: string
    startX: number
    startY: number
    origX: number
    origY: number
    scale: number // view scale captured at drag start — see onMouseMove
    moved: boolean
  } | null>(null)
  const hoverNode = useRef<string | null>(null)

  // Render-affecting drag state.
  const [dragPos, setDragPos] = useState<{ id: string; x: number; y: number } | null>(null)
  const [edgeDraw, setEdgeDraw] = useState<{ from: string; lx: number; ly: number } | null>(null)
  // The node being renamed in place. Only the id lives up here — the draft
  // string is StepNode's own state, so typing re-renders one node.
  const [renamingId, setRenamingId] = useState<string | null>(null)

  // Effective graph-space position of a step (drag override wins).
  const posOf = (st: ProdStep) => (dragPos?.id === st.id ? dragPos : { x: st.x, y: st.y })

  // Bounds over the PERSISTED node positions only — deliberately not the live
  // drag position. Folding dragPos in would shift the layer origin (ox/oy)
  // mid-drag whenever the leftmost/topmost node moves outward, sliding every
  // other node under the cursor. The dragged node may render outside these
  // bounds during the move (the SVG has overflow: visible); bounds catch up
  // when the position PATCH lands.
  const bounds = useMemo(() => {
    if (job.steps.length === 0) return { minX: 0, minY: 0, w: 600, h: 400 }
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const st of job.steps) {
      minX = Math.min(minX, st.x)
      minY = Math.min(minY, st.y)
      maxX = Math.max(maxX, st.x + W)
      maxY = Math.max(maxY, st.y + H)
    }
    return { minX: minX - PAD, minY: minY - PAD, w: maxX - minX + PAD * 2, h: maxY - minY + PAD * 2 }
  }, [job.steps])
  const ox = -bounds.minX
  const oy = -bounds.minY

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
  // Fit once on first mount / when the job changes identity. Subsequent edits
  // must not yank the viewport, so this deliberately depends only on job.id.
  useEffect(() => {
    fit()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [job.id])

  // Re-fit when the viewport resizes (panel drag, tab show, window resize).
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
    // No zooming while a node drag or edge draw is active: the drag math and
    // the dashed preview are anchored in the current view.
    if (nodeDrag.current || edgeDraw) return
    const rect = vpRef.current?.getBoundingClientRect()
    if (!rect) return
    zoomAt(e.deltaY < 0 ? 1.12 : 0.89, e.clientX - rect.left, e.clientY - rect.top)
  }

  // Screen → layer coordinates (the layer is translated by tx/ty then scaled).
  const toLayer = (clientX: number, clientY: number) => {
    const rect = vpRef.current!.getBoundingClientRect()
    return {
      lx: (clientX - rect.left - view.tx) / view.scale,
      ly: (clientY - rect.top - view.ty) / view.scale,
    }
  }

  const onMouseMove = (e: React.MouseEvent) => {
    if (nodeDrag.current) {
      const d = nodeDrag.current
      // Divide by the scale captured at drag START: the total-delta math must
      // never be rescaled retroactively by a zoom change mid-drag.
      const dx = (e.clientX - d.startX) / d.scale
      const dy = (e.clientY - d.startY) / d.scale
      if (Math.abs(dx) > 2 || Math.abs(dy) > 2) d.moved = true
      setDragPos({ id: d.id, x: d.origX + dx, y: d.origY + dy })
      return
    }
    if (edgeDraw) {
      const { lx, ly } = toLayer(e.clientX, e.clientY)
      setEdgeDraw((s) => (s ? { ...s, lx, ly } : s))
      return
    }
    if (pan.current) {
      const dx = e.clientX - pan.current.x
      const dy = e.clientY - pan.current.y
      pan.current = { x: e.clientX, y: e.clientY }
      setView((v) => ({ ...v, tx: v.tx + dx, ty: v.ty + dy }))
    }
  }

  const endInteractions = () => {
    // Persist a node move on release (only if it actually moved).
    if (nodeDrag.current && dragPos && nodeDrag.current.moved) {
      const { id } = nodeDrag.current
      const { x, y } = dragPos
      graph.updateStep.mutate(
        { stepId: id, patch: { x, y } },
        { onSettled: () => setDragPos((p) => (p?.id === id ? null : p)) },
      )
    } else {
      setDragPos(null)
    }
    // Complete an edge draw if released over a different node.
    if (edgeDraw) {
      const target = hoverNode.current
      if (target && target !== edgeDraw.from) {
        graph.addEdge.mutate({ from: edgeDraw.from, to: target })
      }
      setEdgeDraw(null)
    }
    nodeDrag.current = null
    pan.current = null
  }

  // addStep places a new node and, when a step is selected, chains it: the
  // node lands to the right of the selection at the same height and the edge
  // is drawn for you, so building a run is one click per step instead of
  // add-then-drag-then-connect. With nothing selected it falls back to the
  // viewport centre, unconnected — the original behaviour.
  //
  // `selected` is resolved against the live steps rather than trusted: the
  // selection id outlives a delete (it is owned by JobDetail, which does not
  // clear it), so a stale id would otherwise place the node relative to a
  // node that is gone.
  const addStep = () => {
    const vp = vpRef.current
    if (!vp) return
    const from = job.steps.find((s) => s.id === selectedStepId) ?? null
    const pos = from
      ? { x: from.x + W + GAP, y: from.y }
      : {
          // View center in graph coords, offset so the node lands centered.
          x: (vp.clientWidth / 2 - view.tx) / view.scale - ox - W / 2,
          y: (vp.clientHeight / 2 - view.ty) / view.scale - oy - H / 2,
        }
    graph.addStep.mutate(
      { title: t('canvas.newStepTitle', { num: job.steps.length + 1 }), x: pos.x, y: pos.y },
      {
        onSuccess: (j) => {
          const created = j.steps[j.steps.length - 1]
          if (!created) return
          onSelectStep(created.id)
          if (from) graph.addEdge.mutate({ from: from.id, to: created.id })
        },
      },
    )
  }

  // Node event handlers are hoisted out of the render loop and made identity-
  // stable, so memo(StepNode) actually holds: with per-node closures every node
  // re-rendered on every mousemove (N x MUI sx + Tooltip work per frame just to
  // move one). They read live state through refs instead of closing over it.
  const liveRef = useRef({ job, dragPos, scale: view.scale, canWrite, onSelectStep })
  liveRef.current = { job, dragPos, scale: view.scale, canWrite, onSelectStep }
  const graphRef = useRef(graph)
  graphRef.current = graph

  const handleBodyDown = useCallback((e: React.MouseEvent, stepId: string) => {
    e.stopPropagation()
    const { job: j, dragPos: dp, scale, canWrite: cw, onSelectStep: sel } = liveRef.current
    if (!cw) {
      sel(stepId)
      return
    }
    const st = j.steps.find((s) => s.id === stepId)
    if (!st) return
    const p = dp?.id === stepId ? dp : { x: st.x, y: st.y }
    nodeDrag.current = {
      id: stepId,
      startX: e.clientX,
      startY: e.clientY,
      origX: p.x,
      origY: p.y,
      scale, // captured at drag start; see onMouseMove
      moved: false,
    }
  }, [])

  const handleBodyUp = useCallback((_e: React.MouseEvent, stepId: string) => {
    // A press that didn't move is a select/open.
    if (nodeDrag.current && !nodeDrag.current.moved) liveRef.current.onSelectStep(stepId)
  }, [])

  const handlePortDown = useCallback((e: React.MouseEvent, stepId: string) => {
    e.stopPropagation()
    const rect = vpRef.current?.getBoundingClientRect()
    if (!rect) return
    setView((v) => {
      // Read the current view without depending on it: setView's updater gives
      // us the live value, and we only use it to seed the preview endpoint.
      setEdgeDraw({
        from: stepId,
        lx: (e.clientX - rect.left - v.tx) / v.scale,
        ly: (e.clientY - rect.top - v.ty) / v.scale,
      })
      return v
    })
  }, [])

  const handleRenameStart = useCallback((stepId: string) => {
    if (!liveRef.current.canWrite) return
    // A drag may be half-started from the double-click's first mousedown;
    // clear it so releasing over the input doesn't PATCH a position.
    nodeDrag.current = null
    setRenamingId(stepId)
  }, [])

  const handleRenameEnd = useCallback((stepId: string, next: string | null) => {
    setRenamingId((cur) => (cur === stepId ? null : cur))
    if (next === null) return // escaped
    const trimmed = next.trim()
    const st = liveRef.current.job.steps.find((s) => s.id === stepId)
    // Blank reverts, unchanged is a no-op: a stray double-click must not bump
    // the step's UpdatedAt.
    if (!trimmed || !st || trimmed === st.title) return
    graphRef.current.updateStep.mutate({ stepId, patch: { title: trimmed } })
  }, [])

  const handleEnter = useCallback((stepId: string) => {
    hoverNode.current = stepId
  }, [])
  const handleLeave = useCallback((stepId: string) => {
    if (hoverNode.current === stepId) hoverNode.current = null
  }, [])

  // Edge geometry, rebuilt only when something that moves it changes. Endpoints
  // resolve through a Map rather than a linear find per edge — at the caps
  // (400 edges x 200 steps) the naive version is ~160k comparisons, re-run on
  // every mousemove-driven render.
  const edges = useMemo(() => {
    const byId = new Map(job.steps.map((s) => [s.id, s]))
    const out: { id: string; d: string }[] = []
    for (const e of job.edges) {
      const from = byId.get(e.from)
      const to = byId.get(e.to)
      if (!from || !to) continue
      const fp = dragPos?.id === from.id ? dragPos : { x: from.x, y: from.y }
      const tp = dragPos?.id === to.id ? dragPos : { x: to.x, y: to.y }
      out.push({
        id: e.id,
        d: edgePath(fp.x + W + ox, fp.y + H / 2 + oy, tp.x + ox, tp.y + H / 2 + oy),
      })
    }
    return out
  }, [job.steps, job.edges, dragPos, ox, oy])

  return (
    <Box
      ref={vpRef}
      onWheel={onWheel}
      onMouseDown={(e) => {
        // Empty-canvas press starts a pan and clears the selection.
        pan.current = { x: e.clientX, y: e.clientY }
        onSelectStep(null)
      }}
      onMouseMove={onMouseMove}
      onMouseUp={endInteractions}
      onMouseLeave={endInteractions}
      sx={{
        position: 'relative',
        flex: 1,
        minHeight: MIN_VIEW_H,
        overflow: 'hidden',
        bgcolor: 'background.canvas',
        cursor: pan.current ? 'grabbing' : 'grab',
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
        <svg
          width={bounds.w}
          height={bounds.h}
          style={{ position: 'absolute', inset: 0, overflow: 'visible' }}
        >
          {edges.map((e) => (
            <path key={e.id} d={e.d} fill="none" stroke={edgeColor} strokeOpacity={0.55} strokeWidth={1.75} />
          ))}
          {edgeDraw &&
            (() => {
              const from = job.steps.find((s) => s.id === edgeDraw.from)
              if (!from) return null
              const p = posOf(from)
              return (
                <path
                  d={edgePath(p.x + W + ox, p.y + H / 2 + oy, edgeDraw.lx, edgeDraw.ly)}
                  fill="none"
                  stroke={accent}
                  strokeWidth={2}
                  strokeDasharray="5 4"
                />
              )
            })()}
        </svg>

        {job.steps.map((st) => {
          const p = posOf(st)
          return (
            <StepNode
              key={st.id}
              step={st}
              left={p.x + ox}
              top={p.y + oy}
              accent={accent}
              selected={st.id === selectedStepId}
              renaming={st.id === renamingId}
              canWrite={canWrite}
              onBodyDown={handleBodyDown}
              onBodyUp={handleBodyUp}
              onPortDown={handlePortDown}
              onRenameStart={handleRenameStart}
              onRenameEnd={handleRenameEnd}
              onEnter={handleEnter}
              onLeave={handleLeave}
            />
          )
        })}
      </Box>

      {/* empty state */}
      {job.steps.length === 0 && (
        <Box
          sx={{
            position: 'absolute',
            inset: 0,
            display: 'grid',
            placeItems: 'center',
            color: 'text.secondary',
            fontSize: 13,
            pointerEvents: 'none',
            textAlign: 'center',
            px: 3,
          }}
        >
          {canWrite ? t('canvas.emptyCanWrite') : t('canvas.emptyReadOnly')}
        </Box>
      )}

      {/* Double-click-to-rename and chain-from-the-selection are both
          invisible affordances; the hint is the only thing that surfaces them. */}
      {canWrite && job.steps.length > 0 && (
        <Typography
          variant="caption"
          sx={{
            position: 'absolute',
            left: 8,
            bottom: 6,
            color: 'text.disabled',
            pointerEvents: 'none',
            fontSize: 11,
          }}
        >
          {selectedStepId ? t('canvas.hintChain') : t('canvas.hintRename')}
        </Typography>
      )}

      {/* toolbar */}
      <Box sx={{ position: 'absolute', top: 8, right: 8, display: 'flex', gap: 0.5, alignItems: 'center' }}>
        {canWrite && (
          <Tooltip title={t('canvas.addStep')}>
            <IconButton
              size="small"
              onMouseDown={(e) => e.stopPropagation()}
              onClick={addStep}
              sx={{ bgcolor: 'primary.main', color: 'primary.contrastText', '&:hover': { bgcolor: 'primary.dark' } }}
            >
              <FontAwesomeIcon icon={faPlus} style={{ fontSize: 12 }} />
            </IconButton>
          </Tooltip>
        )}
        <ToolBtn label={t('canvas.zoomOut')} icon={faMagnifyingGlassMinus} onClick={() => zoomAt(0.83, (vpRef.current?.clientWidth ?? 0) / 2, (vpRef.current?.clientHeight ?? 0) / 2)} />
        <Typography variant="caption" sx={{ minWidth: 34, textAlign: 'center', fontVariantNumeric: 'tabular-nums', color: 'text.secondary' }}>
          {Math.round(view.scale * 100)}%
        </Typography>
        <ToolBtn label={t('canvas.zoomIn')} icon={faMagnifyingGlassPlus} onClick={() => zoomAt(1.2, (vpRef.current?.clientWidth ?? 0) / 2, (vpRef.current?.clientHeight ?? 0) / 2)} />
        <ToolBtn label={t('canvas.fitToView')} icon={faArrowsToDot} onClick={fit} />
      </Box>
    </Box>
  )
}


// StepNameInput is the in-place rename editor on a canvas node. It owns the
// draft so typing re-renders one node rather than the canvas, and it carries
// three guards the canvas forces on it:
//
//   - userSelect: 'text' — the viewport sets 'none' to keep drags from
//     selecting labels, and that inherits into the input.
//   - mousedown/dblclick stopPropagation — otherwise pressing into the input
//     starts a node drag, and a double-click inside it restarts the rename.
//   - escaped ref — blur fires after Escape, so without it every cancel would
//     immediately commit on the way out.
function StepNameInput({ title, onEnd }: { title: string; onEnd: (next: string | null) => void }) {
  const { t } = useTranslation('production')
  const [draft, setDraft] = useState(title)
  const escaped = useRef(false)

  return (
    <TextField
      autoFocus
      variant="standard"
      value={draft}
      placeholder={t('stepCard.namePlaceholder')}
      onChange={(e) => setDraft(e.target.value)}
      onMouseDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onFocus={(e) => e.currentTarget.select()}
      onBlur={() => onEnd(escaped.current ? null : draft)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
        if (e.key === 'Escape') {
          escaped.current = true
          ;(e.target as HTMLInputElement).blur()
        }
      }}
      sx={{
        flex: 1,
        minWidth: 0,
        userSelect: 'text',
        '& input': { fontWeight: 600, fontSize: 14, py: 0, userSelect: 'text' },
      }}
    />
  )
}

// memo matters here: pan and node-drag set state on every mousemove, so without
// it every node in the job re-runs MUI's sx pipeline and its Tooltip ~60x/sec.
// The handler props are identity-stable (see liveRef above), so the only nodes
// that actually re-render are the ones whose position/selection changed.
const StepNode = memo(function StepNode({
  step,
  left,
  top,
  accent,
  selected,
  renaming,
  canWrite,
  onBodyDown,
  onBodyUp,
  onPortDown,
  onRenameStart,
  onRenameEnd,
  onEnter,
  onLeave,
}: {
  step: ProdStep
  left: number
  top: number
  accent: string
  selected: boolean
  renaming: boolean
  canWrite: boolean
  onBodyDown: (e: React.MouseEvent, stepId: string) => void
  onBodyUp: (e: React.MouseEvent, stepId: string) => void
  onPortDown: (e: React.MouseEvent, stepId: string) => void
  onRenameStart: (stepId: string) => void
  /** next === null cancels; otherwise commit (blank and unchanged are no-ops) */
  onRenameEnd: (stepId: string, next: string | null) => void
  onEnter: (stepId: string) => void
  onLeave: (stepId: string) => void
}) {
  const { t } = useTranslation('production')
  const [hovered, setHovered] = useState(false)
  const docCount = step.planDocs.length
  const slotCount = step.placeholders.length

  return (
    <Box
      onMouseEnter={() => {
        setHovered(true)
        onEnter(step.id)
      }}
      onMouseLeave={() => {
        setHovered(false)
        onLeave(step.id)
      }}
      onMouseDown={(e) => onBodyDown(e, step.id)}
      onMouseUp={(e) => onBodyUp(e, step.id)}
      onDoubleClick={() => onRenameStart(step.id)}
      sx={{
        position: 'absolute',
        left,
        top,
        width: W,
        height: H,
        p: 1,
        border: selected ? 2 : 1,
        borderRadius: 1.5,
        borderColor: selected ? accent : hovered ? alpha(accent, 0.6) : 'divider',
        bgcolor: 'background.paper',
        boxShadow: hovered || selected ? 3 : 0,
        cursor: canWrite ? 'grab' : 'pointer',
        transition: 'border-color .1s, box-shadow .1s',
        display: 'flex',
        flexDirection: 'column',
        gap: 0.5,
        overflow: 'hidden',
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, minWidth: 0 }}>
        <StepNumBadge num={step.num} size={20} />
        {renaming ? (
          <StepNameInput title={step.title} onEnd={(next) => onRenameEnd(step.id, next)} />
        ) : (
          <Typography variant="body2" fontWeight={600} noWrap sx={{ flex: 1, minWidth: 0 }} title={step.title}>
            {step.title}
          </Typography>
        )}
      </Box>
      <Typography variant="caption" color="text.secondary" sx={{ fontSize: 10.5 }}>
        {t('counts.docs', { count: docCount })} · {t('counts.slots', { count: slotCount })}
      </Typography>

      {/* out-port: drag to another node to connect */}
      {canWrite && (
        <Tooltip title={t('canvas.dragToConnect')} placement="right">
          <Box
            onMouseDown={(e) => onPortDown(e, step.id)}
            sx={{
              position: 'absolute',
              right: -PORT_R,
              top: H / 2 - PORT_R,
              width: PORT_R * 2,
              height: PORT_R * 2,
              borderRadius: '50%',
              bgcolor: accent,
              border: 2,
              borderColor: 'background.paper',
              cursor: 'crosshair',
              opacity: hovered ? 1 : 0.35,
              transition: 'opacity .1s',
            }}
          />
        </Tooltip>
      )}
    </Box>
  )
})
