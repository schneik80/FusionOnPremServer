// Notification types mirror the server's dto_notifications.go. The bell is
// the app-chrome inbox: the caller's own per-user, per-hub notifications. The
// server is the sole author; the client reads, marks read, and dismisses.
//
// Display text is composed client-side from `kind` + these params so it
// localizes across the six catalogs; `subject` / `channelName` are user data
// rendered verbatim and never translated.

// NotifKind is a stable enum token (rendered through i18n enums → notifKind).
export type NotifKind = 'mention' | 'assigned' | 'due_soon' | 'overdue' | 'production'

export interface NotifUser {
  id: string
  name?: string
  email?: string
}

export interface Notification {
  id: string
  kind: NotifKind
  hubId?: string
  projectId?: string
  projectName?: string
  actor?: NotifUser
  subject?: string // task title / channel name — user data, never translated
  ref?: string // fls: deep-link token for click-through (task/production)
  channelId?: string // mention context
  channelName?: string // mention context
  messageSeq?: number // mention context
  read: boolean
  createdAt: string
  readAt?: string
}

export interface NotificationList {
  notifications: Notification[]
  unread: number
}

export interface NotifCount {
  unread: number
}
