import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import type { ColorMode } from '../theme'
import { resolveHubScopedKey } from './hubKeys'

// The theme mode is a PER-HUB setting (each client hub keeps its own visual
// identity — a deliberate anti-mixup cue). The provider resolves its storage
// key once at mount from fls.lastHub: the hub isn't knowable through React
// yet (authMe resolves async), but every hub change is a full reload that
// saves fls.lastHub first, so a mount-time read is always current — see
// resolveHubScopedKey. BASE_KEY doubles as the legacy global key scoped keys
// seed from.
const BASE_KEY = 'fdc.colorMode'

interface ColorModeCtx {
  mode: ColorMode
  /** explicit user choice, or null when following the system preference */
  preference: ColorMode | 'system'
  setPreference: (p: ColorMode | 'system') => void
  toggle: () => void
}

const Ctx = createContext<ColorModeCtx | null>(null)

function systemMode(): ColorMode {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches
    ? 'dark'
    : 'light'
}

function loadPreference(key: string): ColorMode | 'system' {
  const v = localStorage.getItem(key)
  if (v === 'light' || v === 'dark') return v
  return 'system'
}

export function ColorModeProvider({ children }: { children: ReactNode }) {
  const [storageKey] = useState(() => resolveHubScopedKey(BASE_KEY))
  const [preference, setPreferenceState] = useState<ColorMode | 'system'>(() =>
    loadPreference(storageKey),
  )
  const [system, setSystem] = useState<ColorMode>(systemMode)

  // Track OS theme changes while the user is on "system".
  useEffect(() => {
    const mql = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => setSystem(mql.matches ? 'dark' : 'light')
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [])

  const setPreference = useCallback(
    (p: ColorMode | 'system') => {
      setPreferenceState(p)
      // 'system' is stored explicitly rather than removeItem'd: an absent
      // scoped key would be re-seeded from the legacy global value on the
      // next load, silently undoing the user's choice.
      localStorage.setItem(storageKey, p)
    },
    [storageKey],
  )

  const mode: ColorMode = preference === 'system' ? system : preference

  const toggle = useCallback(() => {
    setPreference(mode === 'dark' ? 'light' : 'dark')
  }, [mode, setPreference])

  const value = useMemo(
    () => ({ mode, preference, setPreference, toggle }),
    [mode, preference, setPreference, toggle],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useColorMode(): ColorModeCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useColorMode must be used within ColorModeProvider')
  return ctx
}
