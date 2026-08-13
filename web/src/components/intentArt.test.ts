import { describe, expect, it } from 'vitest'
import { INTENT_ART_BREAK, artKey, intentForSubtype, pickIntentArt } from './intentArt'

describe('intentForSubtype', () => {
  it('reads the classify subtype', () => {
    expect(intentForSubtype('assembly')).toBe('assembly')
    expect(intentForSubtype('part')).toBe('part')
  })

  it('falls back to part for a design that has not classified yet', () => {
    // Item.subtype arrives empty from the listing API and is refined later by a
    // viewport-gated classify call, so "" is the common case on first paint.
    expect(intentForSubtype('')).toBe('part')
    expect(intentForSubtype(undefined)).toBe('part')
    expect(intentForSubtype('dwg')).toBe('part')
  })

  it('never returns hybrid', () => {
    // MDM gives us no root-body signal, so hybrid is undrawable today. When a
    // signal appears, this assertion is the thing that has to change.
    const inputs = ['', 'part', 'assembly', 'hybrid', 'dwg', 'template', undefined]
    for (const s of inputs) expect(intentForSubtype(s), String(s)).not.toBe('hybrid')
  })
})

describe('pickIntentArt', () => {
  it('takes the 16 px drawing below the break and the 32 px one at or above it', () => {
    const at = (px: number) => pickIntentArt({ subtype: 'part', mode: 'dark', px }).size
    expect(at(11)).toBe(16) // breadcrumb bar
    expect(at(15)).toBe(16) // browse row
    expect(at(INTENT_ART_BREAK - 1)).toBe(16)
    expect(at(INTENT_ART_BREAK)).toBe(32)
    expect(at(24)).toBe(32) // EntityCard thumbnail tile
  })

  it('passes the theme mode straight through', () => {
    expect(pickIntentArt({ mode: 'light', px: 15 }).theme).toBe('light')
    expect(pickIntentArt({ mode: 'dark', px: 15 }).theme).toBe('dark')
  })

  it('names the drawing after its source file', () => {
    expect(artKey(pickIntentArt({ subtype: 'assembly', mode: 'light', px: 24 }))).toBe(
      'assembly-32-light',
    )
    expect(artKey(pickIntentArt({ subtype: '', mode: 'dark', px: 15 }))).toBe('part-16-dark')
  })
})
