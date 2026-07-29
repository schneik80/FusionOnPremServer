import { useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Item } from '../api/types'
import { useNav } from './nav'

// GoToTarget is the minimum needed to relocate the browser to a document.
export interface GoToTarget {
  itemId: string
  name: string
  kind: string
  componentVersionId?: string
}

// useGoToDocument is the shared cross-document navigation flow: resolve a
// document's location (project + folder path), then move the whole browser there
// and select it, optionally opening a specific Details tab. Used by NavRow, the
// Uses/Where-Used relationship graphs, and anywhere else that jumps to a related
// document — the `tab` option lets a jump preserve the originating tab.
export function useGoToDocument() {
  const nav = useNav()
  const qc = useQueryClient()
  return useCallback(
    async (t: GoToTarget, opts?: { tab?: string }) => {
      if (!nav.hubId || !t.itemId) return
      const loc = await qc.fetchQuery({
        queryKey: ['location', nav.hubId, t.itemId],
        queryFn: () => api.itemLocation(nav.hubId!, t.itemId),
        staleTime: 5 * 60 * 1000,
      })
      const project: Item = {
        id: loc.projectId,
        name: loc.projectName,
        kind: 'project',
        altId: loc.projectAltId,
        isContainer: true,
      }
      const folderStack: Item[] = loc.folderPath.map((f) => ({
        id: f.id,
        name: f.name,
        kind: 'folder',
        isContainer: true,
      }))
      nav.navigate(
        project,
        folderStack,
        { id: t.itemId, name: t.name, kind: t.kind, componentVersionId: t.componentVersionId, isContainer: false },
        opts?.tab,
      )
    },
    [nav, qc],
  )
}

// useGoToWhiteboard is the board equivalent: move the browser to the board's
// project, open the Whiteboards tab, and select it. Used by the fls:whiteboard
// card wherever it sits (chat, wiki, a task's attachments) and by the local
// nodes in a document's Where-Used graph.
//
// Unlike a document there is nothing to resolve first — a board's token
// already carries its project. The project Item is built from the token's
// hints alone; NavProvider's projects-cache backstop fills in altId, exactly
// as it does for a cold-loaded permalink.
export function useGoToWhiteboard() {
  const nav = useNav()
  return useCallback(
    (ref: { projectId: string; projectName: string; boardId: string }) => {
      if (!ref.projectId || !ref.boardId) return
      nav.openProjectApp(
        { id: ref.projectId, name: ref.projectName, kind: 'project', isContainer: true },
        'whiteboards',
        ref.boardId,
      )
    },
    [nav],
  )
}
