import {
  Box,
  Chip,
  CircularProgress,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Typography,
} from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useHubs } from '../api/queries'
import type { Item } from '../api/types'
import { HubIcon } from './entityIcons'

// HubList is the shared hub picker list (successor of the retired HubSwitcher
// dialog): the HubGate and Settings → Connection both render it. It only
// SELECTS a hub — actually locking/switching (POST /api/session/hub) is the
// caller's move, because the two homes follow selection with very different
// flows (enter vs. warn-then-teardown).
export function HubList({
  selectedId,
  currentId,
  disabled,
  onSelect,
}: {
  /** the highlighted (picked-but-not-applied) hub */
  selectedId?: string | null
  /** the hub the session is currently locked to (marked with a chip) */
  currentId?: string | null
  disabled?: boolean
  onSelect: (hub: Item) => void
}) {
  const { t } = useTranslation('browse')
  const hubsQ = useHubs()
  const hubs = hubsQ.data ?? []

  if (hubsQ.isLoading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
        <CircularProgress size={24} />
      </Box>
    )
  }
  if (hubsQ.error) {
    return (
      <Typography color="error" sx={{ p: 2 }} variant="body2">
        {(hubsQ.error as Error).message}
      </Typography>
    )
  }
  if (hubs.length === 0) {
    return (
      <Typography color="text.secondary" sx={{ p: 2 }} variant="body2">
        {t('hubList.empty')}
      </Typography>
    )
  }
  return (
    <List disablePadding>
      {hubs.map((h) => (
        <ListItemButton
          key={h.id}
          selected={h.id === selectedId}
          disabled={disabled}
          onClick={() => onSelect(h)}
        >
          <ListItemIcon sx={{ minWidth: 34 }}>
            <HubIcon />
          </ListItemIcon>
          <ListItemText primary={h.name} />
          {currentId != null && h.id === currentId && (
            <Chip size="small" variant="outlined" label={t('hubList.current')} sx={{ ml: 1 }} />
          )}
        </ListItemButton>
      ))}
    </List>
  )
}
