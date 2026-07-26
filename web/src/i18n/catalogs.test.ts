import { describe, expect, it } from 'vitest'

// Catalog shape invariants: every locale's key set must be a subset of
// English (en is the source of truth; extra keys in a translation are dead
// weight or typos), and no catalog may contain empty values (an empty string
// silently renders as nothing — fall back to en by OMITTING the key instead).

const all = import.meta.glob('./locales/*/*.json', { eager: true }) as Record<
  string,
  { default: Record<string, unknown> }
>

function flatten(obj: Record<string, unknown>, prefix = ''): Map<string, string> {
  const out = new Map<string, string>()
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v !== null && typeof v === 'object') {
      for (const [k2, v2] of flatten(v as Record<string, unknown>, key)) out.set(k2, v2)
    } else {
      out.set(key, String(v))
    }
  }
  return out
}

interface Catalog {
  locale: string
  ns: string
  keys: Map<string, string>
}

const catalogs: Catalog[] = Object.entries(all).map(([path, mod]) => {
  const parts = path.split('/')
  return {
    locale: parts[parts.length - 2],
    ns: parts[parts.length - 1].replace(/\.json$/, ''),
    keys: flatten(mod.default),
  }
})

const enByNs = new Map(catalogs.filter((c) => c.locale === 'en').map((c) => [c.ns, c.keys]))

describe('i18n catalogs', () => {
  it('English catalogs exist', () => {
    expect(enByNs.size).toBeGreaterThan(0)
  })

  it('no catalog contains empty values', () => {
    for (const c of catalogs) {
      for (const [key, value] of c.keys) {
        expect(value.trim(), `${c.locale}/${c.ns}:${key} is empty`).not.toBe('')
      }
    }
  })

  it('every non-en catalog key exists in English', () => {
    for (const c of catalogs) {
      if (c.locale === 'en') continue
      const en = enByNs.get(c.ns)
      expect(en, `namespace ${c.ns} has a ${c.locale} catalog but no en catalog`).toBeDefined()
      for (const key of c.keys.keys()) {
        expect(en!.has(key), `${c.locale}/${c.ns}:${key} has no en counterpart`).toBe(true)
      }
    }
  })

  it('interpolation placeholders match English', () => {
    const vars = (s: string) => [...s.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]).sort()
    for (const c of catalogs) {
      if (c.locale === 'en') continue
      const en = enByNs.get(c.ns)
      if (!en) continue
      for (const [key, value] of c.keys) {
        const ref = en.get(key)
        if (ref === undefined) continue
        expect(vars(value), `${c.locale}/${c.ns}:${key} placeholder mismatch`).toEqual(vars(ref))
      }
    }
  })
})
