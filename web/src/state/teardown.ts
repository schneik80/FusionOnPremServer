import type { QueryClient } from '@tanstack/react-query'
import { QUERY_CACHE_KEY } from '../queryPersist'
import { safeLocalStorage, type StorageLike } from './hubKeys'

// teardownAndReload is the shared client teardown for every "start over"
// flow — logout and the Settings → Connection hub switch. It drops the
// in-memory query cache, removes the persisted copy (so a different user, or
// the same user in a different hub, never briefly sees the prior state), and
// reloads at the root.
//
// Anything that must survive the reload — the hub switch saving fls.lastHub
// so the per-hub theme keys resolve to the NEW hub on remount — must be
// written BEFORE calling this.
//
// The storage/navigate injection points exist for unit tests only; production
// callers pass just the QueryClient.
export function teardownAndReload(
  qc: QueryClient,
  opts?: {
    storage?: StorageLike | null
    navigate?: (url: string) => void
  },
) {
  qc.clear()
  const storage = opts && 'storage' in opts ? opts.storage : safeLocalStorage()
  try {
    storage?.removeItem(QUERY_CACHE_KEY)
  } catch {
    /* storage unavailable — nothing to clear */
  }
  const navigate = opts?.navigate ?? ((url: string) => window.location.assign(url))
  navigate('/')
}
