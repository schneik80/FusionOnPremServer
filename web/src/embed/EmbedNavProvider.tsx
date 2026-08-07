import { useMemo, useState, type ReactNode } from 'react'
import type { Item } from '../api/types'
import { NavContext, type NavCtx, type NavState } from '../state/nav'
import { navToSearch } from '../state/navUrl'
import { openExternal } from './bridge'

// EmbedNavProvider supplies the NavCtx the chat components consume — without
// NavProvider, whose state→URL effect would rewrite location.search and
// destroy the palette's dm-id parameters. The embedded page is pinned to one
// project's chat: only the channel selection is live state here. Every other
// navigation action a reused component can trigger (document cards, pins,
// member links) escapes to the full SPA in the user's browser via
// openExternal, carrying the equivalent permalink.
export function EmbedNavProvider({
  hubId,
  hubName,
  project,
  initialChannelId,
  children,
}: {
  hubId: string
  hubName: string
  project: Item
  initialChannelId: string | null
  children: ReactNode
}) {
  const [channelId, setChannelId] = useState<string | null>(initialChannelId)

  const value = useMemo<NavCtx>(() => {
    const base: NavState = {
      app: 'browser',
      hubId,
      hubName,
      project,
      folderStack: [],
      selected: null,
      selectedTab: null,
      projectTab: 'chat',
      boardId: null,
      channelId,
    }
    const escape = (state: NavState) => {
      const search = navToSearch(state)
      openExternal(`${window.location.origin}/${search ? `?${search}` : ''}`)
    }
    return {
      ...base,
      currentFolderId: null,
      // Channel switching is the one navigation that stays inside the palette.
      selectChannel: (id) => setChannelId(id),
      // Everything else opens the full app at the equivalent place.
      setApp: (app) => escape({ ...base, app, project: null, channelId: null, projectTab: null }),
      selectHub: () => escape({ ...base, project: null, channelId: null, projectTab: null }),
      selectProject: (p) => escape({ ...base, project: p, channelId: null, projectTab: null }),
      enterFolder: (folder) => escape({ ...base, folderStack: [folder], projectTab: null }),
      selectItem: (item) => escape({ ...base, selected: item, projectTab: null }),
      clearProject: () => escape({ ...base, project: null, channelId: null, projectTab: null }),
      gotoProjectRoot: () => escape({ ...base, projectTab: null }),
      gotoFolder: () => escape({ ...base, projectTab: null }),
      navigate: (p, folderStack, selected, tab) =>
        escape({
          ...base,
          project: p,
          folderStack,
          selected,
          selectedTab: tab ?? null,
          projectTab: null,
          channelId: null,
        }),
      openProjectApp: (p, tab, sel) =>
        escape({
          ...base,
          project: p,
          projectTab: tab,
          boardId: sel?.boardId ?? null,
          channelId: sel?.channelId ?? null,
        }),
      setProjectTab: (tab) => escape({ ...base, projectTab: tab }),
      selectBoard: (id) => escape({ ...base, projectTab: 'whiteboards', boardId: id }),
    }
  }, [hubId, hubName, project, channelId])

  return <NavContext.Provider value={value}>{children}</NavContext.Provider>
}
