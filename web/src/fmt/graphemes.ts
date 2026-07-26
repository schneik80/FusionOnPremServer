// Grapheme-aware string helpers. JS string indexing/slicing operates on
// UTF-16 code units, so `s[0]` / `s.slice(0, n)` can split an emoji or any
// astral character into a lone surrogate (renders as �). Route all
// user-visible truncation and initial-extraction through here.

const seg =
  typeof Intl !== 'undefined' && 'Segmenter' in Intl
    ? new Intl.Segmenter(undefined, { granularity: 'grapheme' })
    : null

// graphemes splits a string into user-perceived characters.
export function graphemes(s: string): string[] {
  if (seg) return [...seg.segment(s)].map((x) => x.segment)
  // Fallback: code points (handles surrogates, not combining sequences).
  return Array.from(s)
}

// firstGrapheme returns the first user-perceived character ('' when empty).
// Use for avatar initials — never s[0] or s.slice(0, 1).
export function firstGrapheme(s: string): string {
  if (!s) return ''
  if (seg) {
    for (const x of seg.segment(s)) return x.segment
    return ''
  }
  return Array.from(s)[0] ?? ''
}

// truncateGraphemes clips to n user-perceived characters, appending an
// ellipsis when clipped.
export function truncateGraphemes(s: string, n: number): string {
  const g = graphemes(s)
  if (g.length <= n) return s
  return g.slice(0, Math.max(0, n - 1)).join('') + '…'
}

// foldSearch normalizes for accent/width-insensitive substring search:
// NFKD (splits accents from letters, folds full-width forms), strips
// combining marks, lowercases. Fold BOTH the query and the haystack.
export function foldSearch(s: string): string {
  return s
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
}
