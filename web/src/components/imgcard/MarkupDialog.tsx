import {
  faArrowRight,
  faPen,
  faRotateLeft,
  faTrash,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
} from '@mui/material'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

// MarkupDialog lets the user redline a captured screenshot before it uploads:
// the image is the canvas background, annotations are kept as a vector shape
// list (so undo is exact), and Attach composites everything at the image's
// native resolution into a fresh PNG File. Drawing is plain HTML5 canvas —
// no library, matching the repo's hand-drawn-visualizations rule.

type Tool = 'pen' | 'arrow' | 'rect'

interface Shape {
  tool: Tool
  color: string
  // pen: the freehand polyline; arrow/rect: [start, end].
  points: { x: number; y: number }[]
}

const COLORS = ['#ff3b30', '#ffcc00', '#34c759', '#0089ff']

function drawShape(ctx: CanvasRenderingContext2D, s: Shape, lineWidth: number) {
  ctx.strokeStyle = s.color
  ctx.fillStyle = s.color
  ctx.lineWidth = lineWidth
  ctx.lineCap = 'round'
  ctx.lineJoin = 'round'
  const pts = s.points
  if (pts.length < 2) return
  const a = pts[0]
  const b = pts[pts.length - 1]
  if (s.tool === 'pen') {
    ctx.beginPath()
    ctx.moveTo(a.x, a.y)
    for (const p of pts) ctx.lineTo(p.x, p.y)
    ctx.stroke()
  } else if (s.tool === 'rect') {
    ctx.strokeRect(Math.min(a.x, b.x), Math.min(a.y, b.y), Math.abs(b.x - a.x), Math.abs(b.y - a.y))
  } else {
    // Arrow: shaft stops short of the tip so the head stays crisp.
    const angle = Math.atan2(b.y - a.y, b.x - a.x)
    const head = lineWidth * 4
    ctx.beginPath()
    ctx.moveTo(a.x, a.y)
    ctx.lineTo(b.x - head * 0.6 * Math.cos(angle), b.y - head * 0.6 * Math.sin(angle))
    ctx.stroke()
    ctx.beginPath()
    ctx.moveTo(b.x, b.y)
    ctx.lineTo(b.x - head * Math.cos(angle - 0.4), b.y - head * Math.sin(angle - 0.4))
    ctx.lineTo(b.x - head * Math.cos(angle + 0.4), b.y - head * Math.sin(angle + 0.4))
    ctx.closePath()
    ctx.fill()
  }
}

