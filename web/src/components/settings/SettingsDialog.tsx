import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faBoxArchive,
  faDatabase,
  faFileLines,
  faHeartPulse,
  faNetworkWired,
  faPalette,
  type IconDefinition,
} from '@fortawesome/free-solid-svg-icons'
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  List,
  ListItemButton,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMeta } from '../../api/queries'
import { AppearanceTool } from './AppearanceTool'
import { BackupsTool } from './BackupsTool'
import { ConnectionTool } from './ConnectionTool'
import { DataTool } from './DataTool'
import { LogsTool } from './LogsTool'
import { UptimeTool } from './UptimeTool'

// SettingsDialog is the admin console: a master-detail dialog (shell cloned
// from the hub browser) with a tool list on the left and the selected tool on
// the right. Each tool receives `active` (dialog open && tool selected) so
// polling queries only run while their screen is actually visible.

type ToolId = 'appearance' | 'connection' | 'uptime' | 'backups' | 'logs' | 'data'

const TOOLS: { id: ToolId; icon: IconDefinition }[] = [
  { id: 'appearance', icon: faPalette },
  { id: 'connection', icon: faNetworkWired },
  { id: 'uptime', icon: faHeartPulse },
  { id: 'backups', icon: faBoxArchive },
  { id: 'logs', icon: faFileLines },
  { id: 'data', icon: faDatabase },
]

export function SettingsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation('settings')
  const metaQ = useMeta()
  const meta = metaQ.data
  const [tool, setTool] = useState<ToolId>('appearance')

  const active = (id: ToolId) => open && tool === id

  return (
    <Dialog open={open} onClose={onClose} maxWidth="md" fullWidth PaperProps={{ sx: { height: '74vh' } }}>
      <DialogTitle sx={{ pb: 1 }}>{t('title')}</DialogTitle>
      <DialogContent dividers sx={{ p: 0, display: 'flex', minHeight: 0 }}>
        <Box
          sx={{
            width: 240,
            flexShrink: 0,
            borderRight: 1,
            borderColor: 'divider',
            overflowY: 'auto',
            minHeight: 0,
          }}
        >
          <List dense disablePadding sx={{ py: 0.5 }}>
            {TOOLS.map(({ id, icon }) => (
              <ListItemButton
                key={id}
                dense
                selected={tool === id}
                onClick={() => setTool(id)}
                sx={{ py: 0.75, gap: 1.25 }}
              >
                <Box
                  sx={{
                    width: 20,
                    flexShrink: 0,
                    textAlign: 'center',
                    color: tool === id ? 'primary.main' : 'text.secondary',
                  }}
                >
                  <FontAwesomeIcon icon={icon} style={{ fontSize: 13 }} />
                </Box>
                <Typography variant="body2" sx={{ fontWeight: tool === id ? 600 : 400 }}>
                  {t(`tools.${id}`)}
                </Typography>
              </ListItemButton>
            ))}
          </List>
        </Box>
        <Box sx={{ flex: 1, minWidth: 0, overflow: 'auto', p: 2.5 }}>
          {tool === 'appearance' && <AppearanceTool />}
          {tool === 'connection' && <ConnectionTool active={active('connection')} />}
          {tool === 'uptime' && <UptimeTool active={active('uptime')} />}
          {tool === 'backups' && <BackupsTool />}
          {tool === 'logs' && <LogsTool active={active('logs')} />}
          {tool === 'data' && <DataTool />}
        </Box>
      </DialogContent>
      <DialogActions sx={{ px: 2, py: 1.5 }}>
        {/* eslint-disable-next-line i18next/no-literal-string -- product name */}
        <Typography variant="caption" color="text.secondary" noWrap sx={{ flex: 1, minWidth: 0 }}>fusionlocalserver · {meta?.version ?? '—'}</Typography>
        <Button onClick={onClose} color="inherit">
          {t('common:close')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
