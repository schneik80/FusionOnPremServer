import { describe, expect, it } from 'vitest'
import { encodeDocRef } from './doccard/docref'
import { encodeBatchRef, encodeJobRef } from './productioncard/prodref'
import { splitRefTokens, type RefPart } from './reftokens'
import { encodeTaskRef } from './taskcard/taskref'
import { encodeWhiteboardRef } from './whiteboardcard/wbref'

// splitRefTokens is the chat path's whole token grammar: every card a message
// unfurls comes through here, and it has to survive text people actually
// write. The load-bearing rules are the parse ORDER (a batch token carries the
// job fields, so fls:job must not claim it) and the character class (a token
// ending a sentence must not swallow the full stop).

const doc = encodeDocRef({ hubId: 'h1', itemId: 'urn:adsk.wipprod:dm.lineage:AbC-123', name: 'Bracket', kind: 'design' })
const task = encodeTaskRef({ hubId: 'h1', projectId: 'p1', taskId: 't3', title: 'Fix the jig' })
const job = encodeJobRef({ hubId: 'h1', projectId: 'p1', projectName: 'Alpha', jobId: 'j1', jobName: 'Run A' })
const batch = encodeBatchRef({
  hubId: 'h1',
  projectId: 'p1',
  projectName: 'Alpha',
  jobId: 'j1',
  jobName: 'Run A',
  batchId: 'b2',
  batchName: 'Batch 2',
})
const board = encodeWhiteboardRef({
  hubId: 'h1',
  projectId: 'p1',
  projectName: 'Alpha',
  boardId: 'w4',
  name: 'Layout sketch',
})

// Which arm of the RefPart union a part landed in — the discriminator ChatBody
// switches on.
function kindOf(p: RefPart): string {
  return Object.keys(p)[0]
}

describe('splitRefTokens', () => {
  it('round-trips every scheme back to its own arm of the union', () => {
    const cases: [string, string][] = [
      [doc, 'doc'],
      [task, 'task'],
      [job, 'job'],
      [batch, 'batch'],
      [board, 'whiteboard'],
    ]
    for (const [token, kind] of cases) {
      const parts = splitRefTokens(token)
      expect(parts).toHaveLength(1)
      expect(kindOf(parts[0])).toBe(kind)
    }
  })

  it('keeps a batch token out of the job arm', () => {
    // A batch token carries every job field, so a job-first parse would match
    // it and silently render the wrong card.
    const parts = splitRefTokens(batch)
    expect(kindOf(parts[0])).toBe('batch')
  })

  it('preserves the surrounding text and its order', () => {
    const parts = splitRefTokens(`see ${board} and ${task} today`)
    expect(parts.map(kindOf)).toEqual(['text', 'whiteboard', 'text', 'task', 'text'])
    expect(parts[0]).toEqual({ text: 'see ' })
    expect(parts[2]).toEqual({ text: ' and ' })
    expect(parts[4]).toEqual({ text: ' today' })
  })

  it('does not swallow trailing punctuation', () => {
    for (const trailer of [')', ',', '!', '?', ' ']) {
      const parts = splitRefTokens(`look at ${board}${trailer}`)
      expect(parts.map(kindOf)).toEqual(['text', 'whiteboard', 'text'])
      expect(parts[2]).toEqual({ text: trailer })
    }
  })

  it('absorbs a trailing full stop into the last value', () => {
    // Pinning a known trade-off rather than an aspiration: '.' is in the token
    // character class because URLSearchParams leaves it unescaped, so a name
    // with a dot in it round-trips — at the cost of a sentence-ending period
    // landing inside the final parameter. It affects every scheme equally
    // (fls:doc, fls:task, …), and the card still resolves because the ids come
    // earlier in the query string.
    const parts = splitRefTokens(`look at ${board}.`)
    expect(parts.map(kindOf)).toEqual(['text', 'whiteboard'])
    const part = parts[1]
    expect('whiteboard' in part && part.whiteboard.boardId).toBe('w4')
    expect('whiteboard' in part && part.whiteboard.name).toBe('Layout sketch.')
  })

  it('leaves a malformed token as plain text', () => {
    // No boardId: parseWhiteboardRef returns null, and a half-token must never
    // render as a card claiming to be a board.
    const bad = 'fls:whiteboard?hubId=h1&projectId=p1&name=Nope'
    expect(splitRefTokens(bad)).toEqual([{ text: bad }])
  })

  it('leaves an unknown scheme as plain text', () => {
    const bad = 'fls:sketch?projectId=p1&id=s1'
    expect(splitRefTokens(bad)).toEqual([{ text: bad }])
  })

  it('returns a single text part for a body with no tokens', () => {
    expect(splitRefTokens('just a message')).toEqual([{ text: 'just a message' }])
  })

  it('round-trips names that need percent-encoding', () => {
    const token = encodeWhiteboardRef({
      hubId: 'h1',
      projectId: 'p1',
      projectName: 'Alpha & Co',
      boardId: 'w9',
      name: 'Räder / Zeichnung #2',
    })
    const parts = splitRefTokens(`board: ${token} ok`)
    expect(parts.map(kindOf)).toEqual(['text', 'whiteboard', 'text'])
    const part = parts[1]
    expect('whiteboard' in part && part.whiteboard.name).toBe('Räder / Zeichnung #2')
    expect('whiteboard' in part && part.whiteboard.projectName).toBe('Alpha & Co')
  })
})
