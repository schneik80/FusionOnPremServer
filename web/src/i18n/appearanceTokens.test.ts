import { describe, expect, it } from 'vitest'
import en from './locales/en/settings.json'
import { tokens } from '../theme'

// Settings → Appearance builds its swatch list from the theme's token bag plus
// the accents, and labels each row with t(`appearance.token.${key}`). A token
// added to the theme without a matching catalog entry therefore renders its own
// key as the label — which is exactly how `accentWarn` shipped, since a missing
// key is invisible to the other catalog checks (they only forbid EXTRA keys and
// empty values, and the eslint ratchet only forbids literal strings).
//
// ACCENT_KEYS mirrors the extra rows AppearanceTool appends after the bag; the
// bag itself is read from the theme so a new mode token is covered the moment
// it exists.
const ACCENT_KEYS = ['accent', 'accentAlt', 'accentWarn']

describe('appearance token labels', () => {
  const labels = en.appearance.token as Record<string, string>

  it('labels every theme token', () => {
    for (const key of Object.keys(tokens.dark)) {
      expect(labels[key], `appearance.token.${key} is missing from en/settings.json`).toBeTruthy()
    }
  })

  it('labels every accent', () => {
    for (const key of ACCENT_KEYS) {
      expect(labels[key], `appearance.token.${key} is missing from en/settings.json`).toBeTruthy()
    }
  })

  it('has no label without a token behind it', () => {
    const known = new Set([...Object.keys(tokens.dark), ...ACCENT_KEYS])
    for (const key of Object.keys(labels)) {
      expect(known.has(key), `appearance.token.${key} labels a token that no longer exists`).toBe(true)
    }
  })
})
