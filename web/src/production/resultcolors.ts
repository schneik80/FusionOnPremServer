import type { Theme } from '@mui/material/styles'

// Decision-result colors. The store holds a palette TOKEN, never a hex value,
// for two reasons: a closed enum cannot inject CSS through the sx rule or the
// SVG stroke the value ends up in, and a user-picked hex has no way to stay
// legible in both themes — #ffff00 vanishes on a light background.
//
// Follows the semantic-color idiom in tasks/chips.tsx rather than raw hex, but
// spelled out per mode because these sit on `background.paper` at 11px and on
// a 1.75px SVG stroke, where MUI's palette mains are too light in dark mode.
//
// Keep RESULT_COLORS in sync with production.ResultColors (Go); the server
// rejects anything not in its list.
export const RESULT_COLORS = ['green', 'amber', 'red', 'blue', 'violet', 'teal', 'grey'] as const

export type ResultColor = (typeof RESULT_COLORS)[number]

const LIGHT: Record<ResultColor, string> = {
  green: '#2e7d32',
  amber: '#b26a00',
  red: '#c62828',
  blue: '#0069c0',
  violet: '#6a3fb5',
  teal: '#00796b',
  grey: '#5f6b7a',
}

const DARK: Record<ResultColor, string> = {
  green: '#66bb6a',
  amber: '#ffb74d',
  red: '#ef5350',
  blue: '#4fa3f7',
  violet: '#b085f5',
  teal: '#4db6ac',
  grey: '#9aa5b1',
}

// resultColor resolves a token for the active theme. An unknown token (a file
// written by a newer build, or hand-edited) falls back to grey rather than
// rendering `undefined` into a stroke.
export function resultColor(token: string, theme: Theme): string {
  const table = theme.palette.mode === 'dark' ? DARK : LIGHT
  return table[token as ResultColor] ?? table.grey
}
