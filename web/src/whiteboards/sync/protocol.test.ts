import { describe, expect, it } from 'vitest'
import { classifyFrame, diffToPatch, nextRev, squash, type WirePatch } from './protocol'

const patch = (put: Record<string, unknown> = {}, remove: string[] = []): WirePatch => ({ put, remove })

describe('diffToPatch', () => {
  it('folds added and updated into put, keeping only the new value', () => {
    const out = diffToPatch({
      added: { 'shape:a': { v: 1 } },
      updated: { 'shape:b': [{ v: 1 }, { v: 2 }] },
      removed: { 'shape:c': { v: 9 } },
    })
    expect(out.put).toEqual({ 'shape:a': { v: 1 }, 'shape:b': { v: 2 } })
    expect(out.remove).toEqual(['shape:c'])
  })
})

describe('squash', () => {
  // The card-measure case: many small writes for the same records collapse to
  // one request rather than one per card.
  it('keeps the last value per record', () => {
    const out = squash([patch({ 'shape:a': { v: 1 } }), patch({ 'shape:a': { v: 2 } })])
    expect(out.put).toEqual({ 'shape:a': { v: 2 } })
  })

  it('a later remove wins over an earlier put', () => {
    const out = squash([patch({ 'shape:a': { v: 1 } }), patch({}, ['shape:a'])])
    expect(out.put).toEqual({})
    expect(out.remove).toEqual(['shape:a'])
  })

  it('a later put wins over an earlier remove', () => {
    const out = squash([patch({}, ['shape:a']), patch({ 'shape:a': { v: 3 } })])
    expect(out.put).toEqual({ 'shape:a': { v: 3 } })
    expect(out.remove).toEqual([])
  })
})

describe('classifyFrame', () => {
  const mine = 'c-me'

  it('skips frames at or below the applied revision', () => {
    // Applying an older revision after a newer one resurrects superseded
    // values — application is idempotent, ordering is not.
    expect(classifyFrame({ rev: 5, clientId: 'other' }, 7, mine)).toBe('skip')
    expect(classifyFrame({ rev: 7, clientId: 'other' }, 7, mine)).toBe('skip')
  })

  it('applies the next revision in sequence', () => {
    expect(classifyFrame({ rev: 8, clientId: 'other' }, 7, mine)).toBe('apply')
  })

  it('resyncs on a gap rather than applying ahead', () => {
    // Applying 10 while holding 7 would silently drop three revisions of
    // someone else's work and leave this client undetectably wrong.
    expect(classifyFrame({ rev: 10, clientId: 'other' }, 7, mine)).toBe('resync')
  })

  it('skips our own echo, which we already applied locally', () => {
    expect(classifyFrame({ rev: 8, clientId: mine }, 7, mine)).toBe('skip')
  })

  it('treats a gap as a gap even when the frame is ours', () => {
    expect(classifyFrame({ rev: 12, clientId: mine }, 7, mine)).toBe('resync')
  })
})

describe('nextRev', () => {
  // A skipped echo must still advance the cursor: it can arrive before the
  // response to the request that produced it, and standing still there would
  // make the following frame look like a gap.
  it('advances on a skipped echo', () => {
    expect(nextRev('skip', { rev: 8 }, 7)).toBe(8)
  })

  it('advances on apply', () => {
    expect(nextRev('apply', { rev: 8 }, 7)).toBe(8)
  })

  it('never moves backwards for an old frame', () => {
    expect(nextRev('skip', { rev: 3 }, 7)).toBe(7)
  })

  it('holds still on resync, until the document has been re-read', () => {
    expect(nextRev('resync', { rev: 20 }, 7)).toBe(7)
  })
})
