import { faArrowUpRightFromSquare, faFileImage } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, Tooltip, Typography } from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../api/client'
import { useItemDetails, useItemLocation } from '../../api/queries'
import { thumbnailSrc } from '../../api/thumbnails'
import { useGoToDocument } from '../../state/goto'
import { useNav } from '../../state/nav'
import { iconForItem } from '../icons'
import { viewerKindFor } from '../viewers/kind'
import type { DocRef } from './docref'

// DocumentCard is the unfurled form of a DocRef (see docref.ts): a compact,
// link-preview-style card with a thumbnail, the document name, its location
// (project › folder trail), and a hover-revealed jump affordance — clicking
// navigates the main browser to the document. It hydrates itself from the
// details + location queries (both shared with the rest of the app, so cards
// piggyback on warm caches), falling back to the names captured in the token
// while loading.
//
// Built entirely from span elements so it is valid inside a <p> — markdown
// paragraphs and chat text bodies are its two homes.
export function DocumentCard({ docRef }: { docRef: DocRef }) {
  const { t } = useTranslation('browse')
  const nav = useNav()
  // Hub isolation: the session (and every data route) is locked to nav's
  // hub, so a token minted in a DIFFERENT hub must not fetch — the server
  // would only 403 (hub_mismatch) — and can't be opened from here. It renders
  // as a muted, inert card built from the names captured in the token.
  const sameHub = nav.hubId !== null && docRef.hubId === nav.hubId
  const otherHub = nav.hubId !== null && !sameHub
  const detailsQ = useItemDetails(sameHub ? docRef.hubId : null, docRef.itemId)
  const locationQ = useItemLocation(sameHub ? docRef.hubId : null, docRef.itemId, sameHub)
  const goTo = useGoToDocument()
  const [thumbFailed, setThumbFailed] = useState(false)

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

  function open() {
    if (otherHub) return
    void goTo({
      itemId: docRef.itemId,
      name,
      kind,
      componentVersionId: details?.rootComponentVersionId,
    })
  }

  const card = (
      <Box
        component="span"
        role={otherHub ? undefined : 'button'}
        tabIndex={otherHub ? undefined : 0}
        onClick={otherHub ? undefined : open}
        onKeyDown={
          otherHub
            ? undefined
            : (e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  open()
                }
              }
        }
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 1.25,
          border: 1,
          borderColor: 'divider',
          borderRadius: 1,
          bgcolor: 'background.paper',
          px: 1,
          py: 0.75,
          my: 0.25,
          // Shrink with the container (thread drawer, narrow panes): the cap
          // is the container width, and the text column ellipsizes.
          maxWidth: 'min(420px, 100%)',
          cursor: otherHub ? 'default' : 'pointer',
          opacity: otherHub ? 0.55 : 1,
          verticalAlign: 'middle',
          // A card is a control, not text — clicking it must not smear a text
          // -selection highlight across the surrounding message.
          userSelect: 'none',
          transition: 'border-color 120ms',
          // Hover keeps the paper background (the message row underneath
          // paints its own hover wash; changing ours too reads as a smeared
          // double highlight) — the border and jump icon carry the affordance.
          ...(otherHub
            ? {}
            : {
                '&:hover, &:focus-visible': {
                  borderColor: 'primary.main',
                  '& .doccard-go': { opacity: 1 },
                },
              }),
        }}
      >
        <Box
          component="span"
          sx={{
            width: 40,
            height: 40,
            flexShrink: 0,
            borderRadius: 0.5,
            bgcolor: 'action.hover',
            color: 'text.secondary',
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            overflow: 'hidden',
          }}
        >
          {thumb && !thumbFailed ? (
            <Box
              component="img"
              src={thumb}
              alt=""
              // The card is a control: the browser must not start a native
              // image drag from it. That drag advertises the image as a file
              // (Chrome puts 'Files' in dataTransfer.types), which reads as an
              // incoming upload to the app-wide drop handler, and it fights
              // whatever the card is embedded in — moving a card on a
              // whiteboard, panning a graph.
              draggable={false}
              onError={() => setThumbFailed(true)}
              sx={{ width: '100%', height: '100%', objectFit: 'contain', display: 'block' }}
            />
          ) : (
            <FontAwesomeIcon
              icon={isImageFile ? faFileImage : iconForItem({ kind, subtype: '' })}
              style={{ fontSize: 17 }}
            />
          )}
        </Box>
        <Box
          component="span"
          sx={{ display: 'inline-flex', flexDirection: 'column', minWidth: 0 }}
        >
          <Typography component="span" variant="subtitle2" noWrap sx={{ lineHeight: 1.3 }}>
            {name}
          </Typography>
          <Typography
            component="span"
            variant="caption"
            color="text.secondary"
            noWrap
            title={loc ? location : undefined}
          >
            {location}
          </Typography>
        </Box>
        {!otherHub && (
          <Box
            component="span"
            className="doccard-go"
            sx={{
              ml: 0.5,
              color: 'primary.main',
              opacity: 0,
              transition: 'opacity 120ms',
              flexShrink: 0,
              display: 'inline-flex',
            }}
          >
            <FontAwesomeIcon icon={faArrowUpRightFromSquare} style={{ fontSize: 12 }} />
          </Box>
        )}
      </Box>
  )

  // No tooltip on the inert other-hub card — there is nothing to open.
  return otherHub ? card : <Tooltip title={t('docCard.open')}>{card}</Tooltip>
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
