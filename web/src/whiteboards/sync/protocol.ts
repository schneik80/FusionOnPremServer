// The rules that decide whether two people editing one board converge or
// silently drift apart. Pure on purpose: this is the half most likely to be
// wrong, and the repo's tests are pure-logic with no browser harness, so
// keeping it free of React, fetch and tldraw is what makes it testable at all.

// WirePatch is a tldraw RecordsDiff folded for the wire: `added` and `updated`
// collapse into `put` (applying a diff only ever writes the new value, so the
// `from` half would double the bytes for nothing), `removed` keeps only keys.
export interface WirePatch {
  put: Record<string, unknown>
  remove: string[]
}

// The shape store.listen hands us. Typed loosely on purpose — the records are
// tldraw's business and travel through here untouched.
export interface RecordsDiffLike {
  added: Record<string, unknown>
  updated: Record<string, [unknown, unknown]>
  removed: Record<string, unknown>
}

export interface PatchFrame {
  rev: number
  clientId: string
  put?: Record<string, unknown>
  remove?: string[]
}

export const emptyPatch = (): WirePatch => ({ put: {}, remove: [] })

export const isEmptyPatch = (p: WirePatch): boolean =>
  Object.keys(p.put).length === 0 && p.remove.length === 0

// diffToPatch folds one store change into wire form.
export function diffToPatch(diff: RecordsDiffLike): WirePatch {
  const out = emptyPatch()
  for (const [id, rec] of Object.entries(diff.added ?? {})) out.put[id] = rec
  for (const [id, pair] of Object.entries(diff.updated ?? {})) out.put[id] = pair[1]
  for (const id of Object.keys(diff.removed ?? {})) out.remove.push(id)
  return out
}

// squash collapses a queue of patches into one, in order. Later wins, and a
// record removed after being put (or vice versa) ends in its final state only
// — which is what turns "forty cards each measured themselves on mount" into
// one request rather than forty.
export function squash(patches: WirePatch[]): WirePatch {
  const out = emptyPatch()
  const removed = new Set<string>()
  for (const p of patches) {
    for (const [id, rec] of Object.entries(p.put)) {
      out.put[id] = rec
      removed.delete(id)
    }
    for (const id of p.remove) {
      delete out.put[id]
      removed.add(id)
    }
  }
  out.remove = [...removed]
  return out
}

// What to do with an inbound frame.
//
//  - skip:   already applied, or our own edit coming back to us
//  - apply:  the next revision in sequence
//  - resync: a gap; the document must be re-read
export type FrameAction = 'skip' | 'apply' | 'resync'

// classifyFrame is the whole ordering contract, and each rule exists for a
// failure that is otherwise invisible:
//
//  - Anything at or below the applied revision is old news. Applying rev 55
//    after 58 would resurrect superseded values: patch APPLICATION is
//    idempotent, but out-of-order application is not.
//  - A gap must resync, never apply ahead. Applying rev 60 while holding 57
//    silently drops three revisions of other people's work and leaves this
//    client permanently, undetectably wrong.
//  - Our own echo is skipped, because we applied it locally when the user drew
//    it — re-applying would overwrite whatever they have done since. The gap
//    check comes FIRST: an echo we can see but whose predecessors we missed is
//    still a gap.
export function classifyFrame(
  frame: Pick<PatchFrame, 'rev' | 'clientId'>,
  appliedRev: number,
  myClientId: string,
): FrameAction {
  if (frame.rev <= appliedRev) return 'skip'
  if (frame.rev > appliedRev + 1) return 'resync'
  if (frame.clientId === myClientId) return 'skip'
  return 'apply'
}

// nextRev is the revision a client holds after handling a frame. Note that a
// SKIPPED frame still advances it: our own echo can arrive before the response
// to the request that produced it, and refusing to advance there would make
// the following frame look like a gap and trigger a pointless resync.
export function nextRev(action: FrameAction, frame: Pick<PatchFrame, 'rev'>, appliedRev: number): number {
  if (action === 'resync') return appliedRev
  return Math.max(appliedRev, frame.rev)
}
