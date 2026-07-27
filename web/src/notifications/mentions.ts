// @mention tokens are the inline sibling of the fls:doc / fls:task card
// tokens (see components/reftokens.ts): a compact, text-safe pseudo-URL the
// chat composer inserts when you pick a member, stored verbatim in the
// plain-text body. The renderer swaps it for a highlighted "@Name" at display
// time, and the SERVER parses the same token to address the mention
// notification — so a mention costs no APS roster call at either end.
//
// Unlike the card tokens, a mention renders as inline text (a highlighted
// span), not a bordered card, so it lives here rather than in RefCard.

export interface MentionRef {
  userId: string
  name: string
}

export const MENTION_PREFIX = 'fls:user?'

// The character class is char-for-char the one every other fls: token uses
// (reftokens.ts), so trailing punctuation never sticks to a token and both
// the client and the Go parser split identically.
const MENTION_RE = /fls:user\?[A-Za-z0-9*\-._%&=+]+/g

export function encodeMention(m: MentionRef): string {
  const sp = new URLSearchParams()
  sp.set('id', m.userId)
  sp.set('name', m.name)
  return MENTION_PREFIX + sp.toString()
}

export function parseMention(token: string): MentionRef | null {
  if (!token.startsWith(MENTION_PREFIX)) return null
  const sp = new URLSearchParams(token.slice(MENTION_PREFIX.length))
  const userId = sp.get('id') ?? ''
  if (!userId) return null
  return { userId, name: sp.get('name') || '' }
}

export type MentionPart = { text: string } | { mention: MentionRef }

// splitMentions breaks a run of text into plain runs and mention tokens.
// Malformed tokens (no id) stay text. Used on the text parts that survive
// splitRefTokens, so cards and mentions compose without either eating the
// other's tokens.
export function splitMentions(text: string): MentionPart[] {
  const parts: MentionPart[] = []
  let last = 0
  for (const m of text.matchAll(MENTION_RE)) {
    const mention = parseMention(m[0])
    if (!mention) continue // malformed: leave as text
    if (m.index! > last) parts.push({ text: text.slice(last, m.index) })
    parts.push({ mention })
    last = m.index! + m[0].length
  }
  if (last < text.length) parts.push({ text: text.slice(last) })
  return parts.length ? parts : [{ text }]
}
