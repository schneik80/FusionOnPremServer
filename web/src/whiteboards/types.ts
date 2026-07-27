// TypeScript mirrors of the whiteboard DTOs. Keep in sync with
// server/dto_whiteboards.go.
//
// A board's tldraw document is deliberately NOT part of this type: it is
// fetched on its own endpoint when a board is opened, so listing boards never
// ships their shapes.

export interface WhiteboardUser {
  id: string
  name?: string
  email?: string
}

export interface Whiteboard {
  id: string
  num: number
  projectId: string
  hubId: string
  projectName: string
  name: string
  createdBy: WhiteboardUser
  createdAt: string
  updatedAt: string
  updatedBy: WhiteboardUser
  snapshotBytes: number
  // docRev is the document's save counter. The canvas sends the revision it
  // loaded back with each save, so a board two people have open can't be
  // silently overwritten by whoever saves last.
  docRev: number
}

export interface WhiteboardCaps {
  write: boolean
  moderate: boolean
}

export interface WhiteboardList {
  whiteboards: Whiteboard[]
  capabilities: WhiteboardCaps
}

export interface WhiteboardDraft {
  hubId: string
  projectName: string
  name: string
}

export interface WhiteboardPatch {
  name?: string
}

export const boardDisplayId = (b: { num: number }) => `W-${b.num}`
