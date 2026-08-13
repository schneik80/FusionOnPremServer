import { faTrash } from '@fortawesome/free-solid-svg-icons'
import { useTranslation } from 'react-i18next'
import { useCachedSubtype, useItemDetails } from '../api/queries'
import { thumbnailSrc } from '../api/thumbnails'
import { FUSION_NATIVE_KINDS, kindFromTypename } from '../api/types'
import { useDocActions } from '../components/doccard/docActions'
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

  // The tip lookup is one APS call PER CARD, so it waits for the row to near
  // the viewport — the same rule the item rows and thumbnails follow. Until it
  // lands the card shows the pinned version alone, which is never wrong.
  const detailsQ = useItemDetails(inView ? doc.hubId : null, doc.itemId)
  const details = detailsQ.data
  const tipVersion = details?.versionNumber
  const outdated = !!tipVersion && !!doc.versionNumber && tipVersion > doc.versionNumber

  // The GraphQL typename is the kind; the snapshot's own field is only a hint.
  // It has to be, because a pin supplied by the hub browser was kinded from the
  // file EXTENSION (api/browse.go) and a Fusion Team design has none — so every
  // design attached that way is stored as "unknown". Trusting that hint sent a
  // click from a batch to the two-tab uploaded-file view instead of the design's
  // own history, BOM and references, and hid Open, Insert and Archive with it.
  const kind = normalizeKind(kindFromTypename(details?.typename) ?? doc.kind)
  const kindPending = !details && detailsQ.isLoading

  // Version-accurate only: cvId renders the pinned version; no tip fallbacks.
  const thumb = doc.rootComponentVersionId
    ? thumbnailSrc({ kind: 'design', cvId: doc.rootComponentVersionId })
    : null

  // Assembly-or-part for the icon, but only from an answer someone already
  // paid APS for. A pinned version is rarely the one a browse row classified,
  // so this usually stays "" — and the part mark is the right thing to draw
  // when the shape of the design is unknown.
  const subtype = useCachedSubtype(doc.rootComponentVersionId)

  const badges: CardBadge[] = [
    { label: t('card.pinnedVersion', { num: doc.versionNumber || '?' }), tone: 'accent' },
    ...(asRun ? [{ label: t('card.asRun'), tone: 'warn' as const, title: t('card.asRunHint') }] : []),
    ...(outdated
      ? [{ label: t('card.currentVersion', { num: tipVersion }), tone: 'muted' as const, title: t('card.outdated') }]
      : []),
  ]

  // Open / Insert / Archive, or a plain download for an uploaded file — the
  // same set every document card offers. The archive is pinned to the version
  // this card shows: the badge says v{n}, so the file it hands back has to be
  // v{n} and not whatever the lineage tip has become since.
  //
  // Open and Insert cannot be pinned that way — Fusion opens a lineage, and
  // opens it at its tip. That is Fusion's own behaviour, not something this
  // card can qualify, so it is left alone rather than dressed up.
  const docActions = useDocActions({
    itemId: doc.itemId,
    name: doc.name,
    kind,
    kindPending,
    dmProjectId: doc.dmProjectId,
    versionId: doc.versionId,
  })
  const actions: CardAction[] = [
    ...docActions,
    ...(onRemove
      ? [{ key: 'remove', icon: faTrash, label: t('card.remove'), onClick: onRemove, danger: true }]
      : []),
  ]

  // Ask for Preview only where a Preview exists. DetailsPanel validates the
  // request against the kind's available tabs, so a native design lands on its
  // own default (History) rather than being pointed at a tab it does not have.
  //
  // No componentVersionId: navigating means "take me to this document", and the
  // panel is a view of the lineage, not of a pin. Handing it the pinned cvId
  // would draw this version's thumbnail, properties and BOM under the tip's
  // version number — a mix of two versions in one panel.
  const navigate = () => {
    void goTo(
      { itemId: doc.itemId, name: doc.name, kind },
      { tab: FUSION_NATIVE_KINDS.has(kind) ? undefined : 'preview' },
    )
  }

  return (
    <span ref={inViewRef}>
      <EntityCard
        title={doc.name}
        subtitle={tp('pinnedDoc.pinned')}
        thumbUrl={thumb}
        icon={iconForItem({ kind, subtype } as Item)}
        iconItem={{ kind, subtype }}
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
