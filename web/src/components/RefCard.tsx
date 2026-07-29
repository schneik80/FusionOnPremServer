import { DocumentCard } from './doccard/DocumentCard'
import { parseDocRef } from './doccard/docref'
import { ProductionCard } from './productioncard/ProductionCard'
import { parseBatchRef, parseJobRef } from './productioncard/prodref'
import { TaskCard } from './taskcard/TaskCard'
import { parseTaskRef } from './taskcard/taskref'
import { WhiteboardCard } from './whiteboardcard/WhiteboardCard'
import { parseWhiteboardRef } from './whiteboardcard/wbref'

// RefCard renders a single fls: token as the matching rich card — the one
// place that maps every ref scheme (doc, task, job, batch, whiteboard) to its
// renderer, so any list of stored tokens (a task's docRefs, a batch's refs)
// unfurls the same way. Returns null for an unrecognized token.
export function RefCard({ token }: { token: string }) {
  const doc = parseDocRef(token)
  if (doc) return <DocumentCard docRef={doc} />
  const task = parseTaskRef(token)
  if (task) return <TaskCard taskRef={task} />
  const batch = parseBatchRef(token)
  if (batch) return <ProductionCard jobRef={batch} batchRef={batch} />
  const job = parseJobRef(token)
  if (job) return <ProductionCard jobRef={job} />
  const whiteboard = parseWhiteboardRef(token)
  if (whiteboard) return <WhiteboardCard whiteboardRef={whiteboard} />
  return null
}
