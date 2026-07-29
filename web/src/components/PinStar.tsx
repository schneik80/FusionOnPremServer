import { faStar } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { IconButton, Tooltip } from '@mui/material'
import { useTranslation } from 'react-i18next'

// PinStar is the bookmark toggle every app shares. ItemRow keeps its own copy
// inline because a browser row hangs the star off ListItem's secondaryAction
// and reserves padding for it; everywhere else — a board rail row, a task
// header, a job/batch header, the channel menu — this is the control.
//
// Visually it matches ItemRow's: primary when pinned, disabled-grey when not,
// so a pinned thing looks the same wherever it is shown. Hosts that want the
// hover-only reveal wrap it in a row whose sx targets `.pin-star`, exactly as
// ItemRow does.
export function PinStar({
  pinned,
  onToggle,
  fontSize = 13,
  className = 'pin-star',
}: {
  pinned: boolean
  onToggle: () => void
  /** glyph size in px; match the surrounding header's icon buttons */
  fontSize?: number
  className?: string
}) {
  const { t } = useTranslation('browse')
  const label = pinned ? t('itemRow.unpin') : t('itemRow.pin')
  return (
    <Tooltip title={label}>
      <IconButton
        className={className}
        size="small"
        aria-label={label}
        onClick={(e) => {
          // Stars sit inside clickable rows (a board rail item, a list row);
          // toggling a pin must never also select the row.
          e.stopPropagation()
          onToggle()
        }}
        sx={{ color: pinned ? 'primary.main' : 'text.disabled', flexShrink: 0 }}
      >
        <FontAwesomeIcon icon={faStar} style={{ fontSize }} />
      </IconButton>
    </Tooltip>
  )
}
