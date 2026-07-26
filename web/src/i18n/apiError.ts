import type { TFunction } from 'i18next'
import { ApiError } from '../api/client'

// localizeApiError renders an error for display. Category-level codes
// (unauthorized, rate_limited, …) map to the errors catalog; codes absent
// from the catalog — deliberately including invalid_request and not_found —
// fall back to the server's specific English message, which carries detail
// ("end date is before start date") a generic sentence would flatten.
export function localizeApiError(t: TFunction, err: unknown): string {
  if (err instanceof ApiError && err.code) {
    const key = `errors:${err.code}`
    const out = t(key)
    if (out !== key && out !== err.code) return out
  }
  return err instanceof Error ? err.message : String(err)
}
