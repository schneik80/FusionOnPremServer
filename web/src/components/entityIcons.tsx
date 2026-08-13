import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import type { CSSProperties } from 'react'
import type { Item } from '../api/types'
import { GLYPH_BASE, iconForItem } from './icons'
import { DesignIntentIcon } from './intentIcons'

// Custom brand glyphs for the two top-level entities, hub and project. They
// stand in for FontAwesome's faBuilding / faDiagramProject, and are drawn to be
// drop-in replacements for `<FontAwesomeIcon>`: sized at 1em (so a parent's
// fontSize controls them) and painted in currentColor (so they inherit `color`
// exactly as an FA glyph does). A Fusion 3D design gets its own artwork too —
// the design-intent marks in intentIcons.tsx, which are fixed-colour rather
// than currentColor. Every other kind still comes from iconForItem, via the
// ItemIcon router below.

// GLYPH_BASE is the baseline nudge that lines a hand-drawn glyph up with an FA
// one. It lives in ./icons — the one icon module with no JSX — so intentIcons
// can share it without the two files importing each other.

interface GlyphProps {
  style?: CSSProperties
  className?: string
}

// ProjectIcon: three nodes in a triangle joined by edges — a small graph, the
// shape a "project" (a graph of documents) suggests. Hand-drawn per the
// inline-SVG convention (CLAUDE.md); edges first, nodes painted over them.
export function ProjectIcon({ style, className }: GlyphProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      width="1em"
      height="1em"
      fill="none"
      className={className}
      style={{ ...GLYPH_BASE, ...style }}
      aria-hidden
    >
      <g stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
        <line x1="12" y1="5" x2="5.5" y2="17.5" />
        <line x1="12" y1="5" x2="18.5" y2="17.5" />
        <line x1="5.5" y1="17.5" x2="18.5" y2="17.5" />
      </g>
      <g fill="currentColor">
        <circle cx="12" cy="5" r="2.7" />
        <circle cx="5.5" cy="17.5" r="2.7" />
        <circle cx="18.5" cy="17.5" r="2.7" />
      </g>
    </svg>
  )
}

