import { describe, expect, it } from 'vitest'
import {
  HUB_STORAGE_KEY,
  hubScopedKey,
  hubSlug,
  loadLastHub,
  resolveHubScopedKey,
  saveLastHub,
  type StorageLike,
} from './hubKeys'

// memStorage is a minimal in-memory StorageLike so these tests run in the
// plain node environment (no jsdom).
function memStorage(init: Record<string, string> = {}): StorageLike & { data: Map<string, string> } {
  const data = new Map(Object.entries(init))
  return {
    data,
    getItem: (k) => (data.has(k) ? data.get(k)! : null),
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  }
}

describe('hubSlug', () => {
  // Mirrors internal/hubslug/hubslug_test.go — the two implementations must
  // agree byte-for-byte so client per-hub keys line up with server profiles.
  const cases: [string, string, string][] = [
    ['alnum passthrough', 'abcXYZ123', 'abcXYZ123'],
    ['empty becomes unset', '', '_unset'],
    ['URN colons replaced', 'urn:adsk.ace:prod.scope:abc', 'urn_adsk.ace_prod.scope_abc'],
    ['slashes replaced', 'a/b/c', 'a_b_c'],
    ['spaces and special chars replaced', 'hello world! @#', 'hello_world____'],
    ['dot/dash/underscore preserved', 'ab.cd-ef_gh', 'ab.cd-ef_gh'],
    ['capped at 120 chars', 'x'.repeat(200), 'x'.repeat(120)],
    // Go iterates runes: one non-ASCII rune → one '_'. for..of matches that
    // (an emoji is one code point despite being two UTF-16 units).
    ['non-ASCII rune → one underscore', 'héllo', 'h_llo'],
    ['astral code point → one underscore', 'a\u{1F600}b', 'a_b'],
  ]
  for (const [name, input, want] of cases) {
    it(name, () => {
      expect(hubSlug(input)).toBe(want)
    })
  }
})

describe('hubScopedKey', () => {
  it('joins base and slug with a dot', () => {
    expect(hubScopedKey('fdc.colorMode', 'urn_adsk.ace_prod.scope_abc')).toBe(
      'fdc.colorMode.urn_adsk.ace_prod.scope_abc',
    )
  })
})

describe('lastHub round trip', () => {
  it('saves and loads', () => {
    const s = memStorage()
    saveLastHub('hub-1', 'Hub One', s)
    expect(loadLastHub(s)).toEqual({ id: 'hub-1', name: 'Hub One' })
  })

  it('tolerates absence and garbage', () => {
    expect(loadLastHub(memStorage())).toBeNull()
    expect(loadLastHub(memStorage({ [HUB_STORAGE_KEY]: 'not json' }))).toBeNull()
    expect(loadLastHub(memStorage({ [HUB_STORAGE_KEY]: '{"name":"no id"}' }))).toBeNull()
    expect(loadLastHub(null)).toBeNull()
  })
})

describe('resolveHubScopedKey', () => {
  const HUB = { [HUB_STORAGE_KEY]: JSON.stringify({ id: 'urn:x:hub1', name: 'Hub' }) }
  const scoped = 'fdc.colorMode.urn_x_hub1'

  it('falls back to the base key when no hub is remembered', () => {
    const s = memStorage({ 'fdc.colorMode': 'dark' })
    expect(resolveHubScopedKey('fdc.colorMode', s)).toBe('fdc.colorMode')
    expect(s.data.get('fdc.colorMode')).toBe('dark') // untouched
  })

  it('seeds the scoped key from the legacy value, keeping the legacy key', () => {
    const s = memStorage({ ...HUB, 'fdc.colorMode': 'dark' })
    expect(resolveHubScopedKey('fdc.colorMode', s)).toBe(scoped)
    expect(s.data.get(scoped)).toBe('dark') // carried over
    expect(s.data.get('fdc.colorMode')).toBe('dark') // NOT deleted
  })

  it('never overwrites an existing scoped value', () => {
    const s = memStorage({ ...HUB, 'fdc.colorMode': 'dark', [scoped]: 'light' })
    expect(resolveHubScopedKey('fdc.colorMode', s)).toBe(scoped)
    expect(s.data.get(scoped)).toBe('light')
  })

  it('writes nothing when there is no legacy value', () => {
    const s = memStorage({ ...HUB })
    expect(resolveHubScopedKey('fdc.colorMode', s)).toBe(scoped)
    expect(s.data.has(scoped)).toBe(false)
  })
})