export function MarkupDialog({
  file,
  onClose,
  onAttach,
}: {
  file: File
  onClose: () => void
  onAttach: (file: File) => void
}) {
  const { t } = useTranslation('chat')
  const [tool, setTool] = useState<Tool>('pen')
  const [color, setColor] = useState(COLORS[0])
  const [shapes, setShapes] = useState<Shape[]>([])
  const [img, setImg] = useState<HTMLImageElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const drawingRef = useRef(false)

  // Load the captured PNG once; the object URL lives for the dialog's life.
  useEffect(() => {
    const url = URL.createObjectURL(file)
    const el = new Image()
    el.onload = () => setImg(el)
    el.src = url
    return () => URL.revokeObjectURL(url)
  }, [file])

  // Stroke width in image space, so annotations keep their weight regardless
  // of how far the display canvas is scaled down inside the palette.
  const lineWidth = img ? Math.max(3, Math.round(img.naturalWidth / 250)) : 4

  // Repaint: image, then every committed/in-progress shape.
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || !img) return
    canvas.width = img.naturalWidth
    canvas.height = img.naturalHeight
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.drawImage(img, 0, 0)
    for (const s of shapes) drawShape(ctx, s, lineWidth)
  }, [img, shapes, lineWidth])

  // Pointer position in image coordinates (the canvas is CSS-scaled).
  const toImage = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    return {
      x: ((e.clientX - rect.left) / rect.width) * e.currentTarget.width,
      y: ((e.clientY - rect.top) / rect.height) * e.currentTarget.height,
    }
  }

  const onPointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!img) return
    e.currentTarget.setPointerCapture(e.pointerId)
    drawingRef.current = true
    const p = toImage(e)
    setShapes((s) => [...s, { tool, color, points: [p, p] }])
  }
  const onPointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!drawingRef.current) return
    const p = toImage(e)
    setShapes((s) => {
      const last = s[s.length - 1]
      const points = last.tool === 'pen' ? [...last.points, p] : [last.points[0], p]
      return [...s.slice(0, -1), { ...last, points }]
    })
  }
  const onPointerUp = () => {
    drawingRef.current = false
    // A no-movement click leaves a degenerate shape; drop it.
    setShapes((s) => {
      const last = s[s.length - 1]
      if (!last) return s
      const a = last.points[0]
      const b = last.points[last.points.length - 1]
      return Math.abs(a.x - b.x) + Math.abs(a.y - b.y) < 2 ? s.slice(0, -1) : s
    })
  }

  const attach = () => {
    const canvas = canvasRef.current
    if (!canvas) return
    canvas.toBlob((blob) => {
      if (blob) onAttach(new File([blob], file.name, { type: 'image/png' }))
    }, 'image/png')
  }

  return (
    <Dialog open fullScreen onClose={onClose}>
      <DialogTitle sx={{ py: 1 }}>{t('markup.title')}</DialogTitle>
      <Stack
        direction="row"
        spacing={1}
        alignItems="center"
        flexWrap="wrap"
        useFlexGap
        sx={{ px: 2, pb: 1 }}
      >
        <ToggleButtonGroup
          size="small"
          exclusive
          value={tool}
          onChange={(_, v: Tool | null) => v && setTool(v)}
        >
          <ToggleButton value="pen" aria-label={t('markup.pen')}>
            <Tooltip title={t('markup.pen')}>
              <FontAwesomeIcon icon={faPen} />
            </Tooltip>
          </ToggleButton>
          <ToggleButton value="arrow" aria-label={t('markup.arrow')}>
            <Tooltip title={t('markup.arrow')}>
              <FontAwesomeIcon icon={faArrowRight} />
            </Tooltip>
          </ToggleButton>
          <ToggleButton value="rect" aria-label={t('markup.rect')}>
            <Tooltip title={t('markup.rect')}>
              <Box sx={{ width: 12, height: 10, border: 2, borderRadius: 0.25 }} />
            </Tooltip>
          </ToggleButton>
        </ToggleButtonGroup>
        <Stack direction="row" spacing={0.5}>
          {COLORS.map((c) => (
            <Box
              key={c}
              onClick={() => setColor(c)}
              sx={{
                width: 22,
                height: 22,
                borderRadius: '50%',
                bgcolor: c,
                cursor: 'pointer',
                border: 2,
                borderColor: c === color ? 'text.primary' : 'transparent',
              }}
            />
          ))}
        </Stack>
        <Box sx={{ flex: 1 }} />
        <Tooltip title={t('markup.undo')}>
          <span>
            <IconButton
              size="small"
              disabled={shapes.length === 0}
              onClick={() => setShapes((s) => s.slice(0, -1))}
            >
              <FontAwesomeIcon icon={faRotateLeft} />
            </IconButton>
          </span>
        </Tooltip>
        <Tooltip title={t('markup.clear')}>
          <span>
            <IconButton size="small" disabled={shapes.length === 0} onClick={() => setShapes([])}>
              <FontAwesomeIcon icon={faTrash} />
            </IconButton>
          </span>
        </Tooltip>
      </Stack>
      <DialogContent sx={{ p: 1, display: 'flex', bgcolor: 'action.hover' }}>
        <Box
          component="canvas"
          ref={canvasRef}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          sx={{
            maxWidth: '100%',
            maxHeight: '100%',
            m: 'auto',
            display: 'block',
            border: 1,
            borderColor: 'divider',
            cursor: 'crosshair',
            touchAction: 'none',
          }}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('markup.cancel')}</Button>
        <Button variant="contained" disabled={!img} onClick={attach}>
          {t('markup.attach')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
