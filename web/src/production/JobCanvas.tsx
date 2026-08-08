import {
  faArrowsToDot,
  faDiamond,
  faMagnifyingGlassMinus,
  faMagnifyingGlassPlus,
  faPlus,
  faSquare,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  IconButton,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { useJobGraphMutations } from '../api/queries'
import { ToolBtn } from '../components/canvas/ToolBtn'
import { StepNumBadge } from './chips'
import { resultColor } from './resultcolors'
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

// Decision geometry: a rounded-diamond head carrying the question, fused to a
// rounded-rect strip of result rows. A single rhombus holding the whole list
// would give each row a different usable width — w(y) = W·(1−|2y/H−1|), so the
// top and bottom rows are a third of the middle one — and put each result's
// port at a different x, fanning the edge curves out unevenly. Splitting the
// shape keeps every row the same width and every port on one vertical edge.
const DEC_W = 208
const DIA_H = 60
const DEC_ROW_H = 22
const DEC_PAD = 6
const DIA_R = 14 // corner radius of the rhombus

const isDec = (st: { kind?: string }) => st.kind === 'decision'
const nodeW = (st: ProdStep) => (isDec(st) ? DEC_W : W)
const stripH = (n: number) => (n ? DEC_PAD * 2 + n * DEC_ROW_H : 0)

// Node size is a pure function of persisted data — never a measured layout.
// Measuring would mean a DOM read per node per frame and a render→measure→
// re-render loop, which is exactly what the memo/liveRef design below exists
// to avoid.
const nodeH = (st: ProdStep) => (isDec(st) ? DIA_H + stripH(st.results.length) : H)

// Where an incoming edge lands: the diamond's left point, or a step's mid-height.
const inPortY = (st: ProdStep) => (isDec(st) ? DIA_H / 2 : H / 2)
// Where result i leaves from, on the strip's right edge.
const resultPortY = (i: number) => DIA_H + DEC_PAD + (i + 0.5) * DEC_ROW_H

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))

// Horizontal cubic bezier from a right-hand out-port to a left-hand in-port —
// reads as a left-to-right process flow.
function edgePath(sx: number, sy: number, ex: number, ey: number): string {
  const cx = Math.max(40, Math.abs(ex - sx) * 0.5)
  return `M ${sx} ${sy} C ${sx + cx} ${sy} ${ex - cx} ${ey} ${ex} ${ey}`
}

