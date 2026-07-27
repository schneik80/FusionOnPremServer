import { useEffect, useRef, useState } from 'react'

// One board's awareness stream. Two things arrive on it:
//
//  - peers: who else has this board open, republished whenever someone joins
//    or leaves. Ephemeral server-side, so a reconnect never replays a stale
//    roster claiming someone is here who left.
//  - doc.changed: someone saved. onRemoteSave fires with the new revision, so
//    the canvas can say so immediately rather than discovering it when its own
//    next save is refused.
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
  data?: { peers?: BoardPeer[]; rev?: number; by?: BoardPeer }
}

export function useBoardEvents(
  projectId: string | null,
  boardId: string | null,
  onRemoteSave: (rev: number, by?: BoardPeer) => void,
): { peers: BoardPeer[]; live: boolean } {
  const [peers, setPeers] = useState<BoardPeer[]>([])
  const [live, setLive] = useState(false)
  // The callback changes identity on every canvas render; holding it in a ref
  // keeps the stream from being torn down and re-established each time.
  const onSave = useRef(onRemoteSave)
  onSave.current = onRemoteSave

  useEffect(() => {
    if (!projectId || !boardId) return
    const url = `/api/whiteboards/events?projectId=${encodeURIComponent(projectId)}&boardId=${encodeURIComponent(boardId)}`
    const es = new EventSource(url)

    es.onopen = () => setLive(true)
    es.onerror = () => setLive(false)
    es.onmessage = (e) => {
      try {
        const ev = JSON.parse(e.data) as BoardEvent
        if (ev.type === 'peers') setPeers(ev.data?.peers ?? [])
        else if (ev.type === 'doc.changed' && typeof ev.data?.rev === 'number') {
          onSave.current(ev.data.rev, ev.data.by)
        }
      } catch {
        /* one malformed frame must not kill the stream */
      }
    }

    return () => {
      es.close()
      setLive(false)
      setPeers([])
    }
  }, [projectId, boardId])

  return { peers, live }
}
