import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import type { ColorMode, ThemeOverride, ThemeTokens } from '../theme'
import { resolveHubScopedKey } from './hubKeys'

// Per-mode custom color overrides (Settings → Appearance → Custom colors).
// Stored as { light?: {...}, dark?: {...} } so each theme keeps its own edits;
// makeTheme merges the active mode's bag over the stock tokens.
//
// Overrides are a PER-HUB setting: the storage key is resolved once at mount
// from fls.lastHub (the hub isn't knowable through React yet, and every hub
// change is a full reload that saves fls.lastHub first — see
// resolveHubScopedKey). BASE_KEY doubles as the legacy global key scoped keys
// seed from.

const BASE_KEY = 'fdc.themeOverrides'

export type TokenKey = keyof ThemeTokens | 'accent'

export interface ThemeOverrides {
  light?: ThemeOverride
  dark?: ThemeOverride
}

interface ThemeOverridesCtx {
  overrides: ThemeOverrides
  /** set one token's override for a mode; null/undefined clears that token */
  setToken: (mode: ColorMode, key: TokenKey, value: string | null) => void
  /** clear every override for one mode */
  reset: (mode: ColorMode) => void
  /** clear everything (both modes) */
  resetAll: () => void
}

const Ctx = createContext<ThemeOverridesCtx | null>(null)

function load(key: string): ThemeOverrides {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: ThemeOverrides = {}
    for (const mode of ['light', 'dark'] as const) {
      const bag = (parsed as Record<string, unknown>)[mode]
      if (!bag || typeof bag !== 'object' || Array.isArray(bag)) continue
      const clean: Record<string, string> = {}
      for (const [k, v] of Object.entries(bag)) {
        if (typeof v === 'string') clean[k] = v
      }
      if (Object.keys(clean).length) out[mode] = clean as ThemeOverride
    }
    return out
  } catch {
    return {}
  }
}

function persist(key: string, next: ThemeOverrides) {
  // Always write, even when empty: removing an empty scoped key would get
  // re-seeded from the legacy global overrides on the next load, resurrecting
  // colors the user just cleared.
  localStorage.setItem(key, JSON.stringify(next))
}

export function ThemeOverridesProvider({ children }: { children: ReactNode }) {
  const [storageKey] = useState(() => resolveHubScopedKey(BASE_KEY))
  const [overrides, setOverrides] = useState<ThemeOverrides>(() => load(storageKey))

  const setToken = useCallback(
    (mode: ColorMode, key: TokenKey, value: string | null) => {
      setOverrides((prev) => {
        const bag: Record<string, string> = { ...(prev[mode] as Record<string, string> | undefined) }
        if (value == null) delete bag[key]
        else bag[key] = value
        const next: ThemeOverrides = { ...prev }
        if (Object.keys(bag).length) next[mode] = bag as ThemeOverride
        else delete next[mode]
        persist(storageKey, next)
        return next
      })
    },
    [storageKey],
  )

  const reset = useCallback(
    (mode: ColorMode) => {
      setOverrides((prev) => {
        const next: ThemeOverrides = { ...prev }
        delete next[mode]
        persist(storageKey, next)
        return next
      })
    },
    [storageKey],
  )

  const resetAll = useCallback(() => {
    setOverrides(() => {
      persist(storageKey, {})
      return {}
    })
  }, [storageKey])

  const value = useMemo(
    () => ({ overrides, setToken, reset, resetAll }),
    [overrides, setToken, reset, resetAll],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useThemeOverrides(): ThemeOverridesCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useThemeOverrides must be used within ThemeOverridesProvider')
  return ctx
}
