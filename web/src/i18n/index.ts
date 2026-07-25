// i18n bootstrap. English is the fallback and ships eagerly in the entry
// bundle; every other locale's catalogs are lazy chunks loaded on first
// switch (tldraw-lazy-load discipline: the entry bundle carries only what
// every session needs). Keys are semantic (`settings:port.applyRestart`),
// never English-as-key — copy edits must not silently orphan translations.

import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'

export const SUPPORTED_LOCALES = ['en', 'de', 'fr', 'es', 'it', 'pt'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]

// Native-language names for the picker (a language's own name never
// translates).
export const LOCALE_LABEL: Record<Locale, string> = {
  en: 'English',
  de: 'Deutsch',
  fr: 'Français',
  es: 'Español',
  it: 'Italiano',
  pt: 'Português',
}

export function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (SUPPORTED_LOCALES as readonly string[]).includes(v)
}

// ./locales/<locale>/<namespace>.json — the file name is the namespace.
const eagerEn = import.meta.glob('./locales/en/*.json', { eager: true }) as Record<
  string,
  { default: Record<string, unknown> }
>
const lazyRest = import.meta.glob(['./locales/*/*.json', '!./locales/en/*.json']) as Record<
  string,
  () => Promise<{ default: Record<string, unknown> }>
>

const nsOf = (path: string) => path.split('/').pop()!.replace(/\.json$/, '')
const localeOf = (path: string) => {
  const parts = path.split('/')
  return parts[parts.length - 2]
}

const enResources: Record<string, Record<string, unknown>> = {}
for (const [path, mod] of Object.entries(eagerEn)) {
  enResources[nsOf(path)] = mod.default
}

export const NAMESPACES = Object.keys(enResources)

const loadedLocales = new Set<string>(['en'])

// ensureLocale loads a locale's catalogs (idempotent). Callers switch with
// setLocale in state/locale.tsx — don't call i18n.changeLanguage directly.
export async function ensureLocale(locale: Locale): Promise<void> {
  if (loadedLocales.has(locale)) return
  await Promise.all(
    Object.entries(lazyRest)
      .filter(([path]) => localeOf(path) === locale)
      .map(async ([path, load]) => {
        const mod = await load()
        i18n.addResourceBundle(locale, nsOf(path), mod.default, true, true)
      }),
  )
  loadedLocales.add(locale)
}

void i18n.use(initReactI18next).init({
  resources: { en: enResources },
  // The stored preference is applied by LocaleProvider on mount; starting
  // at 'en' keeps init synchronous so the first paint never blocks.
  lng: 'en',
  fallbackLng: 'en',
  ns: NAMESPACES,
  defaultNS: 'common',
  interpolation: { escapeValue: false }, // React already escapes
  react: { useSuspense: false },
})

export default i18n
