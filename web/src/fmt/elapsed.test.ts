import { afterEach, describe, expect, it } from 'vitest'
import i18n from '../i18n'
import { fmtDuration } from './index'

// These assertions are deliberately loose — toContain, not toBe. ICU's exact
// output (serial comma, spacing, non-breaking spaces) varies between engine
// versions, and pinning it would make this file fail on a Node upgrade without
// anything actually being wrong. What matters is that the numbers and the
// localized unit words are present, and that they follow the app language.

afterEach(async () => {
  await i18n.changeLanguage('en')
})

describe('fmtDuration', () => {
  it('renders a single unit', () => {
    expect(fmtDuration({ years: 0, months: 0, days: 1 })).toContain('1 day')
    expect(fmtDuration({ years: 0, months: 3, days: 0 })).toContain('3 months')
    expect(fmtDuration({ years: 2, months: 0, days: 0 })).toContain('2 years')
  })

  it('joins two units', () => {
    const s = fmtDuration({ years: 0, months: 3, days: 2 })
    expect(s).toContain('3 months')
    expect(s).toContain('2 days')
    expect(s).toContain('and')
  })

  it('joins three units', () => {
    const s = fmtDuration({ years: 1, months: 2, days: 3 })
    expect(s).toContain('1 year')
    expect(s).toContain('2 months')
    expect(s).toContain('3 days')
    expect(s).toContain('and')
  })

  it('prefers weeks below a month', () => {
    expect(fmtDuration({ years: 0, months: 0, days: 7 })).toContain('1 week')
    expect(fmtDuration({ years: 0, months: 0, days: 14 })).toContain('2 weeks')
    const s = fmtDuration({ years: 0, months: 0, days: 10 })
    expect(s).toContain('1 week')
    expect(s).toContain('3 days')
  })

  it('keeps days as days once a month or year is present', () => {
    const s = fmtDuration({ years: 0, months: 1, days: 10 })
    expect(s).toContain('1 month')
    expect(s).toContain('10 days')
    expect(s).not.toContain('week')
  })

  it('is empty for a zero span', () => {
    expect(fmtDuration({ years: 0, months: 0, days: 0 })).toBe('')
  })

  it('follows the app language, not the browser', async () => {
    await i18n.changeLanguage('de')
    const s = fmtDuration({ years: 1, months: 2, days: 3 })
    expect(s).toMatch(/Jahr/)
    expect(s).toMatch(/Monate/)
    expect(s).toMatch(/Tage/)
    expect(s).toMatch(/\bund\b/)
  })

  it('degrades to a comma join without Intl.ListFormat', () => {
    // Intl.ListFormat is a read-only property in the lib types, so swap it
    // through the object rather than by assignment.
    const holder = Intl as unknown as Record<string, unknown>
    const real = holder.ListFormat
    delete holder.ListFormat
    try {
      const s = fmtDuration({ years: 0, months: 3, days: 2 })
      expect(s).toContain('3 months')
      expect(s).toContain('2 days')
      expect(s).toContain(',')
    } finally {
      holder.ListFormat = real
    }
  })
})
