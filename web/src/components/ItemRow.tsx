import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faStar } from '@fortawesome/free-solid-svg-icons'
import {
  Box,
  IconButton,
  ListItem,
  ListItemButton,
  ListItemIcon,
  Tooltip,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useClassify } from '../api/queries'
import { thumbnailSrc } from '../api/thumbnails'
import type { Item } from '../api/types'
import { typeTagLabel } from '../i18n/enums'
import { useNav } from '../state/nav'
import { isPinnable } from '../state/pins'
import { typeTag } from './icons'
import { ItemIcon } from './entityIcons'
import { useInView } from './useInView'

interface ItemRowProps {
  item: Item
  selected: boolean
  onClick: () => void
  pinned: boolean
  onTogglePin?: (item: Item) => void
  /** when true, designs without a subtype fetch a classify refinement */
  classifyEnabled?: boolean
}

export function ItemRow({
  item,
  selected,
  onClick,
  pinned,
  onTogglePin,
  classifyEnabled,
}: ItemRowProps) {
  const { t } = useTranslation('browse')
  // Both the classify query and the thumbnail below wait until the row is
  // near the viewport. The column renders every item with no windowing, so
  // eagerly refining a 40-design folder fired ~80 APS requests the moment it
  // opened and the tail came back 429 — visible as missing thumbnails and
  // unrefined icons. Deferring costs nothing (a row you can't see gains
  // nothing from a refined icon) and keeps a 500-item folder from melting.
  const [rowRef, inView] = useInView<HTMLLIElement>()

  // Refine an unclassified design's icon to assembly/part. The query is
  // disabled (cvId undefined) for every other row.
  const cvId =
    inView && classifyEnabled && item.kind === 'design' && !item.subtype
      ? item.componentVersionId
      : undefined
  const classify = useClassify(cvId)
  const display: Item = {
    ...item,
    subtype: item.subtype || classify.data?.subtype,
  }

  const tag = typeTagLabel(t, typeTag(display))
  const showStar = !!onTogglePin && isPinnable(item.kind)

  // Show the document's preview in place of the icon: designs via their MFGDM
  // thumbnail, drawings via the Model Derivative preview (keyed by item id + the
  // current project's altId). On a miss/404 fall back to the kind icon.
  const nav = useNav()
  const thumbSrc = inView
    ? thumbnailSrc({
        kind: display.kind,
        cvId: display.componentVersionId,
        itemId: item.id,
        projectAltId: nav.project?.altId,
      })
    : null
  const [thumbFailed, setThumbFailed] = useState(false)

  return (
    <ListItem
      ref={rowRef}
      disablePadding
      // The pin star stays quiet until wanted: hidden on unpinned rows,
      // revealed on hover or keyboard focus, and kept on permanently once the
      // item is pinned (removing the pin returns it to hover-only). The
      // right-padding is reserved either way (see ListItemButton pr) so the
      // star fading in never reflows the name.
      sx={{
        '& .pin-star': { opacity: pinned ? 1 : 0, transition: 'opacity 120ms' },
        '&:hover .pin-star, &:focus-within .pin-star': { opacity: 1 },
      }}
      secondaryAction={
        showStar ? (
          <Tooltip title={pinned ? t('itemRow.unpin') : t('itemRow.pin')}>
            <IconButton
              className="pin-star"
              edge="end"
              size="small"
              onClick={(e) => {
                e.stopPropagation()
                onTogglePin?.(item)
              }}
              sx={{ color: pinned ? 'primary.main' : 'text.disabled' }}
            >
              <FontAwesomeIcon icon={faStar} style={{ fontSize: 13 }} />
            </IconButton>
          </Tooltip>
        ) : undefined
      }
    >
      <ListItemButton selected={selected} onClick={onClick} sx={{ pr: showStar ? 5 : 1.5 }}>
        <ListItemIcon sx={{ minWidth: 30, color: selected ? 'primary.main' : 'text.secondary' }}>
          {thumbSrc && !thumbFailed ? (
            <Box
              component="img"
              src={thumbSrc}
              alt=""
              draggable={false} // a row is a control, not a draggable image
              onError={() => setThumbFailed(true)}
              sx={{ width: 22, height: 22, objectFit: 'contain', borderRadius: 0.5, display: 'block' }}
            />
          ) : (
            <ItemIcon item={display} style={{ fontSize: 15 }} />
          )}
        </ListItemIcon>
        <Box sx={{ minWidth: 0, display: 'flex', alignItems: 'baseline', gap: 0.75 }}>
          <Typography
            variant="body2"
            noWrap
            sx={{ minWidth: 0, fontWeight: selected ? 600 : 400 }}
            title={item.name}
          >
            {item.name}
          </Typography>
          {tag && (
            <Typography
              variant="caption"
              sx={{ color: 'text.disabled', flexShrink: 0, fontSize: 10 }}
            >
              · {tag}
            </Typography>
          )}
        </Box>
      </ListItemButton>
    </ListItem>
  )
}