// roundedDiamondPath draws a rhombus whose four points are rounded: each
// vertex is cut back by r along both incident edges and the gap rejoined with
// a quadratic through the vertex. Built as a template string like every other
// path here (GanttBar's milestone, ganttMath's arrowHead).
function roundedDiamondPath(w: number, h: number, r: number): string {
  const pts: [number, number][] = [
    [w / 2, 0], // top
    [w, h / 2], // right
    [w / 2, h], // bottom
    [0, h / 2], // left
  ]
  // Point r along the edge from `a` toward `b`, capped at the midpoint so the
  // cutbacks of two adjacent vertices can never cross on a short edge.
  const cut = (a: [number, number], b: [number, number]): [number, number] => {
    const dx = b[0] - a[0]
    const dy = b[1] - a[1]
    const t = Math.min(0.5, r / Math.hypot(dx, dy))
    return [a[0] + dx * t, a[1] + dy * t]
  }
  let d = ''
  for (let i = 0; i < 4; i++) {
    const v = pts[i]
    const prev = pts[(i + 3) % 4]
    const next = pts[(i + 1) % 4]
    const from = cut(v, prev)
    const to = cut(v, next)
    d += i === 0 ? `M ${from[0]} ${from[1]} ` : `L ${from[0]} ${from[1]} `
    d += `Q ${v[0]} ${v[1]} ${to[0]} ${to[1]} `
  }
  return `${d}Z`
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
  const [edgeDraw, setEdgeDraw] = useState<{
    from: string
    /** the decision result being branched from; undefined from a plain step */
    fromResultId?: string
    lx: number
    ly: number
  } | null>(null)
  // The node being renamed in place. Only the id lives up here — the draft
  // string is StepNode's own state, so typing re-renders one node.
  const [renamingId, setRenamingId] = useState<string | null>(null)
  // Which node type the "+" button creates. A palette rather than two buttons
  // so the choice is visible while you build, not re-picked each time.
  const [newKind, setNewKind] = useState<'step' | 'decision'>('step')

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
      maxX = Math.max(maxX, st.x + nodeW(st))
      maxY = Math.max(maxY, st.y + nodeH(st))
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
        graph.addEdge.mutate({
          from: edgeDraw.from,
          to: target,
          fromResultId: edgeDraw.fromResultId,
        })
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
    // Height of the node that doesn't exist yet — a fresh decision has no
    // results, so this is its one-row minimum.
    const newH = newKind === 'decision' ? DIA_H : H
    const newW = newKind === 'decision' ? DEC_W : W
    const newInY = newKind === 'decision' ? DIA_H / 2 : H / 2
    // A decision branches from a result, so chain off the first one with no
    // edge yet; a decision with no results has nothing to branch from.
    const wired = from ? new Set(job.edges.filter((e) => e.from === from.id).map((e) => e.fromResultId)) : null
    const branch = from && isDec(from) ? from.results.find((r) => !wired!.has(r.id)) : undefined
    const canChain = !!from && (!isDec(from) || !!branch)
    const fromY = from ? (branch ? resultPortY(from.results.indexOf(branch)) : inPortY(from)) : 0

    const pos =
      from && canChain
        ? // Line the new node's in-port up with the port it leaves from.
          { x: from.x + nodeW(from) + GAP, y: from.y + fromY - newInY }
        : {
            // View center in graph coords, offset so the node lands centered.
            x: (vp.clientWidth / 2 - view.tx) / view.scale - ox - newW / 2,
            y: (vp.clientHeight / 2 - view.ty) / view.scale - oy - newH / 2,
          }
    graph.addStep.mutate(
      {
        kind: newKind,
        title: t(newKind === 'decision' ? 'canvas.newDecisionTitle' : 'canvas.newStepTitle', {
          num: job.steps.length + 1,
        }),
        x: pos.x,
        y: pos.y,
      },
      {
        onSuccess: (j) => {
          const created = j.steps[j.steps.length - 1]
          if (!created) return
          onSelectStep(created.id)
          if (from && canChain) {
            graph.addEdge.mutate({ from: from.id, to: created.id, fromResultId: branch?.id })
          }
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

  const handlePortDown = useCallback((e: React.MouseEvent, stepId: string, resultId?: string) => {
    e.stopPropagation()
    const rect = vpRef.current?.getBoundingClientRect()
    if (!rect) return
    setView((v) => {
      // Read the current view without depending on it: setView's updater gives
      // us the live value, and we only use it to seed the preview endpoint.
      setEdgeDraw({
        from: stepId,
        fromResultId: resultId,
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
  // Port geometry, built in the same pass as the id index so edge routing
  // stays two map lookups per edge. At the caps (400 edges x 200 steps) a
  // per-edge nodeH()/indexOf walk would re-run on every mousemove render.
  const anchors = useMemo(() => {
    const m = new Map<string, { step: ProdStep; w: number; inY: number; out: Map<string, number> }>()
    for (const s of job.steps) {
      const out = new Map<string, number>()
      if (isDec(s)) s.results.forEach((r, i) => out.set(r.id, resultPortY(i)))
      else out.set('', H / 2)
      m.set(s.id, { step: s, w: nodeW(s), inY: inPortY(s), out })
    }
    return m
  }, [job.steps])

  // Stroke colour is resolved here rather than in the JSX: the render below
  // runs on every pan frame, and useTheme lookups per edge would too.
  const edges = useMemo(() => {
    const out: { id: string; d: string; stroke: string; opacity: number }[] = []
    for (const e of job.edges) {
      const from = anchors.get(e.from)
      const to = anchors.get(e.to)
      if (!from || !to) continue
      // An edge whose result was deleted has no port to leave from. The store
      // cascades those away, so this is belt-and-braces against a hand-edited
      // file rather than a state the UI can reach.
      const fromY = from.out.get(e.fromResultId ?? '')
      if (fromY === undefined) continue
      const fp = dragPos?.id === e.from ? dragPos : { x: from.step.x, y: from.step.y }
      const tp = dragPos?.id === e.to ? dragPos : { x: to.step.x, y: to.step.y }
      const branch = e.fromResultId
        ? from.step.results.find((r) => r.id === e.fromResultId)
        : undefined
      out.push({
        id: e.id,
        d: edgePath(fp.x + from.w + ox, fp.y + fromY + oy, tp.x + ox, tp.y + to.inY + oy),
        stroke: branch ? resultColor(branch.color, theme) : edgeColor,
        opacity: branch ? 0.9 : 0.55,
      })
    }
    return out
  }, [job.edges, anchors, dragPos, ox, oy, theme, edgeColor])

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
            <path
              key={e.id}
              d={e.d}
              fill="none"
              stroke={e.stroke}
              strokeOpacity={e.opacity}
              strokeWidth={1.75}
            />
          ))}
          {edgeDraw &&
            (() => {
              const a = anchors.get(edgeDraw.from)
              const fromY = a?.out.get(edgeDraw.fromResultId ?? '')
              if (!a || fromY === undefined) return null
              const p = posOf(a.step)
              const branch = edgeDraw.fromResultId
                ? a.step.results.find((r) => r.id === edgeDraw.fromResultId)
                : undefined
              return (
                <path
                  d={edgePath(p.x + a.w + ox, p.y + fromY + oy, edgeDraw.lx, edgeDraw.ly)}
                  fill="none"
                  stroke={branch ? resultColor(branch.color, theme) : accent}
                  strokeWidth={2}
                  strokeDasharray="5 4"
                />
              )
            })()}
        </svg>

        {job.steps.map((st) => {
          const p = posOf(st)
          const Node = isDec(st) ? DecisionNode : StepNode
          return (
            <Node
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
          <>
            {/* Node palette. stopPropagation matters twice over here: without
                it the viewport starts a pan AND clears the selection, which is
                what "+" chains from. */}
            <ToggleButtonGroup
              size="small"
              exclusive
              value={newKind}
              onMouseDown={(e) => e.stopPropagation()}
              onChange={(_, v) => v && setNewKind(v)}
              sx={{
                bgcolor: 'background.paper',
                '& .MuiToggleButton-root': { py: 0.25, px: 0.9, border: 1, borderColor: 'divider' },
              }}
            >
              <ToggleButton value="step" aria-label={t('canvas.paletteStep')}>
                <Tooltip title={t('canvas.paletteStep')}>
                  <FontAwesomeIcon icon={faSquare} style={{ fontSize: 11 }} />
                </Tooltip>
              </ToggleButton>
              <ToggleButton value="decision" aria-label={t('canvas.paletteDecision')}>
                <Tooltip title={t('canvas.paletteDecision')}>
                  <FontAwesomeIcon icon={faDiamond} style={{ fontSize: 11 }} />
                </Tooltip>
              </ToggleButton>
            </ToggleButtonGroup>
            <Tooltip title={t(newKind === 'decision' ? 'canvas.addDecision' : 'canvas.addStep')}>
              <IconButton
                size="small"
                aria-label={t(newKind === 'decision' ? 'canvas.addDecision' : 'canvas.addStep')}
                onMouseDown={(e) => e.stopPropagation()}
                onClick={addStep}
                sx={{ bgcolor: 'primary.main', color: 'primary.contrastText', '&:hover': { bgcolor: 'primary.dark' } }}
              >
                <FontAwesomeIcon icon={faPlus} style={{ fontSize: 12 }} />
              </IconButton>
            </Tooltip>
          </>
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

// Both node kinds take the same props, so the render loop can pick between
// them with one ternary.
interface NodeProps {
  step: ProdStep
  left: number
  top: number
  accent: string
  selected: boolean
  renaming: boolean
  canWrite: boolean
  onBodyDown: (e: React.MouseEvent, stepId: string) => void
  onBodyUp: (e: React.MouseEvent, stepId: string) => void
  /** resultId binds the edge to a decision branch; omitted from a plain step */
  onPortDown: (e: React.MouseEvent, stepId: string, resultId?: string) => void
  onRenameStart: (stepId: string) => void
  /** next === null cancels; otherwise commit (blank and unchanged are no-ops) */
  onRenameEnd: (stepId: string, next: string | null) => void
  onEnter: (stepId: string) => void
  onLeave: (stepId: string) => void
}

// OutPort is the draggable connector dot. Shared so a decision's per-result
// ports and a step's single port cannot drift apart in size or hit area — the
// edge geometry anchors to their centres.
function OutPort({
  top,
  color,
  visible,
  onMouseDown,
}: {
  top: number
  color: string
  visible: boolean
  onMouseDown: (e: React.MouseEvent) => void
}) {
  return (
    <Box
      onMouseDown={onMouseDown}
      sx={{
        position: 'absolute',
        right: -PORT_R,
        top: top - PORT_R,
        width: PORT_R * 2,
        height: PORT_R * 2,
        borderRadius: '50%',
        bgcolor: color,
        border: 2,
        borderColor: 'background.paper',
        cursor: 'crosshair',
        opacity: visible ? 1 : 0.35,
        transition: 'opacity .1s',
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
}: NodeProps) {
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
          <span>
            <OutPort
              top={H / 2}
              color={accent}
              visible={hovered}
              onMouseDown={(e) => onPortDown(e, step.id)}
            />
          </span>
        </Tooltip>
      )}
    </Box>
  )
})

// DecisionNode is a branch point: a rounded-diamond head carrying the question,
// fused to a strip of result rows, each with its own out-port and a colored
// underline. The rhombus is an inline SVG BACKGROUND with the title as an HTML
// overlay — drawing the node itself in SVG would cost MUI theming, the Tooltip,
// and noWrap ellipsis, all of which the canvas's HTML nodes get for free.
const DecisionNode = memo(function DecisionNode({
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
}: NodeProps) {
  const { t } = useTranslation('production')
  const theme = useTheme()
  const [hovered, setHovered] = useState(false)
  const border = selected ? accent : hovered ? alpha(accent, 0.6) : theme.palette.divider
  const results = step.results

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
        width: DEC_W,
        height: DIA_H + stripH(results.length),
        cursor: canWrite ? 'grab' : 'pointer',
      }}
    >
      {/* the head */}
      <Box sx={{ position: 'absolute', inset: `0 0 auto 0`, height: DIA_H }}>
        <svg width={DEC_W} height={DIA_H} style={{ display: 'block' }}>
          <path
            d={roundedDiamondPath(DEC_W, DIA_H, DIA_R)}
            fill={theme.palette.background.paper}
            stroke={border}
            strokeWidth={selected ? 2 : 1}
            style={{ transition: 'stroke .1s' }}
          />
        </svg>
        {/* Title overlaid on the rhombus, inset to the width the shape
            actually offers at mid-height minus the sloping sides. */}
        <Box
          sx={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            px: `${DEC_W / 4}px`,
          }}
        >
          {renaming ? (
            <StepNameInput title={step.title} onEnd={(next) => onRenameEnd(step.id, next)} />
          ) : (
            <Typography
              variant="body2"
              fontWeight={600}
              noWrap
              sx={{ minWidth: 0, textAlign: 'center' }}
              title={step.title}
            >
              {step.title}
            </Typography>
          )}
        </Box>
      </Box>

      {/* the result strip */}
      {results.length > 0 && (
        <Box
          sx={{
            position: 'absolute',
            top: DIA_H,
            left: DEC_W * 0.14,
            right: DEC_W * 0.14,
            py: `${DEC_PAD}px`,
            border: selected ? 2 : 1,
            borderColor: border,
            borderRadius: 1.5,
            bgcolor: 'background.paper',
            boxShadow: hovered || selected ? 3 : 0,
            transition: 'border-color .1s, box-shadow .1s',
          }}
        >
          {results.map((r) => {
            const color = resultColor(r.color, theme)
            return (
              <Box
                key={r.id}
                sx={{
                  height: DEC_ROW_H,
                  px: 1,
                  display: 'flex',
                  alignItems: 'center',
                  minWidth: 0,
                }}
              >
                <Typography
                  variant="caption"
                  noWrap
                  title={r.label}
                  sx={{
                    fontSize: 11,
                    minWidth: 0,
                    borderBottom: `2px solid ${color}`,
                    lineHeight: 1.5,
                  }}
                >
                  {r.label}
                </Typography>
              </Box>
            )
          })}
        </Box>
      )}

      {/* One out-port per result, on the node's right edge rather than the
          strip's, so every branch leaves from the same x and the edge curves
          stay consistent. */}
      {canWrite &&
        results.map((r, i) => (
          <Tooltip key={r.id} title={t('canvas.dragToConnectResult', { label: r.label })} placement="right">
            <span>
              <OutPort
                top={resultPortY(i)}
                color={resultColor(r.color, theme)}
                visible={hovered}
                onMouseDown={(e) => onPortDown(e, step.id, r.id)}
              />
            </span>
          </Tooltip>
        ))}

      {/* A decision with no results has nothing to branch on, so it has no
          ports either — say why rather than leaving a dead node. */}
      {canWrite && results.length === 0 && (
        <Typography
          variant="caption"
          sx={{
            position: 'absolute',
            top: DIA_H + 2,
            left: 0,
            right: 0,
            textAlign: 'center',
            color: 'text.disabled',
            fontSize: 10,
            pointerEvents: 'none',
          }}
        >
          {t('canvas.decisionNeedsResults')}
        </Typography>
      )}
    </Box>
  )
})
