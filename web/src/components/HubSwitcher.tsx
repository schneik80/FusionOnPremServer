import {
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Typography,
} from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useHubs } from '../api/queries'
import { useNav } from '../state/nav'
import { HubIcon } from './entityIcons'

export function HubSwitcher({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation('browse')
  const nav = useNav()
  const hubsQ = useHubs()
  const hubs = hubsQ.data ?? []

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="xs">
      <DialogTitle>{t('hubSwitcher.title')}</DialogTitle>
      <DialogContent dividers sx={{ p: 0 }}>
        {hubsQ.isLoading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
            <CircularProgress size={24} />
          </Box>
        ) : hubsQ.error ? (
          <Typography color="error" sx={{ p: 2 }} variant="body2">
            {(hubsQ.error as Error).message}
          </Typography>
        ) : (
          <List disablePadding>
            {hubs.map((h) => (
              <ListItemButton
                key={h.id}
                selected={h.id === nav.hubId}
                onClick={() => {
                  nav.selectHub(h.id, h.name)
                  onClose()
                }}
              >
                <ListItemIcon sx={{ minWidth: 34 }}>
                  <HubIcon />
                </ListItemIcon>
                <ListItemText primary={h.name} />
              </ListItemButton>
            ))}
          </List>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common:close')}</Button>
      </DialogActions>
    </Dialog>
  )
}
