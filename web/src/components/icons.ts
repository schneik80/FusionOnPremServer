import type { IconDefinition } from '@fortawesome/fontawesome-svg-core'
import {
  faBuilding,
  faChalkboard,
  faComments,
  faCube,
  faCubes,
  faDiagramProject,
  faFile,
  faFlask,
  faFolder,
  faGears,
  faListCheck,
  faMicrochip,
  faPenRuler,
} from '@fortawesome/free-solid-svg-icons'
import type { Item } from '../api/types'

// iconForItem picks a glyph from kind + (for designs) the async-refined
// subtype. An unclassified design falls back to the generic cube until its
// classify query resolves to assembly (cubes) or part (cube).
export function iconForItem(item: Pick<Item, 'kind' | 'subtype'>): IconDefinition {
  switch (item.kind) {
    case 'hub':
      return faBuilding
    case 'project':
      return faDiagramProject
    case 'folder':
      return faFolder
    case 'design':
      return item.subtype === 'assembly' ? faCubes : faCube
    case 'configured':
      return faGears
    case 'drawing':
      return faPenRuler
    case 'schematic':
    case 'pcb':
    case 'ecad':
      return faMicrochip
    // Local records, not hub documents: the Where-Used graph draws them beside
    // parent designs, so they need glyphs too. Each matches the icon its own
    // app and card already use.
    case 'task':
      return faListCheck
    case 'chat':
    case 'channel':
      return faComments
    case 'whiteboard':
      return faChalkboard
    case 'job':
      return faDiagramProject
    case 'batch':
      return faFlask
    default:
      return faFile
  }
}

// typeTag returns the short inline tag shown after a row's name (rendered as
// "· asm" etc. by the row component), or "" when the row needs no tag.
export function typeTag(item: Pick<Item, 'kind' | 'subtype'>): string {
  switch (item.kind) {
    case 'design':
      if (item.subtype === 'assembly') return 'asm'
      if (item.subtype === 'part') return 'part'
      return 'design'
    case 'configured':
      return 'cfg'
    case 'drawing':
      return item.subtype === 'template' ? 'tmpl' : 'dwg'
    case 'schematic':
      return 'sch'
    case 'pcb':
      return 'pcb'
    case 'ecad':
      return 'ecad'
    default:
      return ''
  }
}
