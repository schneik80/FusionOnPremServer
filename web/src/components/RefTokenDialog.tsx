import { useCallback } from 'react'
import { parseChannelRef } from './chatcard/chanref'
import { ProductionViewDialog } from './productioncard/ProductionViewDialog'
import { parseBatchRef, parseJobRef } from './productioncard/prodref'
import { parseTaskRef } from './taskcard/taskref'
import { parseWhiteboardRef } from './whiteboardcard/wbref'
import { useGoToChannel, useGoToWhiteboard } from '../state/goto'
import { TaskViewDialog } from '../tasks/TaskViewDialog'

// RefTokenDialog opens an fls: token's record directly, skipping the card.
// RefCard maps a token to its inline card (which opens a dialog when clicked);
// this maps the same token straight to that dialog, for places where the card
// itself isn't the thing being clicked — the Where-Used graph's local nodes,
// where the node IS the card.
//
// Like RefCard it is the single place the mapping lives, so a new token scheme
// only has to be added once. Not every scheme has a dialog: a document
// (fls:doc), a whiteboard (fls:whiteboard) and a chat channel (fls:channel)
// are NAVIGATED to, and those render nothing here — see useRefTokenNavigate
// below, which a host must try first.
export function RefTokenDialog({ token, onClose }: { token: string; onClose: () => void }) {
  const task = parseTaskRef(token)
  if (task) return <TaskViewDialog open projectId={task.projectId} taskId={task.taskId} onClose={onClose} />
  const batch = parseBatchRef(token)
  if (batch) return <ProductionViewDialog jobRef={batch} batchRef={batch} onClose={onClose} />
  const job = parseJobRef(token)
  if (job) return <ProductionViewDialog jobRef={job} onClose={onClose} />
  return null
}

// useRefTokenNavigate is RefTokenDialog's other half: the schemes that take the
// user somewhere instead of raising a dialog. It returns true when it handled
// the token, so a host reads as
//
//   if (navigateToken(token)) return
//   setOpenToken(token)
//
// Keeping it beside RefTokenDialog is the point — a host must not have to know
// which schemes navigate and which open, and adding a scheme still means
// editing one module.
export function useRefTokenNavigate(): (token: string) => boolean {
  const goToBoard = useGoToWhiteboard()
  const goToChannel = useGoToChannel()
  return useCallback(
    (token: string) => {
      const whiteboard = parseWhiteboardRef(token)
      if (whiteboard) {
        goToBoard(whiteboard)
        return true
      }
      const channel = parseChannelRef(token)
      if (channel) {
        goToChannel(channel)
        return true
      }
      return false
    },
    [goToBoard, goToChannel],
  )
}
