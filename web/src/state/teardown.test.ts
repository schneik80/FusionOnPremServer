import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { QUERY_CACHE_KEY } from '../queryPersist'
import type { StorageLike } from './hubKeys'
import { teardownAndReload } from './teardown'

function memStorage(init: Record<string, string> = {}): StorageLike & { data: Map<string, string> } {
  const data = new Map(Object.entries(init))
  return {
    data,
    getItem: (k) => (data.has(k) ? data.get(k)! : null),
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  }
}

describe('teardownAndReload', () => {
  it('clears the query cache, drops the persisted copy, and reloads at the root', () => {
    const qc = new QueryClient()
    qc.setQueryData(['hubs'], [{ id: 'h1' }])
    const storage = memStorage({ [QUERY_CACHE_KEY]: '{"clientState":{}}', 'fls.lastHub': 'keep' })
    const navigate = vi.fn()

    teardownAndReload(qc, { storage, navigate })

    expect(qc.getQueryData(['hubs'])).toBeUndefined()
    expect(storage.data.has(QUERY_CACHE_KEY)).toBe(false)
    // fls.lastHub survives — the hub switch saves it BEFORE tearing down and
    // the next mount's per-hub keys depend on it.
    expect(storage.data.get('fls.lastHub')).toBe('keep')
    expect(navigate).toHaveBeenCalledExactlyOnceWith('/')
  })

  it('tolerates a null storage (private mode)', () => {
    const qc = new QueryClient()
    const navigate = vi.fn()
    teardownAndReload(qc, { storage: null, navigate })
    expect(navigate).toHaveBeenCalledExactlyOnceWith('/')
  })
})
