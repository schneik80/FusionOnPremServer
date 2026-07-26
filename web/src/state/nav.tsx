import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  type ReactNode,
} from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Item } from '../api/types'
import { saveLastHub } from './hubKeys'
import { navToSearch, searchToNav, shouldPush } from './navUrl'

// The session is locked to ONE hub server-side (POST /api/session/hub); nav's
// hubId always mirrors that lock. The remembered last hub lives in
// state/hubKeys.ts (fls.lastHub) — it feeds the HubGate auto-relock and the
// per-hub client-setting keys, and selectHub keeps it in sync below.

// The rail's top-level view: the document browser, or one of the cross-project
// screens (Tasks, Production). All stay mounted (AppLayout display-toggles
// them) so switching apps never loses browser drill-down or list state.
export type AppKind = 'browser' | 'tasks' | 'production'

// Navigation state for the three-column browser. The hub mirrors the session's
// hub lock (seeded by AppLayout); project lives in the Projects column; folderStack is the
// drill-down path inside the Contents column (mirrored by the breadcrumb);
// selected is the document whose Details panel is shown.
export interface NavState {
  app: AppKind
  hubId: string | null
  hubName: string | null
  project: Item | null
  folderStack: Item[]
  selected: Item | null
  // The Details tab to open on the next selection (consumed at mount). Set by
  // navigate() so a cross-document jump can land on the same tab it came from;
  // cleared by plain selection so ordinary clicks default to History.
  selectedTab: string | null
}

const initialState: NavState = {
  app: 'browser',
  hubId: null,
  hubName: null,
  project: null,
  folderStack: [],
  selected: null,
  selectedTab: null,
}

type Action =
  | { type: 'hydrate'; state: NavState }
  | { type: 'patchProject'; project: Item }
  | { type: 'setApp'; app: AppKind }
  | { type: 'selectHub'; id: string; name: string }
  | { type: 'selectProject'; project: Item }
  | { type: 'enterFolder'; folder: Item }
  | { type: 'selectItem'; item: Item }
  | { type: 'clearProject' }
  | { type: 'gotoProjectRoot' }
  | { type: 'gotoFolder'; index: number }
  | {
      type: 'navigate'
      project: Item
      folderStack: Item[]
      selected: Item | null
      tab?: string
    }

function reducer(state: NavState, action: Action): NavState {
  switch (action.type) {
    case 'hydrate':
      // Atomic replace from a parsed URL (cold load / back-forward). One action
      // so no intermediate reset (selectHub-style) fires mid-hydration.
      return action.state
    case 'patchProject':
      // Swap in the fully-resolved project object (adds altId) without
      // disturbing the folder stack or selection — a backstop for a permalink
      // whose project came from name hints only.
      if (!state.project || state.project.id !== action.project.id) return state
      return { ...state, project: action.project }
    case 'setApp':
      return state.app === action.app ? state : { ...state, app: action.app }
    case 'selectHub':
      if (action.id === state.hubId) return state
      // Selecting a hub resets everything below it. The app is kept so a deep
      // link like ?app=tasks&hub=… still lands on the Tasks screen once the
      // lock-seed effect (AppLayout) aligns nav with the session hub. The
      // cross-project screens are hub-scoped now (the server scopes /mine to
      // the session hub) — an actual hub switch is a full reload via
      // Settings → Connection, never an in-place selectHub.
      return { ...initialState, app: state.app, hubId: action.id, hubName: action.name }
    case 'selectProject':
      return { ...state, project: action.project, folderStack: [], selected: null, selectedTab: null }
    case 'enterFolder':
      return {
        ...state,
        folderStack: [...state.folderStack, action.folder],
        selected: null,
        selectedTab: null,
      }
    case 'selectItem':
      return { ...state, selected: action.item, selectedTab: null }
    case 'clearProject':
      // Back to the hub level (projects list); keep the hub, drop everything below it.
      return { ...state, project: null, folderStack: [], selected: null, selectedTab: null }
    case 'gotoProjectRoot':
      return { ...state, folderStack: [], selected: null, selectedTab: null }
    case 'gotoFolder':
      return {
        ...state,
        folderStack: state.folderStack.slice(0, action.index + 1),
        selected: null,
        selectedTab: null,
      }
    case 'navigate':
      // Cross-document jumps (document cards, where-used, …) land in the
      // browser regardless of which app issued them.
      return {
        ...state,
        app: 'browser',
        project: action.project,
        folderStack: action.folderStack,
        selected: action.selected,
        selectedTab: action.tab ?? null,
      }
    default:
      return state
  }
}

interface NavCtx extends NavState {
  setApp: (app: AppKind) => void
  selectHub: (id: string, name: string) => void
  selectProject: (project: Item) => void
  enterFolder: (folder: Item) => void
  selectItem: (item: Item) => void
  clearProject: () => void
  gotoProjectRoot: () => void
  gotoFolder: (index: number) => void
  navigate: (project: Item, folderStack: Item[], selected: Item | null, tab?: string) => void
  /** id of the folder whose contents the Contents column currently shows, or null at project root */
  currentFolderId: string | null
}

