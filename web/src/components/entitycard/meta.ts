import type { TFunction } from 'i18next'
import type { Details } from '../../api/types'
import { fmtDate } from '../../fmt'
import type { CardMeta } from './EntityCard'

// Back-face rows for a document card, built from the shared details query.
// Rows with no value are dropped rather than shown empty — a part number is a
// Fusion property that most documents simply don't carry.
//
// versionNumber pins the rows to ONE version: a production card shows the
// version its batch recorded, not the lineage tip, so the date and author on
// the back match the version badge on the front. The per-version record comes
// from details.versions (already fetched with the details), and when that
// version has aged out of the returned window the tip values are used instead.
export function docMeta(
  t: TFunction<'browse'>,
  details: Details | undefined,
  versionNumber?: number,
): CardMeta[] {
  if (!details) return []
  const pinned =
    versionNumber != null ? details.versions.find((v) => v.number === versionNumber) : undefined
  const shownVersion = versionNumber ?? details.versionNumber
  const changedOn = pinned?.createdOn ?? details.modifiedOn
  const changedBy = pinned?.createdBy ?? details.modifiedBy

  const rows: (CardMeta | null)[] = [
    details.partNumber ? { label: t('card.partNumber'), value: details.partNumber } : null,
    details.partDesc ? { label: t('card.description'), value: details.partDesc } : null,
    shownVersion ? { label: t('card.version'), value: `v${shownVersion}` } : null,
    changedOn ? { label: t('card.modified'), value: fmtDate(changedOn, { dateStyle: 'medium', timeStyle: 'short' }) } : null,
    changedBy ? { label: t('card.modifiedBy'), value: changedBy } : null,
    details.material ? { label: t('card.material'), value: details.material } : null,
    details.size ? { label: t('card.size'), value: details.size } : null,
  ]
  return rows.filter(Boolean) as CardMeta[]
}
