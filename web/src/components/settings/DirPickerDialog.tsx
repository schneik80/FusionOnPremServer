import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faFolder, faTurnUp } from '@fortawesome/free-solid-svg-icons'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  List,
  ListItemButton,
  Typography,
} from '@mui/material'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAdminFsDirs } from '../../api/queries'

// DirPickerDialog: a small server-side directory browser for choosing the
// backup folder. It only ever lists directories (GET /api/admin/fs/dirs) —
// files are invisible by design — starting from the user's home (or the
// currently configured folder) and walking up/down from there.

export function DirPickerDialog({
  open,
  initialPath,
  onClose,
  onSelect,
}: {
  open: boolean
  initialPath?: string
  onClose: () => void
  onSelect: (path: string) => void
}) {
  const { t } = useTranslation('settings')
  // undefined = "wherever the server says home is" (the empty-path default).
  const [path, setPath] = useState<string | undefined>(undefined)

  // Restart from the configured folder (or home) each time the picker opens.
  useEffect(() => {
    if (open) setPath(initialPath || undefined)
  }, [open, initialPath])

  const dirsQ = useAdminFsDirs(path, open)
  const data = dirsQ.data

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth PaperProps={{ sx: { height: '60vh' } }}>
      <DialogTitle sx={{ pb: 1 }}>{t('backups.picker.title')}</DialogTitle>
      <DialogContent dividers sx={{ p: 0, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        <Typography
          variant="caption"
          color="text.secondary"
          sx={{ px: 2, py: 1, borderBottom: 1, borderColor: 'divider', wordBreak: 'break-all', flexShrink: 0 }}
        >
          {data?.path ?? '…'}
        </Typography>
        {dirsQ.isLoading ? (
          <Box sx={{ p: 2, textAlign: 'center' }}>
            <CircularProgress size={18} />
          </Box>
        ) : dirsQ.error ? (
          <Alert severity="error" variant="outlined" sx={{ m: 2 }}>
            {(dirsQ.error as Error).message}
          </Alert>
        ) : (
          <List dense disablePadding sx={{ overflowY: 'auto', flex: 1, minHeight: 0 }}>
            {data?.parent && (
              <ListItemButton dense onClick={() => setPath(data.parent)} sx={{ gap: 1.25 }}>
                <Box sx={{ width: 18, textAlign: 'center', color: 'text.secondary' }}>
                  <FontAwesomeIcon icon={faTurnUp} style={{ fontSize: 12 }} />
                </Box>
                <Typography variant="body2" color="text.secondary">
                  {t('backups.picker.up')}
                </Typography>
              </ListItemButton>
            )}
            {data?.dirs.map((name) => (
              <ListItemButton
                key={name}
                dense
                onClick={() => data && setPath(`${data.path === '/' ? '' : data.path}/${name}`)}
                sx={{ gap: 1.25 }}
              >
                <Box sx={{ width: 18, textAlign: 'center', color: 'text.secondary' }}>
                  <FontAwesomeIcon icon={faFolder} style={{ fontSize: 12 }} />
                </Box>
                <Typography variant="body2" noWrap>
                  {name}
                </Typography>
              </ListItemButton>
            ))}
            {data && data.dirs.length === 0 && (
              <Typography variant="caption" color="text.secondary" sx={{ display: 'block', px: 2, py: 1.5 }}>
                {t('backups.picker.empty')}
              </Typography>
            )}
          </List>
        )}
      </DialogContent>
      <DialogActions sx={{ px: 2, py: 1.5 }}>
        <Button onClick={onClose} color="inherit">
          {t('common:cancel')}
        </Button>
        <Button
          variant="contained"
          disabled={!data?.path}
          onClick={() => data?.path && onSelect(data.path)}
        >
          {t('backups.picker.select')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
