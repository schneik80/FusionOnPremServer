import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import i18n, { ensureLocale, isLocale, SUPPORTED_LOCALES, type Locale } from '../i18n'

// LocaleProvider mirrors ColorModeProvider: one persisted preference, applied
// live. Switching loads the target catalogs (lazy chunks), tells i18next —
// which re-renders every useTranslation consumer without a reload — and
// stamps <html lang> for the browser (hyphenation, spellcheck, a11y).
// User-entered data is untouched: locale only affects render-time strings.

const STORAGE_KEY = 'fdc.locale'

interface LocaleCtx {
  locale: Locale
  setLocale: (l: Locale) => void
  available: readonly Locale[]
}

const Ctx = createContext<LocaleCtx | null>(null)

function loadStored(): Locale {
  const v = localStorage.getItem(STORAGE_KEY)
  return isLocale(v) ? v : 'en'
}

async function applyLocale(l: Locale): Promise<void> {
  await ensureLocale(l)
  await i18n.changeLanguage(l)
  document.documentElement.lang = l
}

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(loadStored)

  // Apply the stored preference once on mount (init starts at 'en' so the
  // first paint never blocks on a catalog chunk).
  useEffect(() => {
    if (locale !== 'en') void applyLocale(locale)
    else document.documentElement.lang = 'en'
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l)
    localStorage.setItem(STORAGE_KEY, l)
    void applyLocale(l)
  }, [])

  const value = useMemo(
    () => ({ locale, setLocale, available: SUPPORTED_LOCALES }),
    [locale, setLocale],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useLocale(): LocaleCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useLocale must be used within LocaleProvider')
  return ctx
}
