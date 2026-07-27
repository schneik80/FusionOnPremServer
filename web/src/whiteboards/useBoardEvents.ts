import { useEffect, useRef, useState } from 'react'

// One board's awareness stream. Two things arrive on it:
//
//  - peers: who else has this board open, republished whenever someone joins
//    or leaves. Ephemeral server-side, so a reconnect never replays a stale
//    roster claiming someone is here who left.
//  - patch: a peer's edit, carrying the board's new revision and the id of the
//    client that made it (so a client can ignore its own echo).
//  - doc.changed: a whole document was written — an import, or an acknowledged
//    overwrite. It cannot be expressed as a patch, so the canvas re-reads.
//
// Recovery is EventSource's own: it reconnects and re-sends Last-Event-ID, and
// the server replays what its ring holds or sends a named `reset`. A reset
// needs no special handling here — a board's truth is its document, which the
// canvas re-reads when it acts on a change.

export interface BoardPeer {
  userId: string
  name: string
  color: string
  canWrite: boolean
}

interface BoardEvent {
  type: string
  v: number
  data?: {
    peers?: BoardPeer[]
    rev?: number
    by?: BoardPeer
    clientId?: string
    put?: Record<string, unknown>
    remove?: string[]
  }
}

export function useBoardEvents(
  projectId: string | null,
  boardId: string | null,
  handlers: {
    // A peer's edit, to be ordered and applied (see sync/protocol.ts).
    onPatch: (frame: { rev: number; clientId: string; put?: Record<string, unknown>; remove?: string[] }) => void
    // A whole-document write — an import or an overwrite. Not expressible as a
    // patch, so the canvas re-reads.
    onReplaced: (rev: number, by?: BoardPeer) => void
    onLive: (live: boolean) => void
  },
): { peers: BoardPeer[] } {
  const [peers, setPeers] = useState<BoardPeer[]>([])
  // The callbacks change identity on every canvas render; holding them in a ref
  // keeps the stream from being torn down and re-established each time.
  const cb = useRef(handlers)
  cb.current = handlers

  useEffect(() => {
    if (!projectId || !boardId) return
    const url = `/api/whiteboards/events?projectId=${encodeURIComponent(projectId)}&boardId=${encodeURIComponent(boardId)}`
    const es = new EventSource(url)

    es.onopen = () => cb.current.onLive(true)
    es.onerror = () => cb.current.onLive(false)
    es.onmessage = (e) => {
      try {
        const ev = JSON.parse(e.data) as BoardEvent
        if (ev.type === 'peers') {
          setPeers(ev.data?.peers ?? [])
        } else if (ev.type === 'patch' && typeof ev.data?.rev === 'number') {
          cb.current.onPatch({
            rev: ev.data.rev,
            clientId: ev.data.clientId ?? '',
            put: ev.data.put,
            remove: ev.data.remove,
          })
        } else if (ev.type === 'doc.changed' && typeof ev.data?.rev === 'number') {
          cb.current.onReplaced(ev.data.rev, ev.data.by)
        }
      } catch {
        /* one malformed frame must not kill the stream */
      }
    }

    return () => {
      es.close()
      cb.current.onLive(false)
      setPeers([])
    }
  }, [projectId, boardId])

  return { peers }
}
