// TypeScript mirrors of the task DTOs in server/dto_tasks.go. Keep field
// names in sync with the json tags there.

export type TaskStatus = 'todo' | 'inprogress' | 'blocked' | 'done'
export type TaskPriority = 'low' | 'medium' | 'high' | 'urgent'

export interface TaskUser {
  id: string
  name?: string
  email?: string
}

// Task always carries its project identity (projectId/hubId/projectName)
// so the cross-project "my tasks" list and fls:task cards can act on a
// task without extra lookups.
export interface Task {
  id: string
  num: number
  projectId: string
  hubId: string
  projectName: string
  title: string
  description?: string
  status: TaskStatus
  priority: TaskPriority
  dueDate?: string // YYYY-MM-DD
  assignee?: TaskUser
  createdBy: TaskUser
  createdAt: string
  updatedAt: string
  docRefs: string[] // fls:doc?… tokens, rendered as document cards
  rank: number // ordering within a status column on the Kanban board
  // Schedule (Gantt view). startDate/endDate come as a pair or not at all;
  // dueDate stays an independent deadline. progress 0 doubles as "unset".
  startDate?: string // YYYY-MM-DD
  endDate?: string // YYYY-MM-DD; >= startDate
  progress?: number // 0..100
  milestone?: boolean // scheduled with endDate == startDate
  dependsOn: string[] // finish-to-start predecessor task IDs (this project)
  stage?: string // Gantt grouping; the stage bar is derived, never stored
}

// TaskCaps is what the signed-in user may do with this project's tasks,
// derived server-side from their APS project role.
export interface TaskCaps {
  write: boolean
  moderate: boolean
}

export interface TaskList {
  tasks: Task[]
  capabilities: TaskCaps
}

export interface MyTasks {
  tasks: Task[]
}

export interface TaskDraft {
  hubId: string
  projectName: string
  title: string
  description?: string
  status?: TaskStatus
  priority?: TaskPriority
  dueDate?: string
  assignee?: TaskUser
  docRefs?: string[]
  startDate?: string
  endDate?: string
  progress?: number
  milestone?: boolean
  dependsOn?: string[]
  stage?: string
}

// TaskPatch: absent fields stay untouched; clearing the optional
// assignee/dueDate is explicit (mirrors the Go handler). clearSchedule
// clears startDate+endDate+milestone together — a one-sided schedule is
// invalid anyway; dependsOn/stage clear by sending empty.
export interface TaskPatch {
  title?: string
  description?: string
  status?: TaskStatus
  priority?: TaskPriority
  dueDate?: string
  assignee?: TaskUser
  clearAssignee?: boolean
  clearDueDate?: boolean
  docRefs?: string[]
  rank?: number
  startDate?: string
  endDate?: string
  clearSchedule?: boolean
  progress?: number
  milestone?: boolean
  dependsOn?: string[]
  stage?: string
}

// TaskShiftResult is POST /api/tasks/shift — the Gantt stage-bar drag moves
// a set of scheduled tasks by whole days in one atomic write.
export interface TaskShiftResult {
  tasks: Task[]
}

// STATUSES doubles as the Kanban column order (mirrors tasks.Statuses).
export const STATUSES: TaskStatus[] = ['todo', 'inprogress', 'blocked', 'done']

export const STATUS_LABEL: Record<TaskStatus, string> = {
  todo: 'To do',
  inprogress: 'In progress',
  blocked: 'Blocked',
  done: 'Done',
}

export const PRIORITIES: TaskPriority[] = ['low', 'medium', 'high', 'urgent']

export const PRIORITY_LABEL: Record<TaskPriority, string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  urgent: 'Urgent',
}

// taskDisplayId is the human-readable per-project task number.
export function taskDisplayId(t: { num: number }): string {
  return `T-${t.num}`
}

// isScheduled: a task appears on the Gantt iff it carries both dates
// (the server enforces the pairing).
export function isScheduled(t: Task): boolean {
  return !!t.startDate && !!t.endDate
}