// HubIcon: the supplied network/molecule mark. The path relies on evenodd to
// punch the ring holes — which is exactly why it can't be a FontAwesome custom
// icon (FA renders paths nonzero, filling the rings solid). The source's hard
// #808080 is swapped for currentColor so it themes like every other icon.
export function HubIcon({ style, className }: GlyphProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      width="1em"
      height="1em"
      fill="none"
      className={className}
      style={{ ...GLYPH_BASE, ...style }}
      aria-hidden
    >
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        fill="currentColor"
        d="M13 3.5C13 4.32843 12.3284 5 11.5 5C10.6716 5 10 4.32843 10 3.5C10 2.67157 10.6716 2 11.5 2C12.3284 2 13 2.67157 13 3.5ZM14 3.5C14 4.88071 12.8807 6 11.5 6C10.1193 6 9 4.88071 9 3.5C9 2.11929 10.1193 1 11.5 1C12.8807 1 14 2.11929 14 3.5ZM15 12.5C15 14.433 13.433 16 11.5 16C9.567 16 8 14.433 8 12.5C8 10.567 9.567 9 11.5 9C13.433 9 15 10.567 15 12.5ZM16 12.5C16 14.9853 13.9853 17 11.5 17C9.01472 17 7 14.9853 7 12.5C7 11.8318 7.14564 11.1976 7.40691 10.6274L5.21948 9.31498C5.46264 9.08451 5.65951 8.8057 5.79471 8.49393L7.92212 9.77037C8.64912 8.8189 9.74704 8.16597 11 8.02746V5.9502C11.1616 5.98299 11.3288 6 11.5 6C11.6712 6 11.8384 5.98299 12 5.9502V8.02746C13.253 8.16597 14.3509 8.8189 15.0779 9.77037L17.2053 8.49393C17.3405 8.8057 17.5374 9.08451 17.7805 9.31498L15.5931 10.6274C15.8544 11.1976 16 11.8318 16 12.5ZM19.5 9C20.3284 9 21 8.32843 21 7.5C21 6.67157 20.3284 6 19.5 6C18.6716 6 18 6.67157 18 7.5C18 8.32843 18.6716 9 19.5 9ZM19.5 10C20.8807 10 22 8.88071 22 7.5C22 6.11929 20.8807 5 19.5 5C18.1193 5 17 6.11929 17 7.5C17 8.88071 18.1193 10 19.5 10ZM21.5 17C21.5 17.8284 20.8284 18.5 20 18.5C19.1716 18.5 18.5 17.8284 18.5 17C18.5 16.1716 19.1716 15.5 20 15.5C20.8284 15.5 21.5 16.1716 21.5 17ZM22.5 17C22.5 18.3807 21.3807 19.5 20 19.5C18.6193 19.5 17.5 18.3807 17.5 17C17.5 16.8965 17.5063 16.7945 17.5185 16.6944L15.0778 15.23C15.2797 14.9658 15.453 14.6785 15.593 14.3729L17.8484 15.7262C18.284 14.9921 19.0845 14.5 20 14.5C21.3807 14.5 22.5 15.6193 22.5 17ZM11.5 22C12.3284 22 13 21.3284 13 20.5C13 19.6716 12.3284 19 11.5 19C10.6716 19 10 19.6716 10 20.5C10 21.3284 10.6716 22 11.5 22ZM11.5 23C12.8807 23 14 21.8807 14 20.5C14 19.2905 13.1411 18.2816 12 18.05V16.9727C11.8358 16.9909 11.669 17 11.5 17C11.331 17 11.1642 16.9909 11 16.9727V18.05C9.85888 18.2816 9 19.2905 9 20.5C9 21.8807 10.1193 23 11.5 23ZM5 17C5 17.8284 4.32843 18.5 3.5 18.5C2.67157 18.5 2 17.8284 2 17C2 16.1716 2.67157 15.5 3.5 15.5C4.32843 15.5 5 16.1716 5 17ZM6 17C6 18.3807 4.88071 19.5 3.5 19.5C2.11929 19.5 1 18.3807 1 17C1 15.6193 2.11929 14.5 3.5 14.5C4.32316 14.5 5.05341 14.8978 5.50894 15.5117L7.40698 14.3729C7.54704 14.6785 7.72033 14.9658 7.92221 15.23L5.93318 16.4234C5.97688 16.6085 6 16.8015 6 17ZM3.5 9C4.32843 9 5 8.32843 5 7.5C5 6.67157 4.32843 6 3.5 6C2.67157 6 2 6.67157 2 7.5C2 8.32843 2.67157 9 3.5 9ZM3.5 10C4.88071 10 6 8.88071 6 7.5C6 6.11929 4.88071 5 3.5 5C2.11929 5 1 6.11929 1 7.5C1 8.88071 2.11929 10 3.5 10Z"
      />
    </svg>
  )
}

// ItemIcon is the single funnel for a row/entity glyph: the brand marks for hub
// and project, the Fusion design-intent mark for a design, and iconForItem's
// FontAwesome glyph for everything else. Swapped in wherever a
// `<FontAwesomeIcon icon={iconForItem(item)}>` used to render, so the new marks
// appear everywhere an item's kind is shown.
export function ItemIcon({
  item,
  style,
  className,
}: {
  item: Pick<Item, 'kind' | 'subtype'>
  style?: CSSProperties
  className?: string
}) {
  if (item.kind === 'hub') return <HubIcon style={style} className={className} />
  if (item.kind === 'project') return <ProjectIcon style={style} className={className} />
  if (item.kind === 'design')
    return <DesignIntentIcon subtype={item.subtype} style={style} className={className} />
  return <FontAwesomeIcon icon={iconForItem(item)} style={style} className={className} />
}
