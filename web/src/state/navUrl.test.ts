import { describe, expect, it } from 'vitest'
import type { Item } from '../api/types'
import type { NavState } from './nav'
import { navToSearch, searchToNav, shouldPush } from './navUrl'

// The project tab and the selected board are the two params with a *condition*
// attached rather than a plain round-trip: ProjectPanel only exists at a
// project's root with nothing selected, and WhiteboardsApp latches a board as
// soon as the project has one — so without the gates every URL in the app
// would carry a ptab/board it never reads back.

const project: Item = { id: 'p1', name: 'Alpha', kind: 'project', isContainer: true }
const folder: Item = { id: 'f1', name: 'Parts', kind: 'folder', isContainer: true }
const doc: Item = { id: 'urn:x:i1', name: 'Bracket', kind: 'design', isContainer: false }

function state(over: Partial<NavState> = {}): NavState {
  return {
    app: 'browser',
    hubId: 'h1',
    hubName: 'Hub',
    project: null,
    folderStack: [],
    selected: null,
    selectedTab: null,
    projectTab: null,
    boardId: null,
    ...over,
  }
}

describe('navUrl project tab and board', () => {
  it('round-trips a board permalink', () => {
    const s = state({ project, projectTab: 'whiteboards', boardId: 'w3' })
    const search = navToSearch(s)
    expect(search).toContain('ptab=whiteboards')
    expect(search).toContain('board=w3')

    const back = searchToNav(search)
    expect(back.project?.id).toBe('p1')
    expect(back.projectTab).toBe('whiteboards')
    expect(back.boardId).toBe('w3')
  })

  it('round-trips a non-whiteboard project tab without a board', () => {
    const search = navToSearch(state({ project, projectTab: 'chat', boardId: 'w3' }))
    expect(search).toContain('ptab=chat')
    expect(search).not.toContain('board=')
    expect(searchToNav(search).boardId).toBeNull()
  })

  it('drops the tab under a folder or a selected document', () => {
    // ProjectPanel is replaced by the contents/details view in both cases.
    for (const over of [{ folderStack: [folder] }, { selected: doc }]) {
      const search = navToSearch(state({ project, projectTab: 'whiteboards', boardId: 'w3', ...over }))
      expect(search).not.toContain('ptab=')
      expect(search).not.toContain('board=')
    }
  })

  it('ignores ptab and board from a hand-edited URL that has no project', () => {
    const back = searchToNav('hub=h1&ptab=whiteboards&board=w3')
    expect(back.projectTab).toBeNull()
    expect(back.boardId).toBeNull()
  })

  it('keeps the details tab and the project tab in separate slots', () => {
    // One field meaning both is the bug that left the notification bell's
    // deep-link on the dashboard; dtab and ptab must never collide.
    const withDoc = navToSearch(state({ project, selected: doc, selectedTab: 'whereUsed' }))
    expect(withDoc).toContain('dtab=whereUsed')
    expect(withDoc).not.toContain('ptab=')

    const withTab = navToSearch(state({ project, projectTab: 'tasks' }))
    expect(withTab).toContain('ptab=tasks')
    expect(withTab).not.toContain('dtab=')
  })

  it('replaces rather than pushes history for a tab or board change', () => {
    // Same location, different view of it — like the details-tab switch.
    const a = state({ project, projectTab: 'whiteboards', boardId: 'w3' })
    expect(shouldPush(a, { ...a, projectTab: 'chat' })).toBe(false)
    expect(shouldPush(a, { ...a, boardId: 'w7' })).toBe(false)
    // Crossing to another project is a real move.
    expect(shouldPush(a, { ...a, project: { ...project, id: 'p2', name: 'Beta' } })).toBe(true)
  })
})
