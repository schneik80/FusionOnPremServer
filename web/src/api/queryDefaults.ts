import { QueryCache, MutationCache, type DefaultOptions } from '@tanstack/react-query'
import { ApiError } from './client'
import { reportRateLimited } from '../state/quota'

// One place for the react-query posture, shared by the SPA (main.tsx) and the
// Fusion palette (embed/main.tsx) so the two never drift.

export const DAY = 24 * 60 * 60 * 1000

// Browsing reads (hubs, projects, contents, details, references) are fresh
// for five minutes; per-version facts (classify, thumbnail, properties) for
// ever. A hook that forgets to say gets a 30 s floor, so a remount never
// refetches by accident.
export const STALE_BROWSE = 5 * 60 * 1000
export const STALE_FLOOR = 30 * 1000

// Bounded retry: a 429 is retried ONCE, after the wait the server named
// (retryAfterMs) — the server now queues and cools down, so one retry lands
// on a replenished budget instead of spending more of a spent one. Terminal
// client errors are never retried; anything else gets one retry.
export function shouldRetry(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError) {
    if (error.status === 429) return failureCount < 1
    if (error.status === 401 || error.status === 403 || error.status === 404 || error.status === 409) return false
  }
  return failureCount < 1
}

const MIN_RETRY_MS = 1000
const MAX_RETRY_MS = 30_000

export function retryDelayFor(failureCount: number, error: unknown): number {
  if (error instanceof ApiError && error.status === 429) {
    const hint = error.retryAfterMs ?? 0
    const base = Math.min(MAX_RETRY_MS, Math.max(MIN_RETRY_MS, hint))
    return base + Math.floor(Math.random() * 250)
  }
  return Math.min(1000 * 2 ** failureCount, MAX_RETRY_MS)
}

// reportQueryError feeds a 429 to the quota store so the banner reacts on the
// first one, before any poll.
export function reportQueryError(error: unknown): void {
  if (error instanceof ApiError && error.status === 429) reportRateLimited(error.retryAfterMs)
}

export const queryDefaults: DefaultOptions = {
  queries: {
    refetchOnWindowFocus: false,
    // A network flap used to refire every mounted stale query at once —
    // exactly the burst the quota punishes. Realtime keys (chat, tasks,
    // production, notifications) opt back in per hook.
    refetchOnReconnect: false,
    staleTime: STALE_FLOOR,
    gcTime: DAY,
    retry: shouldRetry,
    retryDelay: retryDelayFor,
  },
}

export function newQueryCaches() {
  return {
    queryCache: new QueryCache({ onError: reportQueryError }),
    mutationCache: new MutationCache({ onError: reportQueryError }),
  }
}
