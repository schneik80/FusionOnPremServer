import { faDownload, faFileImage } from '@fortawesome/free-solid-svg-icons'
import { useTranslation } from 'react-i18next'
import { api } from '../../api/client'
import { useItemDetails, useItemLocation } from '../../api/queries'
import { thumbnailSrc } from '../../api/thumbnails'
import { useGoToDocument } from '../../state/goto'
import { useNav } from '../../state/nav'
import { EntityCard, type CardAction } from '../entitycard/EntityCard'
import { docMeta } from '../entitycard/meta'
import { iconForItem } from '../icons'
import { viewerKindFor } from '../viewers/kind'
import type { DocRef } from './docref'

// DocumentCard is the unfurled form of a DocRef (see docref.ts): the shared
// EntityCard, fed from the details + location queries (both shared with the
// rest of the app, so cards piggyback on warm caches) and falling back to the
// names captured in the token while loading.
//
// Click opens the document — an inline card in a sentence is a link preview,
// so it behaves like a link. Its details/download live on the back face and
// action bar, which the card only reveals once engaged.
export function DocumentCard({ docRef }: { docRef: DocRef }) {
  const { t } = useTranslation('browse')
  const nav = useNav()
  // Hub isolation: the session (and every data route) is locked to nav's hub,
  // so a token minted in a DIFFERENT hub must not fetch — the server would only
  // 403 (hub_mismatch) — and can't be opened from here. It renders as a muted,
  // inert card built from the names captured in the token.
  const sameHub = nav.hubId !== null && docRef.hubId === nav.hubId
  const otherHub = nav.hubId !== null && !sameHub
  const detailsQ = useItemDetails(sameHub ? docRef.hubId : null, docRef.itemId)
  const locationQ = useItemLocation(sameHub ? docRef.hubId : null, docRef.itemId, sameHub)
  const goTo = useGoToDocument()

  const details = detailsQ.data
  const loc = locationQ.data
  const name = details?.name ?? docRef.name
  // The token's kind is an insert-time hint (DM listings can't tell a design
  // from a plain file without an extension); the GraphQL typename is truth.
  const kind = kindFromTypename(details?.typename) ?? docRef.kind

  // Thumbnail: design/drawing preview when there is one; image files render
  // their own bytes; everything else falls back to the kind icon.
  let thumb = thumbnailSrc({
    kind,
    cvId: details?.rootComponentVersionId,
    itemId: docRef.itemId,
    projectAltId: loc?.projectAltId,
  })
  const isImageFile = viewerKindFor(name, details?.mimeType) === 'image'
  if (!thumb && isImageFile && loc?.projectAltId) {
    thumb = api.fileUrl(loc.projectAltId, docRef.itemId, name)
  }

  const location = otherHub
    ? t('docCard.otherHub')
    : loc
      ? [loc.projectName, ...loc.folderPath.map((f) => f.name)].join(' › ')
      : locationQ.isLoading
        ? t('docCard.locating')
        : t('docCard.locationUnavailable')

  // Only uploaded files have bytes to hand back. A native Fusion design is
  // stored as a lineage the browser can't just save, so the action is offered
  // disabled with a reason rather than silently missing.
  const altId = loc?.projectAltId
  const canDownload = !otherHub && !!altId
  const actions: CardAction[] = [
    {
      key: 'download',
      icon: faDownload,
      label: canDownload ? t('card.download') : t('card.downloadUnavailable'),
      href: altId ? api.downloadUrl(altId, docRef.itemId, name) : undefined,
      download: true,
      disabled: !canDownload,
    },
  ]

  return (
    <EntityCard
      title={name}
      subtitle={location}
      thumbUrl={thumb}
      icon={isImageFile ? faFileImage : iconForItem({ kind, subtype: '' })}
      meta={docMeta(t, details)}
      metaLoading={detailsQ.isLoading}
      actions={otherHub ? undefined : actions}
      onNavigate={
        otherHub
          ? undefined
          : () =>
              void goTo({
                itemId: docRef.itemId,
                name,
                kind,
                componentVersionId: details?.rootComponentVersionId,
              })
      }
      dimmed={otherHub}
      selectable
    />
  )
}

// kindFromTypename maps the details query's GraphQL typename onto the app's
// Item.kind vocabulary; null when details haven't loaded (caller falls back
// to the token's hint).
function kindFromTypename(typename?: string): string | null {
  switch (typename) {
    case 'DesignItem':
      return 'design'
    case 'DrawingItem':
      return 'drawing'
    case 'ConfiguredDesignItem':
      return 'configured'
    case 'BasicItem':
      return 'unknown'
    default:
      return null
  }
}
