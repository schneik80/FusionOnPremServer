import { useTheme } from '@mui/material/styles'
import type { CSSProperties } from 'react'
import { GLYPH_BASE } from './icons'
import { artKey, pickIntentArt, type IntentArt } from './intentArt'

// The Fusion design-intent marks — isometric part / hybrid / assembly glyphs,
// transcribed verbatim from the sibling PowerTools add-in's source SVGs
// (PowerTools/lib/ptAddInUtils/assets/intent_icons/<intent>-<size>-<theme>.svg)
// so a design carries the same mark in both products. `<rect>`s in the source
// are written here as their equivalent path; nothing else is changed.
//
// These are the app's second fixed-colour artwork after FusionLogo: unlike
// HubIcon / ProjectIcon they do not paint in currentColor, because the drawing
// is three shaded faces rather than a silhouette. The cost is that a design
// icon does not tint with row selection. intentArt.ts explains the variant
// axes and picks between them.

interface ArtPath {
  d: string
  fill: string
  /** the source path sets fill-rule="evenodd" to punch its holes */
  evenodd?: true
}

const ART: Record<string, ArtPath[]> = {
  'part-16-dark': [
    { d: 'M1 5H11L15 1H5L1 5Z', fill: '#F3F3F3' },
    { d: 'M11 15L15 11V1L11 5V15Z', fill: '#BBBBBB' },
    { d: 'M11 5H1V15H11V5Z', fill: '#D9D9D9' },
  ],
  'part-16-light': [
    { d: 'M1 5H11V15H1V5Z', fill: '#D9D9D9' },
    { d: 'M11 5H1L5 1H15L11 5Z', fill: '#F3F3F3' },
    { d: 'M15 11L11 15V5L15 1V11Z', fill: '#BBBBBB' },
    {
      d: 'M15 1V11L11 15H1V5L5 1H15ZM5.40039 2L2 5.40039V14H10.5996L14 10.5996V2H5.40039Z',
      fill: '#666666',
      evenodd: true,
    },
  ],
  'part-32-dark': [
    { d: 'M2 9H23V30H2V9Z', fill: '#D9D9D9' },
    { d: 'M23 9L30 2V23L23 30V9Z', fill: '#BBBBBB' },
    { d: 'M9 2L30 2L23 9H2L9 2Z', fill: '#F3F3F3' },
  ],
  'part-32-light': [
    { d: 'M2 9H23V30H2V9Z', fill: '#D9D9D9' },
    { d: 'M23 9L30 2V23L23 30V9Z', fill: '#BBBBBB' },
    { d: 'M9 2L30 2L23 9H2L9 2Z', fill: '#F3F3F3' },
    {
      d: 'M30 2V23L23 30H2V9L9 2H30ZM3 9.41421V29H22.5858L29 22.5858V3H9.41421L3 9.41421Z',
      fill: '#666666',
      evenodd: true,
    },
  ],
  'assembly-16-dark': [
    { d: 'M1 9H7V15H1V9Z', fill: '#D9D9D9' },
    { d: 'M8 9H14V15H8V9Z', fill: '#D9D9D9' },
    { d: 'M16 13L14 15V9L16 7V13Z', fill: '#BBBBBB' },
    { d: 'M14 9H8L10 7H16L14 9Z', fill: '#F3F3F3' },
    { d: 'M1 2H7V8H1V2Z', fill: '#D9D9D9' },
    { d: 'M9 6L7 8V2L9 0V6Z', fill: '#BBBBBB' },
    { d: 'M7 2H1L3 0H9L7 2Z', fill: '#F3F3F3' },
  ],
  'assembly-16-light': [
    { d: 'M1 9H13V15H1V9Z', fill: '#D9D9D9' },
    { d: 'M15 13L13 15V9L15 7V13Z', fill: '#BBBBBB' },
    { d: 'M13 9H7L9 7H15L13 9Z', fill: '#F3F3F3' },
    { d: 'M1 3H7V9H1V3Z', fill: '#D9D9D9' },
    { d: 'M9 7L7 9V3L9 1V7Z', fill: '#BBBBBB' },
    { d: 'M7 3H1L3 1H9L7 3Z', fill: '#F3F3F3' },
    {
      d: 'M9 1V7H15V13L13 15H1V3L3 1H9ZM2 14H7V9H2V14ZM8 7L7 8H2V3.41418L3.41418 2H8V7ZM14 12.5858L12.5858 14H8V9L9 8H14V12.5858Z',
      fill: '#666666',
      evenodd: true,
    },
  ],
  'assembly-32-dark': [
    { d: 'M20 18H14V30H20H26V18H20Z', fill: '#D9D9D9' },
    { d: 'M7 18H1V30H7H13V18H7Z', fill: '#D9D9D9' },
    { d: 'M30 26L26 30V18L30 14V26Z', fill: '#BBBBBB' },
    { d: 'M26 18H14L18 14H30L26 18Z', fill: '#F3F3F3' },
    { d: 'M13 5H1V17H13V5Z', fill: '#D9D9D9' },
    { d: 'M17 13L13 17V5L17 1V13Z', fill: '#BBBBBB' },
    { d: 'M13 5H1L5 1H17L13 5Z', fill: '#F3F3F3' },
  ],
  'assembly-32-light': [
    { d: 'M8 18H2V30H8H14V18H8Z', fill: '#D9D9D9' },
    { d: 'M20.5 18H15V30H20.5H26V18H20.5Z', fill: '#D9D9D9' },
    { d: 'M30 26L26 30V18L30 14V26Z', fill: '#BBBBBB' },
    { d: 'M26 18H15L19 14H30L26 18Z', fill: '#F3F3F3' },
    { d: 'M14 6H2V17H14V6Z', fill: '#D9D9D9' },
    { d: 'M18 13L14 17V6L18 2V13Z', fill: '#BBBBBB' },
    { d: 'M14 6H2L6 2H18L14 6Z', fill: '#F3F3F3' },
    {
      d: 'M2 6L6 2H18V14H30V26L26 30H14H3H2V18V6ZM3 18V29H14V18H3ZM14 17L17 14V3H6.41003L3 6.41003V17H14ZM25.59 29L29 25.59V15H18L15 18V29H25.59Z',
      fill: '#666666',
      evenodd: true,
    },
  ],
  // Hybrid is drawn but unreachable — see intentForSubtype in intentArt.ts. It
  // ships so the set is complete the day a root-body signal exists.
  'hybrid-16-dark': [
    { d: 'M16 10L14 12V7L16 5V10Z', fill: '#808080' },
    { d: 'M14 7H9L11 5H16L14 7Z', fill: '#BBBBBB' },
    { d: 'M9 7H14V12H8V8H4V2H9V7Z', fill: '#999999' },
    { d: 'M9 2H4L6 0H11L9 2Z', fill: '#BBBBBB' },
    { d: 'M11 5H12L10 7V12H9V7H4V6H9V2L11 0V5Z', fill: '#808080' },
    { d: 'M0 11H5V16H0V11Z', fill: '#D9D9D9' },
    { d: 'M5 11H0L2 9H7L5 11Z', fill: '#F3F3F3' },
    { d: 'M7 14L5 16V11L7 9V14Z', fill: '#BBBBBB' },
  ],
  'hybrid-16-light': [
    { d: 'M16 10L14 12V7L16 5V10Z', fill: '#999999' },
    { d: 'M14 7H9L11 5H16L14 7Z', fill: '#D9D9D9' },
    { d: 'M14 12H4V2H9V7H14V12Z', fill: '#BBBBBB' },
    { d: 'M9 2H4L6 0H11L9 2Z', fill: '#D9D9D9' },
    { d: 'M11 5H12L10 7V12H9V7H4V6H9V2L11 0V5Z', fill: '#999999' },
    {
      d: 'M11 5H16V10L14 12H4V2L6 0H11V5ZM5 2.41406V11H13.5859L15 9.58594V6H10V1H6.41406L5 2.41406Z',
      fill: '#666666',
      evenodd: true,
    },
    { d: 'M0 10H6V16H0V10Z', fill: '#D9D9D9' },
    { d: 'M6 10H0L2 8H8L6 10Z', fill: '#F3F3F3' },
    { d: 'M8 14L6 16V10L8 8V14Z', fill: '#BBBBBB' },
    { d: 'M8 8V14L6 16H0V10L2 8H8ZM1 10.4141V15H5.58594L7 13.5859V9H2.41406L1 10.4141Z', fill: '#666666' },
  ],
  'hybrid-32-dark': [
    { d: 'M32 20L28 24V14L32 10V20Z', fill: '#808080' },
    { d: 'M28 14H19L23 10H32L28 14Z', fill: '#BBBBBB' },
    { d: 'M18 4V14H28V24H16V16H8V4H18Z', fill: '#999999' },
    { d: 'M17.9998 4H7.99976L11.9998 0H21.9998L17.9998 4Z', fill: '#BBBBBB' },
    { d: 'M22 10H23L19 14V24H18V14H8V13H18V4L22 0V10Z', fill: '#808080' },
    { d: 'M0 20H12V32H0V20Z', fill: '#D9D9D9' },
    { d: 'M12 20L15 17V29L12 32V20Z', fill: '#BBBBBB' },
    { d: 'M3 17H15L12 20H0L3 17Z', fill: '#F3F3F3' },
  ],
  'hybrid-32-light': [
    { d: 'M13 14H8V24H13H18V14H13Z', fill: '#BBBBBB' },
    { d: 'M23.5 14H19V24H23.5H28V14H23.5Z', fill: '#BBBBBB' },
    { d: 'M32 20L28 24V14L32 10V20Z', fill: '#999999' },
    { d: 'M28 14H19L23 10H32L28 14Z', fill: '#D9D9D9' },
    { d: 'M18 4H8V13H18V4Z', fill: '#BBBBBB' },
    { d: 'M18 4H8L12 0H22L18 4Z', fill: '#D9D9D9' },
    { d: 'M22 10H23L19 14V24H18V14H8V13H18V4L22 0V10Z', fill: '#999999' },
    {
      d: 'M22 10H32V20L28 24H8V4L12 0H22V10ZM9 4.41016V23H27.5898L31 19.5898V11H21V1H12.4102L9 4.41016Z',
      fill: '#666666',
      evenodd: true,
    },
    { d: 'M0 20H12V32H0V20Z', fill: '#D9D9D9' },
    { d: 'M12 20L16 16V28L12 32V20Z', fill: '#BBBBBB' },
    { d: 'M4 16H16L12 20H0L4 16Z', fill: '#F3F3F3' },
    {
      d: 'M16 16V28L12 32H0V20L4 16H16ZM1 20.4142V31H11.5858L15 27.5858V17H4.41421L1 20.4142Z',
      fill: '#666666',
      evenodd: true,
    },
  ],
}

