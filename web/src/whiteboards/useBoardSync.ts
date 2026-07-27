import { useCallback, useEffect, useRef, useState } from 'react'
import type { TLRecord, TLStore } from 'tldraw'
import { loadSnapshot } from 'tldraw'
import { ApiError, api } from '../api/client'
import {
  classifyFrame,
  diffToPatch,
  emptyPatch,
  isEmptyPatch,
  nextRev,
  squash,
  type PatchFrame,
  type WirePatch,
} from './sync/protocol'

// The impure half of live editing: timers, fetches and the tldraw store. Every
// rule that decides whether two clients converge lives in sync/protocol.ts,
// which has no browser in it and is tested directly.
//
// Outbound: the store's own change listener, filtered to document edits the
// USER made (source 'user'), debounced and squashed into one request.
// Inbound: patch frames from the board's stream, applied inside
// mergeRemoteChanges so they are marked remote — which is what stops the
// listener above from sending them straight back out.

// Long enough to collapse a drag or a burst of card measurements into one
// request, short enough that a peer sees your stroke as it happens.
const PATCH_DEBOUNCE_MS = 100
// A hard ceiling, so continuous drawing still streams rather than accumulating.
const PATCH_MAX_WAIT_MS = 400

export type SyncStatus = 'connecting' | 'live' | 'offline' | 'error'

export interface BoardSync {
  status: SyncStatus
  pending: boolean
  // clientId identifies this tab. Exposed so the events hook can hand frames
  // back with enough context to be classified.
  clientId: string
}

// newClientId identifies one open tab, not one user: the same person with two
// windows must be able to tell their own echoes apart from each other's.
function newClientId(): string {
  return 'c' + Math.random().toString(36).slice(2, 10)
}

