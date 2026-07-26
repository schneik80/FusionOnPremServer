// Hub-scoped client state: the remembered last hub (fls.lastHub) and the
// per-hub localStorage keys client settings live under.
//
// The server locks every session to ONE hub (POST /api/session/hub); the
// client mirrors that boundary for its own settings so each hub keeps its own
// visual identity (a deliberate anti-mixup cue between clients' hubs). All
// functions take an optional StorageLike so pure unit tests can run without a
// DOM; production callers use the defaults.

// StorageLike is the slice of the DOM Storage interface these helpers need.
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

// safeLocalStorage returns window.localStorage, or null where it is
// unavailable (private mode, non-browser test environment).
export function safeLocalStorage(): StorageLike | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage
  } catch {
    return null
  }
}

// The last selected hub, remembered per browser. It feeds (a) the HubGate
// auto-relock (re-POST /api/session/hub on a fresh login when the remembered
// hub is still in the user's list) and (b) resolveHubScopedKey below.
export const HUB_STORAGE_KEY = 'fls.lastHub'

export interface SavedHub {
  id: string
  name: string
}

export function loadLastHub(storage: StorageLike | null = safeLocalStorage()): SavedHub | null {
  try {
    const raw = storage?.getItem(HUB_STORAGE_KEY)
    if (!raw) return null
    const v = JSON.parse(raw) as SavedHub
    return v && typeof v.id === 'string' ? v : null
  } catch {
    return null
  }
}

export function saveLastHub(
  id: string,
  name: string,
  storage: StorageLike | null = safeLocalStorage(),
) {
  try {
    storage?.setItem(HUB_STORAGE_KEY, JSON.stringify({ id, name }))
  } catch {
    /* storage unavailable (private mode / quota) — non-fatal */
  }
}

// hubSlug mirrors the server's internal/hubslug.Slug byte-for-byte: any
// character outside [A-Za-z0-9_.-] becomes '_', capped at 120. Go iterates
// runes and caps BYTES, but since every non-matching rune collapses to a
// single ASCII '_' the output is pure ASCII, so a code-point cap here is the
// same cap. Iterating with for..of matches Go's rune iteration (a surrogate
// pair is one code point → one '_').
export function hubSlug(hubId: string): string {
  if (hubId === '') return '_unset'
  let out = ''
  for (const ch of hubId) {
    out += /^[A-Za-z0-9_.-]$/.test(ch) ? ch : '_'
  }
  return out.length > 120 ? out.slice(0, 120) : out
}

// hubScopedKey is the per-hub form of a client-setting localStorage key.
export function hubScopedKey(base: string, slug: string): string {
  return `${base}.${slug}`
}

// resolveHubScopedKey returns the storage key a per-hub client setting
// (fdc.colorMode, fdc.themeOverrides) reads and writes for THIS page load.
//
// Design note — why fls.lastHub and not React state: the providers mount
// before authMe resolves, so the hub isn't knowable through React yet, but
// fls.lastHub IS synchronously readable. This is sound because every hub
// change is a full reload: the gate and the Settings switch flow both save
// fls.lastHub BEFORE location.assign('/'), so by the time providers mount
// again the key already points at the new hub. Before any hub is remembered
// (first run) the legacy global key is used; once a hub is known, an absent
// scoped key is seeded from the legacy value — which is kept, not deleted —
// so pre-isolation settings carry over into each hub.
export function resolveHubScopedKey(
  base: string,
  storage: StorageLike | null = safeLocalStorage(),
): string {
  const hub = loadLastHub(storage)
  if (!hub) return base
  const key = hubScopedKey(base, hubSlug(hub.id))
  try {
    if (storage && storage.getItem(key) === null) {
      const legacy = storage.getItem(base)
      if (legacy !== null) storage.setItem(key, legacy)
    }
  } catch {
    /* storage unavailable — fall through with the computed key */
  }
  return key
}
