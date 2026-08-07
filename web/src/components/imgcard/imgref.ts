// Image refs are the fls:doc / fls:task sibling for chat image attachments:
// a compact, text-safe token addressing an image item uploaded to the
// project's Chat/images/ folder (an ordinary Fusion Team item). The message
// body stays a plain string; the token unfurls to an inline <img> at render
// time, served same-origin by the wiki-asset tip download endpoint.

export interface ImgRef {
  dmProjectId: string // project altId (DM id space — what the byte endpoint takes)
  itemId: string // lineage urn of the uploaded image item
  name: string // stored filename (alt text; display hint)
}

export const IMG_REF_PREFIX = 'fls:img?'

export function encodeImgRef(ref: ImgRef): string {
  const sp = new URLSearchParams()
  sp.set('dmProjectId', ref.dmProjectId)
  sp.set('itemId', ref.itemId)
  sp.set('name', ref.name)
  return IMG_REF_PREFIX + sp.toString()
}

export function parseImgRef(url: string): ImgRef | null {
  if (!url.startsWith(IMG_REF_PREFIX)) return null
  const sp = new URLSearchParams(url.slice(IMG_REF_PREFIX.length))
  const dmProjectId = sp.get('dmProjectId') ?? ''
  const itemId = sp.get('itemId') ?? ''
  if (!dmProjectId || !itemId) return null
  return { dmProjectId, itemId, name: sp.get('name') || 'image' }
}
