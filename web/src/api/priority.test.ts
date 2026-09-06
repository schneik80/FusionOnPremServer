import { describe, expect, it } from 'vitest'
import { PRIORITY_HEADER, withPriority } from './priority'

describe('withPriority', () => {
  it('adds the header and keeps an explicit Content-Type', () => {
    const init = withPriority({ headers: { 'Content-Type': 'application/json' }, method: 'POST' }, 0)
    const h = init.headers as Headers
    expect(h.get(PRIORITY_HEADER)).toBe('0')
    expect(h.get('Content-Type')).toBe('application/json')
    expect(init.method).toBe('POST')
  })
  it('works with no init and with undefined headers (FormData case)', () => {
    expect((withPriority(undefined, 2).headers as Headers).get(PRIORITY_HEADER)).toBe('2')
    const init = withPriority({ headers: undefined, body: 'x' }, 1)
    expect((init.headers as Headers).get('Content-Type')).toBeNull()
    expect((init.headers as Headers).get(PRIORITY_HEADER)).toBe('1')
  })
  it('overrides a stale header', () => {
    const init = withPriority({ headers: { [PRIORITY_HEADER]: '2' } }, 0)
    expect((init.headers as Headers).get(PRIORITY_HEADER)).toBe('0')
  })
})
