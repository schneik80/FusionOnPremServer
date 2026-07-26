import { faUpRightFromSquare, faXmark } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, IconButton, Paper, Tooltip, Typography } from '@mui/material'
import { alpha } from '@mui/material/styles'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { thumbnailSrc } from '../api/thumbnails'
import { iconForItem } from '../components/icons'
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

// PinnedDocChip renders a version-pinned document (a frozen DocSnapshot). Unlike
// the shared DocumentCard, which always follows the tip, this shows the EXACT
// version stored: its per-version thumbnail (via rootComponentVersionId) and a
// "v{n}" badge, so a batch always displays the version it recorded. Clicking
// the chip jumps the main browser to the document (the shared useGoToDocument
// flow, same as the relationship graphs).
//
// Thumbnails are version-honest: only design pins carry a per-version cvId.
// The drawing preview endpoint renders the CURRENT tip, which could silently
// show different geometry than the pinned version — so drawing (and plain
// file) pins fall back to their kind icon rather than risk a wrong picture.
export function PinnedDocChip({
  doc,
  onRemove,
  asRun,
}: {
  doc: ProdDoc
  onRemove?: () => void
  asRun?: boolean
}) {
  const { t } = useTranslation('production')
  const [imgFailed, setImgFailed] = useState(false)
  const [hovered, setHovered] = useState(false)
  const goTo = useGoToDocument()
  const kind = normalizeKind(doc.kind)
  // Version-accurate only: cvId renders the pinned version; no tip fallbacks.
  const thumb = doc.rootComponentVersionId
    ? thumbnailSrc({ kind: 'design', cvId: doc.rootComponentVersionId })
    : null
  const showThumb = !!thumb && !imgFailed

  // Ask for Preview explicitly. DetailsPanel validates the request against the
  // kind's available tabs and falls back to available[0], so a design pin still
  // lands on History (designs have no preview tab) — this only makes the intent
  // survive any future reordering of tabsFor().
  const open = () => {
    void goTo({ itemId: doc.itemId, name: doc.name, kind }, { tab: 'preview' })
  }

  return (
    <Paper
      variant="outlined"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onClick={open}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 1,
        p: 0.75,
        pr: onRemove ? 0.5 : 1,
        borderRadius: 1.5,
        minWidth: 0,
        maxWidth: 280,
        cursor: 'pointer',
        transition: 'border-color .1s, background-color .1s, box-shadow .1s',
        ...(hovered && {
          borderColor: (t) => alpha(t.palette.primary.main, 0.6),
          boxShadow: 1,
        }),
        ...(asRun && {
          borderColor: (t) => alpha(t.palette.warning.main, hovered ? 0.9 : 0.6),
          bgcolor: (t) => alpha(t.palette.warning.main, 0.06),
        }),
      }}
    >
      <Box
        sx={{
          width: 34,
          height: 34,
          flexShrink: 0,
          borderRadius: 0.5,
          display: 'grid',
          placeItems: 'center',
          bgcolor: (t) => alpha(t.palette.text.primary, 0.04),
          overflow: 'hidden',
        }}
      >
        {showThumb ? (
          <Box
            component="img"
            src={thumb!}
            alt=""
            onError={() => setImgFailed(true)}
            sx={{ maxWidth: '100%', maxHeight: 34, objectFit: 'contain' }}
          />
        ) : (
          <FontAwesomeIcon
            icon={iconForItem({ kind } as Item)}
            style={{ fontSize: 16, opacity: 0.7 }}
          />
        )}
      </Box>
      <Box sx={{ minWidth: 0, flex: 1 }}>
        <Typography variant="body2" noWrap title={doc.name} sx={{ fontWeight: 600, lineHeight: 1.2 }}>
          {doc.name}
        </Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, mt: 0.25 }}>
          <Box
            component="span"
            sx={{
              px: 0.6,
              borderRadius: 0.75,
              fontSize: 10,
              fontWeight: 700,
              lineHeight: '15px',
              color: 'primary.contrastText',
              bgcolor: 'primary.main',
            }}
          >
            {t('pinnedDoc.versionBadge', { num: doc.versionNumber || '?' })}
          </Box>
          {asRun && (
            <Typography variant="caption" sx={{ fontSize: 10, color: 'warning.main', fontWeight: 600 }}>
              {t('pinnedDoc.asRun')}
            </Typography>
          )}
          <FontAwesomeIcon
            icon={faUpRightFromSquare}
            style={{ fontSize: 9, opacity: hovered ? 0.8 : 0, transition: 'opacity .1s' }}
          />
        </Box>
      </Box>
      {onRemove && (
        <Tooltip title={t('common:remove')}>
          <IconButton
            size="small"
            onClick={(e) => {
              e.stopPropagation() // remove, don't navigate
              onRemove()
            }}
            sx={{ flexShrink: 0 }}
          >
            <FontAwesomeIcon icon={faXmark} style={{ fontSize: 11 }} />
          </IconButton>
        </Tooltip>
      )}
    </Paper>
  )
}
