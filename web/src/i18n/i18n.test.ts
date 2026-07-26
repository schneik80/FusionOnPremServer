import { describe, expect, it } from 'vitest'
import i18n, { NAMESPACES } from './index'
import { fmtBytes, getDateTimeFmt } from '../fmt'

describe('i18n init', () => {
  it('initializes with English resources and semantic keys', () => {
    expect(i18n.isInitialized).toBe(true)
    expect(NAMESPACES).toEqual(
      expect.arrayContaining(['common', 'nav', 'settings', 'errors', 'enums']),
    )
    expect(i18n.t('settings:theme.label')).toBe('Theme')
    expect(i18n.t('nav:settings')).toBe('Settings')
  })

  it('interpolates named variables', () => {
    expect(i18n.t('settings:port.rangeHelp', { min: 1024, max: 65535 })).toContain('1024')
  })

  it('falls back to English for missing keys in another language', async () => {
    i18n.addResourceBundle('de', 'settings', { theme: { label: 'Design' } }, true, true)
    await i18n.changeLanguage('de')
    expect(i18n.t('settings:theme.label')).toBe('Design')
    // Key absent from the de bundle → English fallback, never the raw key.
    expect(i18n.t('settings:port.label')).toBe('Server port')
    await i18n.changeLanguage('en')
  })

  it('missing keys fall back to English rather than leaking key names', () => {
    expect(i18n.t('settings:theme.light')).toBe('Light')
    expect(i18n.t('nav:pins')).toBe('Pins')
  })
})

describe('fmt', () => {
  it('date format cache follows the active language', async () => {
    await i18n.changeLanguage('en')
    const en = getDateTimeFmt({ month: 'long' }).format(new Date(2026, 0, 15))
    expect(en).toBe('January')
    await i18n.changeLanguage('de')
    const de = getDateTimeFmt({ month: 'long' }).format(new Date(2026, 0, 15))
    expect(de).toBe('Januar')
    await i18n.changeLanguage('en')
  })

  it('fmtBytes scales units', () => {
    expect(fmtBytes(0)).toBe('0 B')
    expect(fmtBytes(2048)).toBe('2 KB')
    expect(fmtBytes(5 * 1024 * 1024)).toBe('5 MB')
    expect(fmtBytes(-1)).toBe('')
  })
})
