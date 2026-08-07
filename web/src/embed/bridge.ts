// The Fusion palette JS bridge. Inside a Fusion palette the host injects
// `window.adsk` (JS → Python via fusionSendData) and calls
// `window.fusionJavaScriptHandler.handle(action, data)` (Python → JS). The
// page must also work in a plain browser for development, so every use is
// feature-detected and window.open is the fallback.

import { SET_CONTEXT_EVENT } from './context'

// The window.adsk / fusionJavaScriptHandler globals are declared in
// src/adsk.d.ts (shared with the composer's screenshot capture).

export function hasBridge(): boolean {
  return typeof window.adsk?.fusionSendData === 'function'
}

function send(action: string, payload: unknown): void {
  try {
    void window.adsk?.fusionSendData(action, JSON.stringify(payload ?? {}))
  } catch {
    /* bridge died mid-session — the page keeps working on its own */
  }
}

// openExternal hands a full-app URL to Fusion so it opens in the user's real
// browser (a palette is no place for the whole SPA). Outside Fusion, a new tab.
export function openExternal(url: string): void {
  if (hasBridge()) send('openExternal', { url })
  else window.open(url, '_blank', 'noopener')
}

// installBridge registers the Python → JS receiver. Must run before render so
// a setContext raced against first paint is never dropped. Payloads are
// re-dispatched as window CustomEvents to keep React parts subscribable.
export function installBridge(): void {
  window.fusionJavaScriptHandler = {
    handle(action: string, data: string): string {
      try {
        if (action === 'setContext') {
          const detail: unknown = data ? JSON.parse(data) : {}
          window.dispatchEvent(new CustomEvent(SET_CONTEXT_EVENT, { detail }))
        }
      } catch {
        /* malformed payload — ignore rather than break the palette */
      }
      return 'OK'
    },
  }
  // Tell Python the page is alive; it answers with a fresh setContext (and
  // confirms the bridge round-trip works on a server-served page).
  if (hasBridge()) send('ready', {})
}
