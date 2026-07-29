import { useCallback, useMemo } from 'react'
import { usePinMutations, usePins } from '../api/queries'
import type { Item } from '../api/types'
import { useNav } from './nav'

// APS item kinds that may be pinned — mirrors pins.IsPinnable in the Go
// backend (hubs and unknown items are excluded). The LOCAL record kinds
// (whiteboard, task, job, batch, channel) are pinnable too but never arrive
// as an Item, so they are deliberately not in this set: isPinnable answers
// "may this browser row show a star", and useLocalPin below is their path.
const PINNABLE = new Set([
  'project',
  'folder',
  'design',
  'drawing',
  'configured',
  'schematic',
  'pcb',
  'ecad',
])

export function isPinnable(kind: string): boolean {
  return PINNABLE.has(kind)
}

// LocalPinTarget is everything needed to bookmark a record owned by one of
// the per-project stores. The ref is an fls: token — the same one the inline
// cards use — and doubles as the pin's id, so nothing else has to agree on an
// identity scheme (see pins.Validate, which enforces id === ref).
export interface LocalPinTarget {
  ref: string
  kind: 'whiteboard' | 'task' | 'job' | 'batch' | 'channel'
  name: string
  projectId: string
  projectName?: string
}

// usePinToggle exposes the current hub's pinned-id set plus a toggle that
// captures project + folder context at pin time, exactly like the TUI's
// togglePin: a project pins itself; a folder/document pins under the selected
// project, and a folder also appends itself to the folder_path so navigation
// can drill straight into it.
export function usePinToggle() {
  const nav = useNav()
  const pinsQ = usePins(nav.hubId)
  const { add, remove } = usePinMutations(nav.hubId)

  const pinnedIds = useMemo(
    () => new Set((pinsQ.data ?? []).map((p) => p.id)),
    [pinsQ.data],
  )

  const toggle = useCallback(
    (item: Item) => {
      if (!nav.hubId || !isPinnable(item.kind)) return
      if (pinnedIds.has(item.id)) {
        remove.mutate(item.id)
        return
      }
      const isProject = item.kind === 'project'
      const folderPath = isProject
        ? []
        : nav.folderStack.map((f) => ({ id: f.id, name: f.name }))
      if (item.kind === 'folder') {
        folderPath.push({ id: item.id, name: item.name })
      }
      add.mutate({
        id: item.id,
        name: item.name,
        kind: item.kind,
        project_id: isProject ? item.id : nav.project?.id,
        project_alt_id: isProject ? item.altId : nav.project?.altId,
        folder_path: folderPath,
      })
    },
    [nav.hubId, nav.project, nav.folderStack, pinnedIds, add, remove],
  )

  return { pinnedIds, toggle }
}

// useLocalPin is usePinToggle's counterpart for local records. It cannot share
// the same entry point: a board or a batch is not an Item, has no folder
// stack, and — unlike a browser row, which is always inside the project the
// user has open — may be shown from anywhere (a task card's dialog references
// a task in another project), so its project must come from the record itself
// rather than from nav.
export function useLocalPin() {
  const nav = useNav()
  const pinsQ = usePins(nav.hubId)
  const { add, remove } = usePinMutations(nav.hubId)

  const pinnedRefs = useMemo(
    () => new Set((pinsQ.data ?? []).map((p) => p.id)),
    [pinsQ.data],
  )

  const isPinned = useCallback((ref: string) => pinnedRefs.has(ref), [pinnedRefs])

  const toggle = useCallback(
    (target: LocalPinTarget) => {
      if (!nav.hubId || !target.ref || !target.projectId) return
      if (pinnedRefs.has(target.ref)) {
        remove.mutate(target.ref)
        return
      }
      add.mutate({
        id: target.ref,
        ref: target.ref,
        kind: target.kind,
        name: target.name,
        project_id: target.projectId,
        project_name: target.projectName,
      })
    },
    [nav.hubId, pinnedRefs, add, remove],
  )

  return { isPinned, toggle }
}
