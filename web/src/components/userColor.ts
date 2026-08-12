// A person's identity colour, derived from their id.
//
// This is the app's ONE documented exception to the "colours come from the
// theme" rule. Everywhere else a literal colour is a bug, because Settings →
// Appearance can retheme the accents per hub and a hardcoded hue would not
// follow. Here the whole point is the opposite: the same author must read as
// the same colour in every row of the History view, in every session, whatever
// the hub's accent is — so the colour has to be a function of *who*, not of the
// palette. Deriving it from the accent instead would collapse to near-identical
// hues once more than three or four people appear.
//
// Deterministic and pure — no Math.random, no Date — so a re-render never
// reshuffles the avatars.
//
// Text drawn on top of these must go through theme.palette.getContrastText():
// HSL lightness is not perceptual, so white-on-yellow at L=48% fails contrast
// while white-on-blue at the same lightness is fine.

// hashKey is DJB2 — small, fast, and well spread for short strings.
export function hashKey(s: string): number {
  let h = 5381
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0
  return h >>> 0
}

// Saturation and lightness are fixed: only the hue carries identity, so every
// avatar has the same visual weight and none dominates the row.
const SAT = 68
const LIGHT = 48

// userHue maps a stable user key (APS user id, falling back to display name)
// onto the colour wheel. Two users can land a few degrees apart; the initials
// and the avatar tooltip are what disambiguate them.
export function userHue(key: string): number {
  return hashKey(key) % 360
}

export function userColor(key: string): string {
  return `hsl(${userHue(key)}, ${SAT}%, ${LIGHT}%)`
}
