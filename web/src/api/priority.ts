// Request priority for the server's APS budget (internal/apsbudget). The
// server classifies routes on its own; the header exists to DEMOTE work the
// SPA knows is prefetch (a dashboard roll-up behind another tab) and to
// PROMOTE a per-row call the user just clicked. Images cannot carry headers,
// so thumbnailSrc appends ?p= instead — the server reads both.
//
// Keeping the names here, and nowhere else, is what lets the transport change
// without touching hooks.
export type Priority = 0 | 1 | 2 // 0 user-blocking, 1 in-viewport row, 2 background

export const PRIORITY_HEADER = 'X-FLS-Priority'
export const PRIORITY_PARAM = 'p'
// The server's own default for an unclassified route is P0; the SPA's
// default for a call that did not say is the conservative middle.
export const DEFAULT_PRIORITY: Priority = 1

export interface RequestOptions {
  priority?: Priority
}

// withPriority merges the priority header into a RequestInit without
// clobbering an explicit Content-Type, and leaves headers alone for a
// FormData body (the browser must set the multipart boundary itself, and a
// Headers object with only our header is fine for that).
export function withPriority(init: RequestInit | undefined, p: Priority): RequestInit {
  const headers = new Headers(init?.headers ?? undefined)
  headers.set(PRIORITY_HEADER, String(p))
  return { ...init, headers }
}
