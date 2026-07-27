import { describe, expect, it } from 'vitest'
import { sameRect } from './canvasGeometry'

const rect = (x: number, y: number, width = 800, height = 600) => ({ x, y, width, height })

describe('sameRect', () => {
  // The bug this whole module exists for: the tab Slide moves the canvas with
  // a transform, changing x while width and height stay put. A comparison that
  // only looked at size would call that "unchanged" — which is precisely why
  // tldraw's ResizeObserver misses it and the minimap keeps a stale origin.
  it('treats a move as a change even when the size is identical', () => {
    expect(sameRect(rect(0, 0), rect(-1200, 0))).toBe(false)
    expect(sameRect(rect(0, 0), rect(0, 48))).toBe(false)
  })

  it('treats a resize as a change', () => {
    expect(sameRect(rect(0, 0, 800, 600), rect(0, 0, 640, 600))).toBe(false)
    expect(sameRect(rect(0, 0, 800, 600), rect(0, 0, 800, 480))).toBe(false)
  })

  it('reports an unchanged rectangle', () => {
    expect(sameRect(rect(12, 34), rect(12, 34))).toBe(true)
  })

  // The first measurement has nothing to compare against, and must count as a
  // change so the minimap is anchored and the initial fit can run.
  it('treats a missing rectangle as a change', () => {
    expect(sameRect(null, rect(0, 0))).toBe(false)
    expect(sameRect(rect(0, 0), null)).toBe(false)
  })
})
