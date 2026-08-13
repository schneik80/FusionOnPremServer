// The Fusion add-in's deep link. FusionProjectChat's "Open Web App" command
// launches https://<server>/?dmHubId=…&dmProjectId=…: the Data Management ids
// it can read from the Fusion API (app.data.activeHub.id,
// dataFile.parentProject.id — the DTOs' altId space). Only the server maps
// those to the GraphQL ids this app is keyed by (GET /api/resolve/project),
// so DmDeepLinkGate resolves them at load and hands the URL back here to be
// rewritten as an ordinary ?hub=…&proj=… permalink. Nothing downstream ever
// sees a DM id.

import type { Item } from '../api/types'
import type { NavState } from './nav'
import { navToSearch } from './navUrl'

export interface DmDeepLink {
  dmHubId: string
  dmProjectId: string
  // Display hint only, so the gate can name the project while the resolve
  // call is in flight. May be empty; ids drive correctness.
  projectName: string
}

// readDmDeepLink returns the link in a URL search string, or null when the
// URL is an ordinary one. Both ids are required — one alone resolves nothing.
export function readDmDeepLink(search: string): DmDeepLink | null {
  const p = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const dmHubId = p.get('dmHubId') ?? ''
  const dmProjectId = p.get('dmProjectId') ?? ''
  if (!dmHubId || !dmProjectId) return null
  return { dmHubId, dmProjectId, projectName: p.get('projectName') ?? '' }
}

// readDmDeepLinkHere is the browser-side wrapper, for a lazy useState
// initializer (read once — the gate rewrites the URL when it is done).
export function readDmDeepLinkHere(): DmDeepLink | null {
  if (typeof window === 'undefined') return null
  return readDmDeepLink(window.location.search)
}

// ResolvedForPermalink is the slice of ResolvedProject the permalink needs.
export interface ResolvedForPermalink {
  hubId: string
  hubName: string
  projectId: string
  projectName: string
  projectAltId: string
}

// resolvedPermalink renders a resolved project as the URL the rest of the app
// speaks — byte-for-byte what NavProvider would serialize for the same place,
// so replacing the deep link with it leaves no history or re-push artifacts.
export function resolvedPermalink(r: ResolvedForPermalink): string {
  const project: Item = {
    id: r.projectId,
    name: r.projectName,
    kind: 'project',
    altId: r.projectAltId,
    isContainer: true,
  }
  const state: NavState = {
    app: 'browser',
    hubId: r.hubId,
    hubName: r.hubName,
    project,
    folderStack: [],
    selected: null,
    selectedTab: null,
    projectTab: null,
    boardId: null,
    channelId: null,
  }
  const search = navToSearch(state)
  return search ? `/?${search}` : '/'
}
