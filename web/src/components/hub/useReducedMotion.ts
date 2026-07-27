import { useEffect, useState } from 'react'

// useReducedMotion tracks the OS "reduce motion" setting so the hub's ambient
// SVG animations (starfield twinkle, river playhead) can switch themselves off.
// Ambient motion is decorative, so honouring this is not optional.
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState<boolean>(
    () => typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches,
  )
  useEffect(() => {
    const mq = window.matchMedia?.('(prefers-reduced-motion: reduce)')
    if (!mq) return
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener?.('change', onChange)
    return () => mq.removeEventListener?.('change', onChange)
  }, [])
  return reduced
}