interface GlyphProps {
  style?: CSSProperties
  className?: string
}

// IntentArtwork draws one named variant. Sized at 1em and baseline-nudged like
// every other glyph, so it drops in wherever a <FontAwesomeIcon> sat.
export function IntentArtwork({ art, style, className }: GlyphProps & { art: IntentArt }) {
  const paths = ART[artKey(art)]
  if (!paths) return null
  return (
    <svg
      viewBox={`0 0 ${art.size} ${art.size}`}
      width="1em"
      height="1em"
      fill="none"
      className={className}
      style={{ ...GLYPH_BASE, ...style }}
      aria-hidden
    >
      {paths.map((p, i) => (
        <path
          key={i}
          d={p.d}
          fill={p.fill}
          fillRule={p.evenodd ? 'evenodd' : undefined}
          clipRule={p.evenodd ? 'evenodd' : undefined}
        />
      ))}
    </svg>
  )
}

// DesignIntentIcon is the glyph for a Fusion 3D design, keyed on the classify
// subtype. Callers size it the way they size an FA glyph — style={{fontSize:N}}
// — and that same number picks the 16 px or 32 px drawing, so no size prop has
// to be threaded through the dozen places an item icon is rendered.
export function DesignIntentIcon({ subtype, style, className }: GlyphProps & { subtype?: string }) {
  const mode = useTheme().palette.mode === 'light' ? 'light' : 'dark'
  const px = typeof style?.fontSize === 'number' ? style.fontSize : 16
  return (
    <IntentArtwork art={pickIntentArt({ subtype, mode, px })} style={style} className={className} />
  )
}
