import { useEffect, useRef, useSyncExternalStore } from 'react'
import { DefaultMinimap } from 'tldraw'

// Keeping tldraw's minimap honest about where it is on screen.
//
// tldraw's MinimapManager caches the minimap canvas's getBoundingClientRect()
// and refreshes it from a ResizeObserver. That sees SIZE changes only — and
// this app moves the canvas without resizing it: every project tab lives in a
// MUI <Slide> that stays mounted and travels on `transform: translateX`, so a
// board mounted on an inactive tab measures itself while the pane is parked
// off-screen. Slide back to centre and no resize ever happens, so the manager
// keeps the off-screen x forever.
//
// The consequence is the reported bug: the minimap turns clientX into a page
// point with `clientX - cachedX`, so with a stale x every click lands far off,
// and the clamp then pins the camera at one extreme and refuses to pan
// sideways at all.
//
// There is no public API to poke the cached rect, but the manager reads it
// fresh when it is constructed — so the fix is to remount the minimap whenever
// the canvas has actually moved. A remount is one small <canvas>; the manager
// re-measures and everything is correct again.

// A rectangle's on-screen identity, position included. Exported and pure
// because position is the whole point: a size-only comparison is exactly the
// mistake that makes tldraw's own ResizeObserver miss the tab Slide.
export interface RectLike {
  x: number
  y: number
  width: number
  height: number
}

export function sameRect(a: RectLike | null, b: RectLike | null): boolean {
  if (!a || !b) return false
  return a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height
}

let epoch = 0
const listeners = new Set<() => void>()

function bumpMinimap() {
  epoch++
  for (const l of listeners) l()
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

const getEpoch = () => epoch

// RemountingMinimap is the default minimap, re-created whenever the canvas
// moves. Passed via <Tldraw components={{ Minimap }}>; the component identity
// stays stable (module scope) so tldraw never remounts its whole UI for it.
export function RemountingMinimap() {
  const e = useSyncExternalStore(subscribe, getEpoch, getEpoch)
  return <DefaultMinimap key={e} />
}

// useCanvasGeometry watches an element for changes in its on-screen rectangle
// — position as well as size — and reports settled changes.
//
// ResizeObserver alone is not enough (that is the whole bug above), so it is
// paired with the events that accompany a move: window resize and scroll, and
// `transitionend`, which is how the tab Slide announces it has finished
// travelling. Transition events bubble to the window, so one listener catches
// any ancestor's animation.
//
// onChange fires with the current rect whenever it differs from the last one,
// and the minimap is remounted on the same signal.
export function useCanvasGeometry(
  ref: React.RefObject<HTMLElement | null>,
  onChange: (rect: DOMRect) => void,
) {
  // The callback is redefined every render; a ref keeps the listeners from
  // being torn down and rebuilt each time.
  const cb = useRef(onChange)
  cb.current = onChange

  useEffect(() => {
    const el = ref.current
    if (!el) return
    let last: DOMRect | null = null
    let frame = 0

    const check = () => {
      frame = 0
      const el = ref.current
      if (!el) return
      const r = el.getBoundingClientRect()
      if (sameRect(last, r)) return
      last = r
      // A moved canvas means a stale minimap rect; a resized one tldraw would
      // have caught itself, but remounting for it too costs nothing and keeps
      // this to a single rule.
      bumpMinimap()
      cb.current(r)
    }

    // Coalesce a burst (a resize drag, an animation's final frames) into one
    // measurement on the next frame.
    const schedule = () => {
      if (frame) return
      frame = requestAnimationFrame(check)
    }

    const ro = new ResizeObserver(schedule)
    ro.observe(el)
    window.addEventListener('resize', schedule)
    // Capture: a scroll inside any ancestor moves us too, and scroll events
    // from a nested container do not bubble.
    window.addEventListener('scroll', schedule, true)
    window.addEventListener('transitionend', schedule)
    schedule()

    return () => {
      if (frame) cancelAnimationFrame(frame)
      ro.disconnect()
      window.removeEventListener('resize', schedule)
      window.removeEventListener('scroll', schedule, true)
      window.removeEventListener('transitionend', schedule)
    }
  }, [ref])
}
