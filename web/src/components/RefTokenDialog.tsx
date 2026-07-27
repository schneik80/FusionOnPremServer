import { ProductionViewDialog } from './productioncard/ProductionViewDialog'
import { parseBatchRef, parseJobRef } from './productioncard/prodref'
import { parseTaskRef } from './taskcard/taskref'
import { TaskViewDialog } from '../tasks/TaskViewDialog'

// RefTokenDialog opens an fls: token's record directly, skipping the card.
// RefCard maps a token to its inline card (which opens a dialog when clicked);
// this maps the same token straight to that dialog, for places where the card
// itself isn't the thing being clicked — the Where-Used graph's local nodes,
// where the node IS the card.
//
// Like RefCard it is the single place the mapping lives, so a new token scheme
// only has to be added once. A token with no dialog (fls:doc — a document is
// navigated to, not opened in a dialog) renders nothing.
export function RefTokenDialog({ token, onClose }: { token: string; onClose: () => void }) {
  const task = parseTaskRef(token)
  if (task) return <TaskViewDialog open projectId={task.projectId} taskId={task.taskId} onClose={onClose} />
  const batch = parseBatchRef(token)
  if (batch) return <ProductionViewDialog jobRef={batch} batchRef={batch} onClose={onClose} />
  const job = parseJobRef(token)
  if (job) return <ProductionViewDialog jobRef={job} onClose={onClose} />
  return null
}
