// The palette's document context: which hub/project (in Data Management id
// space — what Fusion's Python API can read) the embedded chat should show.
// It arrives once in the page URL and again on every Fusion document switch
// via the adsk bridge ("setContext" → the fls:setContext window event).

export interface EmbedContext {
  dmHubId: string
  dmProjectId: string
  /** Display-name hint while the resolve call is in flight. */
  projectName: string
  /** Lineage URN of the active document, when it is saved. Unused for now. */
  itemId: string
  theme: 'light' | 'dark'
  /** Channel to open once chat renders (permalink parity with the SPA). */
  channelId: string | null
}

export const SET_CONTEXT_EVENT = 'fls:setContext'
export const HUB_GATE_EVENT = 'fls:hubgate'

function asTheme(v: unknown): 'light' | 'dark' {
  return v === 'light' ? 'light' : 'dark'
}

export function contextFromSearch(search: string): EmbedContext {
  const p = new URLSearchParams(search)
  return {
    dmHubId: p.get('dmHubId') ?? '',
    dmProjectId: p.get('dmProjectId') ?? '',
    projectName: p.get('projectName') ?? '',
    itemId: p.get('itemId') ?? '',
    theme: asTheme(p.get('theme')),
    channelId: p.get('chan'),
  }
}

// contextFromPayload folds a setContext payload (already JSON-parsed by the
// bridge) over the current context. Unknown/missing fields keep their values
// so a partial push (e.g. theme only) never blanks the project.
export function contextFromPayload(cur: EmbedContext, data: unknown): EmbedContext {
  if (typeof data !== 'object' || data === null) return cur
  const d = data as Record<string, unknown>
  return {
    dmHubId: typeof d.dmHubId === 'string' && d.dmHubId ? d.dmHubId : cur.dmHubId,
    dmProjectId:
      typeof d.dmProjectId === 'string' && d.dmProjectId ? d.dmProjectId : cur.dmProjectId,
    projectName: typeof d.projectName === 'string' ? d.projectName : cur.projectName,
    itemId: typeof d.itemId === 'string' ? d.itemId : cur.itemId,
    theme: 'theme' in d ? asTheme(d.theme) : cur.theme,
    channelId: cur.channelId,
  }
}
