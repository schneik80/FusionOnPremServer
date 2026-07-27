// Localized labels for server-originated enum tokens (status/priority/kind/
// role values that live in data as stable snake/upper tokens and must never
// be rendered raw). Unknown values fall back to the raw token so new
// server-side values degrade readably instead of blanking.

import type { TFunction } from 'i18next'

function lookup(t: TFunction, key: string, raw: string): string {
  const full = `enums:${key}.${raw}`
  const out = t(full)
  return out === `${key}.${raw}` || out === full ? raw : out
}

export const taskStatusLabel = (t: TFunction, s: string) => lookup(t, 'taskStatus', s)
export const taskPriorityLabel = (t: TFunction, p: string) => lookup(t, 'taskPriority', p)
export const batchKindLabel = (t: TFunction, k: string) => lookup(t, 'batchKind', k)
export const batchStatusLabel = (t: TFunction, s: string) => lookup(t, 'batchStatus', s)
export const roleLabel = (t: TFunction, r: string) => lookup(t, 'role', r.toUpperCase())

// localRefKindLabel names a local where-used source ("task", "chat", …) and
// localRefViaLabel how a production reference was made ("step",
// "fulfillment", "reference") — both server tokens, both rendered here.
export const localRefKindLabel = (t: TFunction, k: string) => lookup(t, 'localRefKind', k)
export const localRefViaLabel = (t: TFunction, v: string) => lookup(t, 'localRefVia', v)

// typeTagLabel localizes the short item-kind tag ("asm", "dwg", …). Most
// locales keep the English abbreviations; the indirection exists so any
// locale that wants native tags can supply them.
export const typeTagLabel = (t: TFunction, tag: string) =>
  tag ? lookup(t, 'typeTag', tag) : ''

// docStateLabel localizes a document lifecycle badge (api/documentState.ts).
export function docStateLabel(
  t: TFunction,
  s: { kind: 'wip' } | { kind: 'version' } | { kind: 'released'; revision: string },
): string {
  if (s.kind === 'released') return t('enums:docState.released', { revision: s.revision })
  return lookup(t, 'docState', s.kind)
}
