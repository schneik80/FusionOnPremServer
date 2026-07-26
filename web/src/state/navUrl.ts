// Permalink serialization: the browser's navigable state <-> the URL search
// string. Query-param based (the path always stays "/"), so URN ids ride
// encoded in values and the Go server's "/" SPA catch-all needs no change.
// See docs/permalinks/PLAN.md.
//
// Field layout (all values encodeURIComponent'd, "~"-joined — URNs never
// contain "~"):
//   app=tasks|production              (cross-project screen; carries hub= only)
//   hub=<hubId>~<hubName>
//   proj=<projectId>~<projectName>
//   f=<folderId>~<folderName>         (repeated, in drill order)
//   sel=<itemId>~<name>~<kind>        (selected document)
//   dtab=<detailsTab>                 (only meaningful with sel)
// The ~name/kind suffixes are display hints so the breadcrumb/label paint on a
// cold load without a fetch; ids drive correctness (names may be stale until
// the real item loads).

import type { Item } from '../api/types'
import type { AppKind, NavState } from './nav'

const SEP = '~'

function enc(...parts: (string | undefined)[]): string {
  return parts.map((p) => encodeURIComponent(p ?? '')).join(SEP)
}

function dec(s: string): string[] {
  return s.split(SEP).map((p) => {
    try {
      return decodeURIComponent(p)
    } catch {
      return p
    }
  })
}

// navToSearch renders nav state as a URL search string (no leading "?").
export function navToSearch(s: NavState): string {
  const p = new URLSearchParams()
  if (s.app === 'tasks' || s.app === 'production') {
    p.set('app', s.app)
    // The cross-project screens are hub-scoped like everything else (the
    // session is locked to one hub), so the hub context stays in the URL —
    // a permalink reopens in the same hub. Browser sub-state (project/folder/
    // selection) is browser-only and stays out.
    if (s.hubId) p.set('hub', enc(s.hubId, s.hubName ?? ''))
    return p.toString()
  }
  if (s.hubId) p.set('hub', enc(s.hubId, s.hubName ?? ''))
  if (s.project) p.set('proj', enc(s.project.id, s.project.name))
  for (const f of s.folderStack) p.append('f', enc(f.id, f.name))
  if (s.selected) {
    p.set('sel', enc(s.selected.id, s.selected.name, String(s.selected.kind)))
    if (s.selectedTab) p.set('dtab', s.selectedTab)
  }
  return p.toString()
}

// ParsedNav is the synchronous, hint-only reconstruction of nav state from a
// URL — enough to paint immediately. project.altId and any stale names are
// reconciled afterward by the itemLocation / projects-cache backstops in
// NavProvider.
export interface ParsedNav {
  app: AppKind
  hubId: string | null
  hubName: string | null
  project: Item | null
  folderStack: Item[]
  selected: Item | null
  selectedTab: string | null
}

// searchToNav parses a URL search string (with or without leading "?") into
// nav state. Absent params mean "not set".
export function searchToNav(search: string): ParsedNav {
  const p = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)

  const appRaw = p.get('app')
  const app: AppKind = appRaw === 'tasks' || appRaw === 'production' ? appRaw : 'browser'

  const hubRaw = p.get('hub')
  const [hubId, hubName] = hubRaw ? dec(hubRaw) : [undefined, undefined]

  const projRaw = p.get('proj')
  let project: Item | null = null
  if (projRaw) {
    const [id, name] = dec(projRaw)
    if (id) project = { id, name: name ?? '', kind: 'project', isContainer: true }
  }

  const folderStack: Item[] = p
    .getAll('f')
    .map((raw) => {
      const [id, name] = dec(raw)
      return { id, name: name ?? '', kind: 'folder', isContainer: true } as Item
    })
    .filter((f) => !!f.id)

  const selRaw = p.get('sel')
  let selected: Item | null = null
  if (selRaw) {
    const [id, name, kind] = dec(selRaw)
    if (id) selected = { id, name: name ?? '', kind: kind ?? 'unknown', isContainer: false }
  }

  return {
    app,
    hubId: hubId || null,
    hubName: hubName || null,
    project,
    folderStack,
    selected,
    selectedTab: (selected && p.get('dtab')) || null,
  }
}

// shouldPush decides history.pushState (new back-stack entry) vs replaceState.
// Push when the *location* changes (app / hub / project / folder depth / the
// selected document); replace for in-place refinements (tab switches, a name
// hint corrected by a backstop).
export function shouldPush(prev: NavState, next: NavState): boolean {
  if (prev.app !== next.app) return true
  if (prev.hubId !== next.hubId) return true
  if ((prev.project?.id ?? null) !== (next.project?.id ?? null)) return true
  if (prev.folderStack.length !== next.folderStack.length) return true
  if (prev.folderStack.some((f, i) => f.id !== next.folderStack[i]?.id)) return true
  if ((prev.selected?.id ?? null) !== (next.selected?.id ?? null)) return true
  return false
}
