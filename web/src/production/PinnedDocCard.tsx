import { faDownload, faTrash } from '@fortawesome/free-solid-svg-icons'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import { useItemDetails } from '../api/queries'
import { thumbnailSrc } from '../api/thumbnails'
import { EntityCard, type CardAction, type CardBadge } from '../components/entitycard/EntityCard'
import { docMeta } from '../components/entitycard/meta'
import { iconForItem } from '../components/icons'
import { useInView } from '../components/useInView'
import { useGoToDocument } from '../state/goto'
import type { Item, ItemKind } from '../api/types'
import type { ProdDoc } from './types'

// Kinds the rest of the app understands (web/src/api/types.ts ItemKind). A pin's
// stored kind is only a hint, and early builds persisted "file" — which is NOT an
// ItemKind, so DetailsPanel's tabsFor() fell through to History-only and the
// Preview tab vanished. Normalising on read repairs those records in place, with
// no data migration.
const ITEM_KINDS: ItemKind[] = [
  'hub',
  'project',
  'folder',
  'design',
  'configured',
  'drawing',
  'schematic',
  'pcb',
  'ecad',
  'unknown',
]

function normalizeKind(kind: string | undefined): ItemKind {
  return ITEM_KINDS.includes(kind as ItemKind) ? (kind as ItemKind) : 'unknown'
}

// PinnedDocCard renders a version-pinned document (a frozen DocSnapshot) as the
// shared EntityCard. What makes it different from a plain document card is that
// it is version-honest: it shows the EXACT version the batch or step recorded —
// its per-version thumbnail, a "v{n}" badge, and (once the row nears the
// viewport) how far behind the lineage tip that pin has fallen.
//
// Thumbnails are version-honest too: only design pins carry a per-version cvId.
// The drawing preview endpoint renders the CURRENT tip, which could silently
// show different geometry than the pinned version — so drawing (and plain file)
// pins fall back to their kind icon rather than risk a wrong picture.
export function PinnedDocCard({
  doc,
  onRemove,
  asRun,
}: {
  doc: ProdDoc
  onRemove?: () => void
  asRun?: boolean
}) {
  const { t } = useTranslation('browse')
  const { t: tp } = useTranslation('production')
  const goTo = useGoToDocument()
  const [inViewRef, inView] = useInView<HTMLSpanElement>()
  const kind = normalizeKind(doc.kind)

  // The tip lookup is one APS call PER CARD, so it waits for the row to near
  // the viewport — the same rule the item rows and thumbnails follow. Until it
  // lands the card shows the pinned version alone, which is never wrong.
  const detailsQ = useItemDetails(inView ? doc.hubId : null, doc.itemId)
  const details = detailsQ.data
  const tipVersion = details?.versionNumber
  const outdated = !!tipVersion && !!doc.versionNumber && tipVersion > doc.versionNumber

  // Version-accurate only: cvId renders the pinned version; no tip fallbacks.
  const thumb = doc.rootComponentVersionId
    ? thumbnailSrc({ kind: 'design', cvId: doc.rootComponentVersionId })
    : null

  const badges: CardBadge[] = [
    { label: t('card.pinnedVersion', { num: doc.versionNumber || '?' }), tone: 'accent' },
    ...(asRun ? [{ label: t('card.asRun'), tone: 'warn' as const, title: t('card.asRunHint') }] : []),
    ...(outdated
      ? [{ label: t('card.currentVersion', { num: tipVersion }), tone: 'muted' as const, title: t('card.outdated') }]
      : []),
  ]

  const canDownload = !!doc.dmProjectId
  const actions: CardAction[] = [
    {
      key: 'download',
      icon: faDownload,
      label: canDownload ? t('card.download') : t('card.downloadUnavailable'),
      href: canDownload ? api.downloadUrl(doc.dmProjectId!, doc.itemId, doc.name) : undefined,
      download: true,
      disabled: !canDownload,
    },
    ...(onRemove
      ? [{ key: 'remove', icon: faTrash, label: t('card.remove'), onClick: onRemove, danger: true }]
      : []),
  ]

  // Ask for Preview explicitly. DetailsPanel validates the request against the
  // kind's available tabs and falls back to available[0], so a design pin still
  // lands on History (designs have no preview tab) — this only makes the intent
  // survive any future reordering of tabsFor().
  const navigate = () => {
    void goTo({ itemId: doc.itemId, name: doc.name, kind }, { tab: 'preview' })
  }

  return (
    <span ref={inViewRef}>
      <EntityCard
        title={doc.name}
        subtitle={tp('pinnedDoc.pinned')}
        thumbUrl={thumb}
        icon={iconForItem({ kind } as Item)}
        badges={badges}
        // The back face describes the PINNED version, not the tip: its date and
        // author must match the v{n} badge on the front.
        meta={docMeta(t, details, doc.versionNumber)}
        metaLoading={detailsQ.isLoading}
        actions={actions}
        onNavigate={navigate}
        selectable
      />
    </span>
  )
}
