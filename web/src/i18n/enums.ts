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
