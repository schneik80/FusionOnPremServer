import { describe, expect, it } from 'vitest'
import { ApiError, parseRetryAfter } from './client'
import { retryDelayFor, shouldRetry } from './queryDefaults'

describe('shouldRetry', () => {
  it('retries a 429 exactly once, never terminal client errors, others once', () => {
    const rl = new ApiError(429, 'rate limited', 'rate_limited', 3000)
    expect(shouldRetry(0, rl)).toBe(true)
    expect(shouldRetry(1, rl)).toBe(false)
    for (const st of [401, 403, 404, 409]) expect(shouldRetry(0, new ApiError(st, 'x'))).toBe(false)
    expect(shouldRetry(0, new ApiError(502, 'x'))).toBe(true)
    expect(shouldRetry(1, new ApiError(502, 'x'))).toBe(false)
    expect(shouldRetry(0, new TypeError('network'))).toBe(true)
  })
})

describe('retryDelayFor', () => {
  it('honours retryAfterMs on a 429, clamped, with a little jitter', () => {
    const d = retryDelayFor(0, new ApiError(429, 'x', 'rate_limited', 4200))
    expect(d).toBeGreaterThanOrEqual(4200)
    expect(d).toBeLessThan(4200 + 250)
    expect(retryDelayFor(0, new ApiError(429, 'x', 'rate_limited', 10))).toBeGreaterThanOrEqual(1000)
    expect(retryDelayFor(0, new ApiError(429, 'x', 'rate_limited', 90_000))).toBeLessThan(30_250)
    expect(retryDelayFor(0, new ApiError(429, 'x'))).toBeGreaterThanOrEqual(1000)
  })
  it('falls back to exponential backoff otherwise', () => {
    expect(retryDelayFor(0, new ApiError(502, 'x'))).toBe(1000)
    expect(retryDelayFor(3, new ApiError(502, 'x'))).toBe(8000)
    expect(retryDelayFor(9, new ApiError(502, 'x'))).toBe(30_000)
  })
})

describe('parseRetryAfter', () => {
  it('reads seconds and HTTP dates', () => {
    const now = Date.UTC(2026, 8, 6, 12, 0, 0)
    expect(parseRetryAfter('42')).toBe(42_000)
    expect(parseRetryAfter(' 3 ')).toBe(3000)
    expect(parseRetryAfter(null)).toBeUndefined()
    expect(parseRetryAfter('soon')).toBeUndefined()
    expect(parseRetryAfter(new Date(now + 25_000).toUTCString(), now)).toBe(25_000)
    expect(parseRetryAfter(new Date(now - 25_000).toUTCString(), now)).toBe(0)
  })
})
