import { describe, expect, it } from 'vitest'
import { thumbnailSrc } from './thumbnails'

describe('thumbnailSrc', () => {
  it('carries the priority as ?p=, defaulting to 1', () => {
    expect(thumbnailSrc({ kind: 'design', cvId: 'urn:a:b/c' })).toBe('/api/items/thumbnail/image?cvId=urn%3Aa%3Ab%2Fc&p=1')
    expect(thumbnailSrc({ kind: 'design', cvId: 'x', priority: 0 })).toBe('/api/items/thumbnail/image?cvId=x&p=0')
    expect(thumbnailSrc({ kind: 'drawing', itemId: 'i', projectAltId: 'b.p', priority: 2 })).toBe(
      '/api/items/drawing/preview?itemId=i&dmProjectId=b.p&p=2',
    )
  })
  it('keeps the null cases', () => {
    expect(thumbnailSrc({ kind: 'design' })).toBeNull()
    expect(thumbnailSrc({ kind: 'drawing', itemId: 'i' })).toBeNull()
    expect(thumbnailSrc({ kind: 'file', cvId: 'x' })).toBe('/api/items/thumbnail/image?cvId=x&p=1')
  })
})
