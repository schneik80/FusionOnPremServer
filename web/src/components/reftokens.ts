import { parseDocRef, type DocRef } from './doccard/docref'
import { parseImgRef, type ImgRef } from './imgcard/imgref'
import { parseBatchRef, parseJobRef, type BatchRef, type JobRef } from './productioncard/prodref'
import { parseTaskRef, type TaskRef } from './taskcard/taskref'
import { parseWhiteboardRef, type WhiteboardRef } from './whiteboardcard/wbref'

// splitRefTokens splits plain text (chat bodies) into text runs and ref
// tokens of every scheme — fls:doc (document cards), fls:task (task cards),
// fls:job / fls:batch (production cards) and fls:whiteboard (board cards). The
// character class is char-for-char the one in docref.ts: exactly the
// application/x-www-form-urlencoded alphabet URLSearchParams emits, so trailing
// punctuation ("token.", "token)") never sticks to a token. Malformed tokens
// stay text.
const TOKEN_RE = /fls:(?:doc|task|batch|job|whiteboard|img)\?[A-Za-z0-9*\-._%&=+]+/g

export type RefPart =
  | { text: string }
  | { doc: DocRef }
  | { task: TaskRef }
  | { job: JobRef }
  | { batch: BatchRef }
  | { whiteboard: WhiteboardRef }
  | { img: ImgRef }

export function splitRefTokens(text: string): RefPart[] {
  const parts: RefPart[] = []
  let last = 0
  for (const m of text.matchAll(TOKEN_RE)) {
    // Order matters: fls:batch must be tried before fls:job would misparse it
    // (batch tokens carry the job fields too). The regex already
    // distinguishes them, so each parser only accepts its own prefix.
    let part: RefPart | null = null
    const doc = parseDocRef(m[0])
    if (doc) part = { doc }
    if (!part) {
      const task = parseTaskRef(m[0])
      if (task) part = { task }
    }
    if (!part) {
      const batch = parseBatchRef(m[0])
      if (batch) part = { batch }
    }
    if (!part) {
      const job = parseJobRef(m[0])
      if (job) part = { job }
    }
    if (!part) {
      const whiteboard = parseWhiteboardRef(m[0])
      if (whiteboard) part = { whiteboard }
    }
    if (!part) {
      const img = parseImgRef(m[0])
      if (img) part = { img }
    }
    if (!part) continue // malformed token: leave it as text
    if (m.index! > last) parts.push({ text: text.slice(last, m.index) })
    parts.push(part)
    last = m.index! + m[0].length
  }
  if (last < text.length) parts.push({ text: text.slice(last) })
  return parts.length ? parts : [{ text }]
}
