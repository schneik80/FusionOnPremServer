// Fusion design-intent artwork: the choice of which drawing to render.
//
// Fusion classifies a design by its *intent* — part (bodies only), hybrid
// (bodies and components) or assembly (components only) — and the sibling
// PowerTools add-in draws one isometric mark per intent. We reuse that set so
// the same design reads the same in both products; the source SVGs and the
// intent vocabulary live in PowerTools/lib/ptAddInUtils/intent_icons.py.
//
// The set varies along two axes an ordinary glyph does not:
//   * theme — these are fixed-grey marks, not currentColor glyphs, so the light
//     drawing carries a #666 outline the dark one omits;
//   * size  — 16 px and 32 px are separate drawings, not one scaled.
//
// This module is the pure choice between those variants; intentIcons.tsx holds
// the drawings themselves. They are split because the web test runner is
// node-only (no jsdom, no RTL), so logic worth testing has to sit outside a
// component — the same split as components/history/historyLayout.ts.

export type DesignIntent = 'part' | 'hybrid' | 'assembly'
export type ArtSize = 16 | 32
export type ArtTheme = 'light' | 'dark'

export interface IntentArt {
  intent: DesignIntent
  size: ArtSize
  theme: ArtTheme
}

// Rendered px at or above which the 32 px drawing is used. List rows ask for
// 11–16 and the EntityCard thumbnail tile for 24, so the break sits between.
export const INTENT_ART_BREAK = 20

// intentForSubtype maps an Item's classify subtype onto an intent.
//
// 'hybrid' is deliberately unreachable. Fusion's real three-way intent is a
// live-document property; over MDM GraphQL all we have is api/classify.go's
// occurrences>0 probe, which answers assembly-or-not with no signal for
// root-level bodies. An unclassified design (subtype "") draws the part mark,
// matching the faCube fallback this artwork replaces.
export function intentForSubtype(subtype?: string): DesignIntent {
  return subtype === 'assembly' ? 'assembly' : 'part'
}

export function pickIntentArt({
  subtype,
  mode,
  px,
}: {
  subtype?: string
  mode: ArtTheme
  /** the glyph's rendered size in px — callers size these with style.fontSize */
  px: number
}): IntentArt {
  return {
    intent: intentForSubtype(subtype),
    size: px >= INTENT_ART_BREAK ? 32 : 16,
    theme: mode,
  }
}

// artKey names one drawing, matching the source filenames
// (<intent>-<size>-<theme>.svg) so a glyph can be traced back to its SVG.
export function artKey(art: IntentArt): string {
  return `${art.intent}-${art.size}-${art.theme}`
}