const Ctx = createContext<NavCtx | null>(null)

// initFromUrl seeds nav state from the current URL so a cold load / shared
// permalink paints the right place immediately (from the encoded name hints).
// Backstops in NavProvider then reconcile ids → full items (altId, real names).
function initFromUrl(): NavState {
  if (typeof window === 'undefined') return initialState
  const p = searchToNav(window.location.search)
  return {
    app: p.app,
    hubId: p.hubId,
    hubName: p.hubName,
    project: p.project,
    folderStack: p.folderStack,
    selected: p.selected,
    selectedTab: p.selectedTab,
  }
}

export function NavProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, undefined, initFromUrl)
  const qc = useQueryClient()
  const prevRef = useRef(state)

  // state → URL. Push a new history entry when the location changes; replace on
  // in-place refinements (tab, a backstop-corrected name). Comparing against the
  // live URL makes hydration/popstate self-cancel: the search we'd write already
  // matches, so nothing is pushed.
  useEffect(() => {
    const search = navToSearch(state)
    const current = window.location.search.replace(/^\?/, '')
    if (search !== current) {
      const url = search ? `?${search}` : window.location.pathname
      if (shouldPush(prevRef.current, state)) window.history.pushState(null, '', url)
      else window.history.replaceState(null, '', url)
    }
    prevRef.current = state
  }, [state])

  // URL → state on back/forward. The browser already moved history, so sync
  // prevRef first to keep the serialize effect from re-pushing.
  useEffect(() => {
    const onPop = () => {
      const next = initFromUrl()
      prevRef.current = next
      dispatch({ type: 'hydrate', state: next })
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  // Backstop A — a selected document resolves its own project + folder path
  // (and the project's altId, needed by contents/wiki) from just its id, the
  // same resolver useGoToDocument uses. Runs until the project is fully filled.
  const selId = state.selected?.id ?? null
  const projAltId = state.project?.altId
  const reconciledSelRef = useRef<string | null>(null)
  useEffect(() => {
    if (!state.hubId || !selId || projAltId) return
    if (reconciledSelRef.current === selId) return // already resolved (even if it had no altId)
    const hubId = state.hubId
    let cancelled = false
    qc.fetchQuery({
      queryKey: ['location', hubId, selId],
      queryFn: () => api.itemLocation(hubId, selId),
      staleTime: 5 * 60 * 1000,
    })
      .then((loc) => {
        if (cancelled) return
        reconciledSelRef.current = selId
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
        dispatch({
          type: 'navigate',
          project,
          folderStack,
          // Keep the selected item itself (its name/kind hint); only its
          // location needed resolving.
          selected: state.selected,
          tab: state.selectedTab ?? undefined,
        })
      })
      .catch(() => {
        /* no access / deleted — keep the hint-based state */
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.hubId, selId, projAltId])

  // Backstop B — a project/folder permalink with no selected doc fills
  // project.altId from the projects-list cache (contents/wiki need it).
  const projId = state.project?.id ?? null
  useEffect(() => {
    if (!state.hubId || !projId || projAltId || selId) return
    const hubId = state.hubId
    let cancelled = false
    qc.fetchQuery({
      queryKey: ['projects', hubId],
      queryFn: () => api.projects(hubId),
      staleTime: 5 * 60 * 1000,
    })
      .then((list) => {
        if (cancelled) return
        const full = list.find((pr) => pr.id === projId)
        if (full?.altId) dispatch({ type: 'patchProject', project: full })
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.hubId, projId, projAltId, selId])

  const value = useMemo<NavCtx>(() => {
    const top = state.folderStack[state.folderStack.length - 1]
    return {
      ...state,
      currentFolderId: top ? top.id : null,
      setApp: (app) => dispatch({ type: 'setApp', app }),
      selectHub: (id, name) => {
        saveLastHub(id, name)
        dispatch({ type: 'selectHub', id, name })
      },
      selectProject: (project) => dispatch({ type: 'selectProject', project }),
      enterFolder: (folder) => dispatch({ type: 'enterFolder', folder }),
      selectItem: (item) => dispatch({ type: 'selectItem', item }),
      clearProject: () => dispatch({ type: 'clearProject' }),
      gotoProjectRoot: () => dispatch({ type: 'gotoProjectRoot' }),
      gotoFolder: (index) => dispatch({ type: 'gotoFolder', index }),
      navigate: (project, folderStack, selected, tab) =>
        dispatch({ type: 'navigate', project, folderStack, selected, tab }),
    }
  }, [state])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useNav(): NavCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useNav must be used within NavProvider')
  return ctx
}
