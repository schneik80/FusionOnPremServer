import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Typography } from '@mui/material'
import { lighten, useTheme } from '@mui/material/styles'
import { faArrowsToDot, faMagnifyingGlassMinus, faMagnifyingGlassPlus } from '@fortawesome/free-solid-svg-icons'
import type { HubOverview, Item } from '../../api/types'
import { ToolBtn } from '../canvas/ToolBtn'
import { Hint } from '../dashboard/shell'
import { useReducedMotion } from './useReducedMotion'

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))
const MIN_VIEW_H = 360
const GOLDEN = Math.PI * (3 - Math.sqrt(5))

// A deterministic starfield for the ambient background (seeded so it never
// reshuffles between renders). Coordinates are in the background svg's
// 100×60 viewBox; each star twinkles between base and peak opacity.
function mulberry32(a: number) {
  return () => {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
const STARS = (() => {
  const r = mulberry32(0x5eed)
  return Array.from({ length: 48 }, () => {
    const base = +(0.1 + r() * 0.3).toFixed(2)
    return {
      x: +(r() * 100).toFixed(2),
      y: +(r() * 60).toFixed(2),
      r: +(0.15 + r() * 0.4).toFixed(2),
      base,
      peak: +Math.min(1, base + 0.45).toFixed(2),
      dur: +(2.6 + r() * 3.6).toFixed(2),
      begin: +(r() * 4).toFixed(2),
    }
  })
})()

// HubConstellation is the opt-in "Explore" mode: every accessible project as a
// node in a pan/zoom star map, sized by how much LOCAL activity it has (tasks,
// batches, chat over the window). Click a node to open the project. The pan/
// zoom engine is the RelationGraph/JobCanvas one, lifted (the established
// precedent for a third canvas). Shared-reference edges between projects would
// need a per-item APS call each, so they are intentionally left to a later
// pass rather than fanned out on open.
export default function HubConstellation({
  projects,
  overview,
  onOpen,
}: {
  projects: Item[]
  overview: HubOverview
  onOpen: (item: Item) => void
}) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const reduced = useReducedMotion()
  const accent = theme.palette.primary.main

  // Per-project local-activity totals, keyed by whichever id the overview used.
  const totals = useMemo(() => {
    const m = new Map<string, number>()
    for (const p of overview.projects) m.set(p.projectId, p.total)
    return m
  }, [overview.projects])

  // Phyllotaxis (sunflower) layout — deterministic, no overlaps, organic. The
  // busiest projects land near the centre because we place sorted by activity.
  const { nodes, bounds } = useMemo(() => {
    const sorted = [...projects].sort((a, b) => totalFor(b) - totalFor(a))
    function totalFor(it: Item): number {
      return totals.get(it.id) ?? (it.altId ? (totals.get(it.altId) ?? 0) : 0)
    }
    const maxTotal = sorted.reduce((m, it) => Math.max(m, totalFor(it)), 0)
    const spacing = 52
    const placed = sorted.map((item, i) => {
      const total = totalFor(item)
      const r = 9 + (maxTotal > 0 ? Math.sqrt(total / maxTotal) * 22 : 0)
      const dist = spacing * Math.sqrt(i + 0.5)
      const th = i * GOLDEN
      return { item, total, r, x: Math.cos(th) * dist, y: Math.sin(th) * dist }
    })
    let minX = Infinity
    let maxX = -Infinity
    let minY = Infinity
    let maxY = -Infinity
    for (const n of placed) {
      minX = Math.min(minX, n.x - n.r)
      maxX = Math.max(maxX, n.x + n.r)
      minY = Math.min(minY, n.y - n.r)
      maxY = Math.max(maxY, n.y + n.r)
    }
    const pad = 80 // room for labels + glow
    const ox = -minX + pad
    const oy = -minY + pad
    return {
      nodes: placed.map((n) => ({ ...n, cx: n.x + ox, cy: n.y + oy })),
      bounds: { w: maxX - minX + pad * 2, h: maxY - minY + pad * 2 },
    }
  }, [projects, totals])

  const vpRef = useRef<HTMLDivElement>(null)
  const [view, setView] = useState({ scale: 1, tx: 0, ty: 0 })
  const drag = useRef<{ x: number; y: number } | null>(null)
  // Whether the current gesture panned. mouseup clears drag.current before the
  // node's click fires, so the "did we pan?" flag must live on its own ref or a
  // drag that ends on a node would wrongly open it.
  const movedRef = useRef(false)
  const [hover, setHover] = useState<string | null>(null)

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
  useEffect(fit, [bounds.w, bounds.h]) // eslint-disable-line react-hooks/exhaustive-deps
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
  const cw = () => (vpRef.current?.clientWidth ?? 0) / 2
  const ch = () => (vpRef.current?.clientHeight ?? 0) / 2

  if (projects.length === 0) return <Hint>{t('dashboards.constellationEmpty')}</Hint>

  const glowEmpty = lighten(accent, 0.4)

  return (
    <Box
      ref={vpRef}
      onWheel={onWheel}
      onMouseDown={(e) => {
        drag.current = { x: e.clientX, y: e.clientY }
        movedRef.current = false
      }}
      onMouseMove={(e) => {
        if (!drag.current) return
        const dx = e.clientX - drag.current.x
        const dy = e.clientY - drag.current.y
        if (Math.abs(dx) + Math.abs(dy) > 2) movedRef.current = true
        drag.current = { x: e.clientX, y: e.clientY }
        setView((v) => ({ ...v, tx: v.tx + dx, ty: v.ty + dy }))
      }}
      onMouseUp={() => (drag.current = null)}
      onMouseLeave={() => {
        drag.current = null
        setHover(null)
      }}
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
      {/* Ambient starfield behind the nodes — fixed (does not pan/zoom), a
          gentle twinkle for subtle depth. Decorative, so still under reduced
          motion. */}
      <svg
        viewBox="0 0 100 60"
        preserveAspectRatio="none"
        style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none' }}
        aria-hidden
      >
        {STARS.map((s, i) => (
          <circle key={i} cx={s.x} cy={s.y} r={s.r} fill={theme.palette.text.primary} opacity={s.base}>
            {!reduced && (
              <animate
                attributeName="opacity"
                values={`${s.base};${s.peak};${s.base}`}
                dur={`${s.dur}s`}
                begin={`${s.begin}s`}
                repeatCount="indefinite"
              />
            )}
          </circle>
        ))}
      </svg>
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
          {nodes.map((n) => {
            const on = hover === n.item.id
            return (
              <g
                key={n.item.id}
                style={{ cursor: 'pointer' }}
                onMouseEnter={() => setHover(n.item.id)}
                onMouseLeave={() => setHover((h) => (h === n.item.id ? null : h))}
                onClick={() => {
                  if (!movedRef.current) onOpen(n.item)
                }}
              >
                <circle cx={n.cx} cy={n.cy} r={n.r * 2.2} fill={n.total > 0 ? accent : glowEmpty} fillOpacity={on ? 0.28 : 0.14} />
                <circle
                  cx={n.cx}
                  cy={n.cy}
                  r={n.r}
                  fill={accent}
                  fillOpacity={n.total > 0 ? 0.95 : 0.5}
                  stroke={theme.palette.background.paper}
                  strokeWidth={on ? 2 : 1}
                />
                <text
                  x={n.cx}
                  y={n.cy + n.r + 15}
                  textAnchor="middle"
                  fontSize={12}
                  fontWeight={on ? 700 : 400}
                  fill={theme.palette.text.secondary}
                  style={{ pointerEvents: 'none' }}
                >
                  {truncate(n.item.name, 22)}
                </text>
              </g>
            )
          })}
        </svg>
      </Box>

      <Box sx={{ position: 'absolute', top: 8, left: 8, pointerEvents: 'none' }}>
        <Typography variant="caption" color="text.secondary">
          {t('dashboards.constellationHint')}
        </Typography>
      </Box>

      <Box sx={{ position: 'absolute', top: 8, right: 8, display: 'flex', gap: 0.5, alignItems: 'center' }}>
        <ToolBtn label={t('relation.zoomOut')} icon={faMagnifyingGlassMinus} onClick={() => zoomAt(0.83, cw(), ch())} />
        <Typography variant="caption" sx={{ minWidth: 34, textAlign: 'center', fontVariantNumeric: 'tabular-nums', color: 'text.secondary' }}>
          {Math.round(view.scale * 100)}%
        </Typography>
        <ToolBtn label={t('relation.zoomIn')} icon={faMagnifyingGlassPlus} onClick={() => zoomAt(1.2, cw(), ch())} />
        <ToolBtn label={t('relation.fitToView')} icon={faArrowsToDot} onClick={fit} />
      </Box>
    </Box>
  )
}

// truncate is code-unit-safe enough for Latin project names; user names use the
// grapheme helpers, but a mid-canvas label clip is cosmetic, not an initial.
function truncate(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s
}
