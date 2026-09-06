// The SPA's view of the server's APS budget: a tiny external store fed from
// three places — the X-FLS-Throttle headers every response carries, any 429
// a query or mutation sees, and GET /api/quota while slow. One store, one
// banner (components/QuotaBanner.tsx); widgets keep their own errors.
//
// Pure and DOM-free so it is unit-tested; useQuotaStatus wraps it.

export type ThrottleLevel = 'ok' | 'slow' | 'cooldown'

export interface QuotaStatus {
  level: ThrottleLevel
  throttled: boolean
  cooldownUntil?: string
  restCooldownUntil?: string
  retryAfterMs: number
  capacity: number
  available: number
  pointsUsedLastMinute: number
  inFlight: number
  queued: { p0: number; p1: number; p2: number }
  refused: number
  trips: number
  enabled: boolean
}

export interface QuotaState {
  // Last level the server reported (header or poll) and when.
  level: ThrottleLevel
  levelAt: number
  // Server-reported cooldown deadline (unix ms), 0 when none.
  untilMs: number
  // Last 429 the client itself saw, and the wait it carried.
  lastRateLimitedAt: number
  retryAfterMs: number
  server: QuotaStatus | null
}

// A client-side 429 keeps the banner up for this long even if the server
// never says "slow" (it may have answered ok before the burst landed).
export const HOT_WINDOW_MS = 60_000

const initial: QuotaState = {
  level: 'ok',
  levelAt: 0,
  untilMs: 0,
  lastRateLimitedAt: 0,
  retryAfterMs: 0,
  server: null,
}

let state: QuotaState = initial
const listeners = new Set<() => void>()

function emit(next: QuotaState) {
  state = next
  for (const l of listeners) l()
}

export function subscribe(cb: () => void): () => void {
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
  }
}

export function getSnapshot(): QuotaState {
  return state
}

// applyHeader records what a response's throttle headers said.
export function applyHeader(level: string | null, untilMs: number | null, now = Date.now()): void {
  if (level !== 'ok' && level !== 'slow' && level !== 'cooldown') return
  const until = level === 'ok' ? 0 : (untilMs ?? state.untilMs)
  if (level === state.level && until === state.untilMs && now - state.levelAt < 1000) return
  emit({ ...state, level, levelAt: now, untilMs: until })
}

// reportRateLimited records a 429 the client saw itself.
export function reportRateLimited(retryAfterMs: number | undefined, now = Date.now()): void {
  const wait = retryAfterMs && retryAfterMs > 0 ? retryAfterMs : 0
  emit({
    ...state,
    lastRateLimitedAt: now,
    retryAfterMs: wait,
    level: state.level === 'ok' ? 'slow' : state.level,
    levelAt: state.level === 'ok' ? now : state.levelAt,
  })
}

// applyServerStatus folds a GET /api/quota answer in.
export function applyServerStatus(s: QuotaStatus, now = Date.now()): void {
  const until = s.cooldownUntil ? Date.parse(s.cooldownUntil) : s.restCooldownUntil ? Date.parse(s.restCooldownUntil) : 0
  emit({ ...state, server: s, level: s.level, levelAt: now, untilMs: Number.isFinite(until) ? until : 0 })
}

// isHot: should the banner show and the poll run?
export function isHot(s: QuotaState, now = Date.now()): boolean {
  if (s.level !== 'ok') return true
  return s.lastRateLimitedAt > 0 && now - s.lastRateLimitedAt < HOT_WINDOW_MS
}

// cooldownRemainingMs: the later of the server's deadline and the client's
// own 429 wait, or 0.
export function cooldownRemainingMs(s: QuotaState, now = Date.now()): number {
  const serverEnd = s.untilMs
  const clientEnd = s.lastRateLimitedAt > 0 && s.retryAfterMs > 0 ? s.lastRateLimitedAt + s.retryAfterMs : 0
  const end = Math.max(serverEnd, clientEnd)
  return end > now ? end - now : 0
}

// resetForTesting clears the store between unit tests.
export function resetForTesting(): void {
  emit(initial)
}
