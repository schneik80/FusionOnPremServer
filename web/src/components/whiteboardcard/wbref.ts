import type { Whiteboard } from '../../whiteboards/types'

// Whiteboard refs are the fls:doc sibling (see doccard/docref.ts): the compact,
// text-safe way a chat message, a wiki page or a task body points at one of a
// project's boards. The stored content carries only the pseudo-URL token;
// renderers swap it for a rich WhiteboardCard at display time. In the wiki the
// token travels inside a normal markdown link (degrading to a plain named link
// in any other markdown renderer); in chat it sits inline in the plain-text
// body.
//
// The board's display id (W-3) is deliberately NOT in the token: like a task's
// T-7 it comes from the hydrated board, so a renamed or renumbered board never
// leaves a stale label frozen in a chat log.

export interface WhiteboardRef {
  hubId: string // GraphQL hub id
  projectId: string // project urn (the whiteboard store's key)
  projectName: string // display name at insert time
  boardId: string // per-project board id, "w<num>"
  name: string // display name at insert time (hydration refreshes it)
}

export const WHITEBOARD_REF_PREFIX = 'fls:whiteboard?'

export function encodeWhiteboardRef(ref: WhiteboardRef): string {
  const sp = new URLSearchParams()
  sp.set('hubId', ref.hubId)
  sp.set('projectId', ref.projectId)
  sp.set('projectName', ref.projectName)
  sp.set('boardId', ref.boardId)
  sp.set('name', ref.name)
  return WHITEBOARD_REF_PREFIX + sp.toString()
}

export function parseWhiteboardRef(url: string): WhiteboardRef | null {
  if (!url.startsWith(WHITEBOARD_REF_PREFIX)) return null
  const sp = new URLSearchParams(url.slice(WHITEBOARD_REF_PREFIX.length))
  const projectId = sp.get('projectId') ?? ''
  const boardId = sp.get('boardId') ?? ''
  if (!projectId || !boardId) return null
  return {
    hubId: sp.get('hubId') ?? '',
    projectId,
    projectName: sp.get('projectName') ?? '',
    boardId,
    name: sp.get('name') || 'whiteboard',
  }
}

export function whiteboardRefFromBoard(b: Whiteboard): WhiteboardRef {
  return {
    hubId: b.hubId,
    projectId: b.projectId,
    projectName: b.projectName,
    boardId: b.id,
    name: b.name,
  }
}

// whiteboardRefMarkdown is the wiki-side form: a markdown link whose href is
// the token. Square brackets are stripped from the label only — the token
// itself carries the exact name, percent-encoded.
export function whiteboardRefMarkdown(ref: WhiteboardRef): string {
  return `[${ref.name.replace(/[[\]]/g, '')}](${encodeWhiteboardRef(ref)})`
}
