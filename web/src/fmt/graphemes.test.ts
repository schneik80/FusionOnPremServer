import { describe, expect, it } from 'vitest'
import { firstGrapheme, foldSearch, graphemes, truncateGraphemes } from './graphemes'

describe('graphemes', () => {
  it('splits astral characters whole', () => {
    expect(graphemes('a😀b')).toEqual(['a', '😀', 'b'])
  })
  it('keeps combining sequences together (Segmenter)', () => {
    // é as e + combining acute
    const s = 'éx'
    const g = graphemes(s)
    expect(g[g.length - 1]).toBe('x')
    expect(g.length).toBe(2)
  })
})

describe('firstGrapheme', () => {
  it('never returns a lone surrogate', () => {
    expect(firstGrapheme('😀name')).toBe('😀')
    expect(firstGrapheme('田中')).toBe('田')
    expect(firstGrapheme('')).toBe('')
  })
})

describe('truncateGraphemes', () => {
  it('clips by user-perceived characters with ellipsis', () => {
    expect(truncateGraphemes('hello', 10)).toBe('hello')
    expect(truncateGraphemes('hello world', 6)).toBe('hello…')
    expect(truncateGraphemes('😀😀😀😀', 3)).toBe('😀😀…')
  })
})

describe('foldSearch', () => {
  it('is accent-insensitive', () => {
    expect(foldSearch('Décollage')).toBe('decollage')
    expect(foldSearch('MÜNCHEN')).toBe('munchen')
  })
  it('folds full-width forms (CJK-adjacent widths)', () => {
    expect(foldSearch('ＡＢＣ')).toBe('abc')
  })
  it('leaves CJK intact for exact matching', () => {
    expect(foldSearch('部品')).toBe('部品')
  })
})