export function useBoardSync(args: {
  store: TLStore
  projectId: string
  boardId: string
  canWrite: boolean
  // ready gates everything until the initial document has been loaded: patches
  // sent before that would be built against an empty store.
  ready: boolean
  // revision the document was loaded at.
  initialRev: number
}) {
  const { store, projectId, boardId, canWrite, ready, initialRev } = args
  const [status, setStatus] = useState<SyncStatus>('connecting')
  const [pending, setPending] = useState(false)

  const clientId = useRef(newClientId()).current
  const appliedRev = useRef(initialRev)
  const seq = useRef(0)
  const queue = useRef<WirePatch[]>([])
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const firstQueuedAt = useRef(0)
  const inFlight = useRef(false)
  // Suppresses the outbound listener while remote changes are being applied.
  // mergeRemoteChanges already marks them 'remote', so this is belt and braces
  // for anything the editor does in reaction to them.
  const applying = useRef(false)

  useEffect(() => {
    appliedRev.current = initialRev
  }, [initialRev, boardId])

  // ---- resync: re-read the document and start again from its revision ----
  const resync = useCallback(async () => {
    try {
      const { doc, rev } = await api.whiteboardDoc(projectId, boardId)
      applying.current = true
      try {
        loadSnapshot(store, { document: (doc ?? { store: {}, schema: {} }) as never })
      } finally {
        applying.current = false
      }
      appliedRev.current = rev
      queue.current = [] // built against a document that no longer exists
      setStatus('live')
    } catch {
      setStatus('error')
    }
  }, [projectId, boardId, store])

  // ---- outbound ----
  const send = useCallback(async () => {
    timer.current = null
    if (inFlight.current || queue.current.length === 0) return
    const patch = squash(queue.current)
    if (isEmptyPatch(patch)) {
      queue.current = []
      return
    }
    // Held, not dropped: a failed send must be retried, not lost, because the
    // editor is the only place this work exists.
    const sending = queue.current
    queue.current = []
    inFlight.current = true
    setPending(true)
    seq.current += 1
    try {
      const res = await api.whiteboardPatch(projectId, boardId, {
        clientId,
        seq: seq.current,
        baseRev: appliedRev.current,
        put: patch.put,
        remove: patch.remove,
      })
      appliedRev.current = Math.max(appliedRev.current, res.rev)
      setStatus('live')
      // Records the server refused — someone deleted them while we were
      // editing. Drop them locally so both sides agree the shape is gone.
      if (res.rejected.length > 0) {
        applying.current = true
        try {
          store.mergeRemoteChanges(() => {
            store.remove(res.rejected as never[])
          })
        } finally {
          applying.current = false
        }
      }
    } catch (e) {
      if (e instanceof ApiError && e.code === 'whiteboard_resync') {
        await resync()
      } else {
        // Put the work back at the front and let the next change (or the
        // reconnect) carry it.
        queue.current = [...sending, ...queue.current]
        setStatus('offline')
      }
    } finally {
      inFlight.current = false
      setPending(queue.current.length > 0)
      if (queue.current.length > 0 && !timer.current) {
        timer.current = setTimeout(() => void send(), PATCH_DEBOUNCE_MS)
      }
    }
  }, [clientId, projectId, boardId, store, resync])

  useEffect(() => {
    if (!ready || !canWrite) return
    const unlisten = store.listen(
      (entry) => {
        if (applying.current) return
        const patch = diffToPatch(entry.changes as never)
        if (isEmptyPatch(patch)) return
        queue.current.push(patch)
        setPending(true)
        const now = Date.now()
        if (queue.current.length === 1) firstQueuedAt.current = now
        // Debounce, but never wait longer than the ceiling: a continuous drag
        // would otherwise keep pushing the deadline out and send nothing.
        if (now - firstQueuedAt.current >= PATCH_MAX_WAIT_MS) {
          if (timer.current) clearTimeout(timer.current)
          timer.current = null
          void send()
          return
        }
        if (timer.current) clearTimeout(timer.current)
        timer.current = setTimeout(() => void send(), PATCH_DEBOUNCE_MS)
      },
      // Only the user's own document edits: 'session' is camera and selection
      // (per-user view state), and remote changes are marked 'remote' by
      // mergeRemoteChanges precisely so they are not echoed back.
      { scope: 'document', source: 'user' },
    )
    return () => {
      unlisten()
      if (timer.current) clearTimeout(timer.current)
      timer.current = null
    }
  }, [store, ready, canWrite, send])

  // Flush on unmount so the last strokes inside the debounce window are not
  // lost when a board is closed.
  useEffect(() => {
    return () => {
      if (queue.current.length > 0) void send()
    }
  }, [send])

  // ---- inbound ----
  const onFrame = useCallback(
    (frame: PatchFrame) => {
      const action = classifyFrame(frame, appliedRev.current, clientId)
      if (action === 'resync') {
        void resync()
        return
      }
      if (action === 'apply') {
        const puts = Object.values(frame.put ?? {}) as TLRecord[]
        const removes = (frame.remove ?? []) as never[]
        applying.current = true
        try {
          store.mergeRemoteChanges(() => {
            if (puts.length > 0) store.put(puts)
            if (removes.length > 0) store.remove(removes)
          })
        } catch {
          // A record this build cannot validate (a peer on a newer version):
          // re-read rather than sit on a half-applied document.
          void resync()
          return
        } finally {
          applying.current = false
        }
      }
      appliedRev.current = nextRev(action, frame, appliedRev.current)
      setStatus('live')
    },
    [clientId, store, resync],
  )

  // Called by the events hook when the stream opens or drops.
  const setLive = useCallback((live: boolean) => {
    setStatus((prev) => (live ? 'live' : prev === 'error' ? prev : 'offline'))
  }, [])

  // A whole-document write landed (an import, or an overwrite): not expressible
  // as a patch, so the only correct response is to re-read.
  const onReplaced = useCallback(
    (rev: number) => {
      if (rev <= appliedRev.current) return
      void resync()
    },
    [resync],
  )

  const sync: BoardSync = { status, pending, clientId }
  return { sync, onFrame, setLive, onReplaced, resync }
}

// applyEmpty is exported for symmetry with the pure module's emptyPatch, so
// callers never construct a patch shape by hand.
export { emptyPatch }
