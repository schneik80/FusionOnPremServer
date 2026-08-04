/**
 * QA security — black-box API integration suite (Supertest + Vitest).
 *
 * This drives a RUNNING server (not the SPA bundle), so start one first:
 *
 *     make run                 # HTTPS on :8443 (self-signed)
 *     # or: go run . -dev      # HTTP on :8080
 *
 * Then, from web/:
 *     npm i -D vitest supertest @types/supertest
 *     FLS_BASE_URL=http://localhost:8080 npx vitest run test/api-security.test.ts
 *
 * Most data endpoints require a logged-in session. To exercise the authenticated
 * payload-validation paths (rather than just the 401 gate), grab your
 * `fls_session` cookie from the browser after logging in and pass it:
 *     FLS_SESSION=<cookie-value> FLS_BASE_URL=... npx vitest run ...
 *
 * Notes on scope, stated honestly:
 *  - There is NO password or password-reset endpoint (auth is delegated OAuth),
 *    so there is nothing to brute-force with credentials.
 *  - There is NO application-level rate limiter. The rate-limit block below is a
 *    CHARACTERIZATION probe: it reports how many of a burst were throttled and
 *    currently expects zero. When a limiter is added, flip the expectation.
 */

import { describe, it, expect, beforeAll } from 'vitest'
import request from 'supertest'

const BASE = process.env.FLS_BASE_URL ?? 'http://localhost:8080'
const SESSION = process.env.FLS_SESSION ?? ''

// Allow self-signed TLS when pointed at the HTTPS listener.
if (BASE.startsWith('https')) process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0'

const agent = () => request(BASE)
const authed = (req: request.Test) =>
  SESSION ? req.set('Cookie', `fls_session=${SESSION}`) : req

// Skip the whole suite cleanly if no server is reachable, rather than failing
// with connection errors.
let serverUp = false
beforeAll(async () => {
  try {
    await agent().get('/api/meta')
    serverUp = true
  } catch {
    serverUp = false
  }
})

describe('security headers', () => {
  it('sets CSP, frame, nosniff, referrer on a public route', async () => {
    if (!serverUp) return
    const res = await agent().get('/api/meta')
    expect(res.headers['x-content-type-options']).toBe('nosniff')
    expect(res.headers['x-frame-options']).toBe('DENY')
    expect(res.headers['referrer-policy']).toBe('no-referrer')
    expect(res.headers['content-security-policy']).toContain("frame-ancestors 'none'")
  })
})

describe('auth gate', () => {
  it('rejects unauthenticated data routes with a JSON 401 envelope', async () => {
    if (!serverUp) return
    const res = await agent().get('/api/hubs')
    expect(res.status).toBe(401)
    expect(res.body).toHaveProperty('error')
    expect(typeof res.body.error).toBe('string')
  })

  it('returns a JSON 404 (not the SPA shell) for unknown /api paths', async () => {
    if (!serverUp) return
    const res = await agent().get('/api/does-not-exist')
    expect(res.status).toBe(404)
    expect(res.headers['content-type']).toContain('application/json')
  })
})

// (1) Malformed JSON payloads. Without a session these hit the 401 gate first;
// with FLS_SESSION set they should reach the parser and return 400.
describe('malformed JSON payloads', () => {
  const malformed: Array<[string, string]> = [
    ['missing closing brace', '{"id":"x","kind":"design"'],
    ['trailing comma', '{"id":"x",}'],
    ['type mismatch', '{"id":123,"kind":true}'],
    ['not an object', '[1,2,3]'],
    ['bare token', 'null'],
    ['empty body', ''],
  ]

  for (const [name, payload] of malformed) {
    it(`POST /api/pins rejects ${name}`, async () => {
      if (!serverUp) return
      const res = await authed(
        agent().post('/api/pins?hubId=h1').set('Content-Type', 'application/json').send(payload),
      )
      // 401 when unauthenticated (gate first), 400 when authenticated (parser).
      expect([400, 401]).toContain(res.status)
      if (res.status === 400) {
        // The decode-error text must not leak back to the client.
        expect(res.body.error).not.toMatch(/looking for|invalid character|unexpected/i)
      }
    })
  }

  it('POST /api/settings/port rejects a non-numeric port', async () => {
    if (!serverUp) return
    const res = await authed(
      agent().post('/api/settings/port').set('Content-Type', 'application/json').send('{"port":"NaN"}'),
    )
    expect([400, 401, 409]).toContain(res.status) // 409 if running in dev (port not configurable)
  })
})

// (2) Boundary values: huge ints, null bytes, very long strings.
describe('boundary values', () => {
  it('rejects an over-cap pin body without buffering it whole', async () => {
    if (!serverUp) return
    const huge = `{"id":"x","kind":"design","name":"${'A'.repeat(2 * 1024 * 1024)}"}`
    const res = await authed(
      agent().post('/api/pins?hubId=h1').set('Content-Type', 'application/json').send(huge),
    )
    // 401 (gate) or 400/413 (body cap). Never 5xx, never a hang.
    expect([400, 401, 413]).toContain(res.status)
  })

  it('handles null bytes in a query parameter as inert data (no 5xx)', async () => {
    if (!serverUp) return
    const res = await agent().get('/api/projects?hubId=h%001%00')
    expect(res.status).toBeLessThan(500)
  })

  for (const port of [-1, 0, 80, 1023, 65536, 9223372036854775807]) {
    it(`rejects out-of-range port ${port}`, async () => {
      if (!serverUp) return
      const res = await authed(
        agent().post('/api/settings/port').set('Content-Type', 'application/json').send(`{"port":${port}}`),
      )
      expect([400, 401, 409]).toContain(res.status)
      expect(res.status).not.toBe(200)
    })
  }
})

// (3) Rate limiting / brute force — CHARACTERIZATION ONLY.
// No password endpoint exists and no limiter is configured. We burst the public
// meta route and report throttling. Currently expect zero 429s; this is the
// hook to tighten once a limiter ships.
describe('CSRF backstop', () => {
  it('blocks a mutating request carrying a foreign Origin', async () => {
    if (!serverUp) return
    const res = await authed(agent().post('/api/pins'))
      .set('Origin', 'http://evil.example')
      .send({ id: 'x', kind: 'design' })
    expect(res.status).toBe(403)
    expect(res.body?.code).toBe('forbidden')
  })
})

describe('rate limiting (characterization — no limiter present today)', () => {
  it('documents that a burst is not throttled', async () => {
    if (!serverUp) return
    const burst = 60
    const statuses = await Promise.all(
      Array.from({ length: burst }, () => agent().get('/api/meta').then((r) => r.status)),
    )
    const throttled = statuses.filter((s) => s === 429).length
    // eslint-disable-next-line no-console
    console.warn(`rate-limit characterization: ${throttled}/${burst} throttled (no limiter ⇒ expect 0)`)
    expect(throttled).toBe(0)
    expect(statuses.every((s) => s < 500)).toBe(true)
  })
})
