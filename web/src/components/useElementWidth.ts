import { useEffect, useState } from 'react'

// useElementWidth reports an element's content width and keeps it current
// through a ResizeObserver — for layouts that must respond to the space they
// were given rather than to the viewport.
//
// That distinction is the reason this exists. A viewport media query is wrong
// for anything living inside a resizable region: the chat pane is narrow in the
// Fusion palette AND in a squeezed desktop project panel, and wide in both when
// the user drags them out. Measuring the container makes the host irrelevant.
//
// Returns 0 until the first measurement. Callers should treat 0 as "not known
// yet" and fall back to their roomier layout, so a narrow layout never flashes
// for a frame on a wide screen.
//
// The callback ref (rather than a RefObject) is the same shape useInView uses:
// it fires when the node actually mounts, which matters when the measured
// element is rendered conditionally.
export function useElementWidth<T extends Element>(): [(node: T | null) => void, number] {
  const [node, setNode] = useState<T | null>(null)
  const [width, setWidth] = useState(0)

  useEffect(() => {
    if (!node) return
    if (typeof ResizeObserver === 'undefined') {
      // No observer (jsdom, very old browsers): take one static reading rather
      // than leaving every consumer pinned at 0 forever.
      setWidth(node.getBoundingClientRect().width)
      return
    }
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect?.width ?? 0
      // Sub-pixel churn from a parent's own layout would otherwise re-render
      // the subtree on every frame of a window drag.
      setWidth((prev) => (Math.abs(prev - w) > 0.5 ? w : prev))
    })
    ro.observe(node)
    return () => ro.disconnect()
  }, [node])

  return [setNode, width]
}
