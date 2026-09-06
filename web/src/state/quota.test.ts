import { beforeEach, describe, expect, it } from 'vitest'
import {
  applyHeader,
  applyServerStatus,
  cooldownRemainingMs,
  getSnapshot,
  HOT_WINDOW_MS,
  isHot,
  reportRateLimited,
  resetForTesting,
  subscribe,
  type QuotaStatus,
} from './quota'

const T0 = 1_000_000

function serverStatus(over: Partial<QuotaStatus>): QuotaStatus {
  return {
    level: 'ok',
    throttled: false,
    retryAfterMs: 0,
    capacity: 4800,
    available: 4800,
    pointsUsedLastMinute: 0,
    inFlight: 0,
    queued: { p0: 0, p1: 0, p2: 0 },
    refused: 0,
    trips: 0,
    enabled: true,
    ...over,
  }
}

describe('quota store', () => {
  beforeEach(() => resetForTesting())

  it('is quiet by default', () => {
    expect(isHot(getSnapshot(), T0)).toBe(false)
    expect(cooldownRemainingMs(getSnapshot(), T0)).toBe(0)
  })

  it('a client-side 429 makes it hot for a window and carries the wait', () => {
    let fired = 0
    const off = subscribe(() => fired++)
    reportRateLimited(5000, T0)
    expect(fired).toBe(1)
    const s = getSnapshot()
    expect(s.level).toBe('slow')
    expect(isHot(s, T0 + 1000)).toBe(true)
    expect(cooldownRemainingMs(s, T0 + 1000)).toBe(4000)
    expect(cooldownRemainingMs(s, T0 + 6000)).toBe(0)
    off()
  })

  it('headers drive the level; ok clears the deadline', () => {
    applyHeader('cooldown', T0 + 20_000, T0)
    expect(getSnapshot().level).toBe('cooldown')
    expect(cooldownRemainingMs(getSnapshot(), T0 + 5000)).toBe(15_000)
    applyHeader('bogus', null, T0 + 6000)
    expect(getSnapshot().level).toBe('cooldown')
    applyHeader('ok', null, T0 + 7000)
    expect(getSnapshot().level).toBe('ok')
    expect(isHot(getSnapshot(), T0 + 7000)).toBe(false)
  })

  it('a poll answer replaces the level and deadline; the later deadline wins', () => {
    reportRateLimited(30_000, T0)
    applyServerStatus(serverStatus({ level: 'cooldown', throttled: true, cooldownUntil: new Date(T0 + 10_000).toISOString() }), T0)
    expect(getSnapshot().level).toBe('cooldown')
    expect(cooldownRemainingMs(getSnapshot(), T0)).toBe(30_000)
    applyServerStatus(serverStatus({ level: 'ok' }), T0 + HOT_WINDOW_MS + 1)
    expect(isHot(getSnapshot(), T0 + HOT_WINDOW_MS + 1)).toBe(false)
  })
})
