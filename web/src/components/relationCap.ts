// thumbSlots decides which nodes of a relation graph get a thumbnail. Each
// one is an APS image request, and fit-to-view puts every node on screen at
// once, so a viewport gate cannot bound them — a cap does. The focus always
// gets one; relations get one in placement order (the first row is the
// structural parents nearest the focus) up to cap, or all of them once the
// user asks for the rest. Pure, so the rule is unit-tested.
export const THUMB_CAP = 24

export function thumbSlots(n: number, cap: number, loadAll: boolean): (i: number) => boolean {
  if (loadAll || n <= cap) return () => true
  return (i) => i < cap
}

// thumbsHidden is how many relations the cap leaves without a thumbnail.
export function thumbsHidden(n: number, cap: number, loadAll: boolean): number {
  if (loadAll || n <= cap) return 0
  return n - cap
}
