import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faArrowUpRightFromSquare,
  faLocationArrow,
  faTrash,
} from '@fortawesome/free-solid-svg-icons'
import {
  Box,
  CircularProgress,
  Dialog,
  DialogContent,
  DialogTitle,
  IconButton,
  List,
  ListItem,
  ListItemIcon,
  ListItemText,
  ListSubheader,
  Tooltip,
  Typography,
} from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useProjects } from '../api/queries'
import { usePinMutations, usePins } from '../api/queries'
import type { Item, Pin } from '../api/types'
import { useNav } from '../state/nav'
import { ItemIcon } from './entityIcons'

// Groups for display, in order. Anything that isn't a project or folder is a
// document.
const GROUPS: Array<{ key: string; labelKey: string; match: (p: Pin) => boolean }> = [
  { key: 'project', labelKey: 'pins.groupProjects', match: (p) => p.kind === 'project' },
  { key: 'folder', labelKey: 'pins.groupFolders', match: (p) => p.kind === 'folder' },
  {
    key: 'document',
    labelKey: 'pins.groupDocuments',
    match: (p) => p.kind !== 'project' && p.kind !== 'folder',
  },
]

export function PinsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation('browse')
  const nav = useNav()
  const pinsQ = usePins(nav.hubId)
  const projectsQ = useProjects(nav.hubId)
  const { remove } = usePinMutations(nav.hubId)
  const pins = pinsQ.data ?? []

  const navigateToPin = (pin: Pin) => {
    const lookupId = pin.kind === 'project' ? pin.id : pin.project_id
    const real = projectsQ.data?.find((p) => p.id === lookupId)

    if (pin.kind === 'project') {
      const project: Item =
        real ?? {
          id: pin.id,
          name: pin.name,
          kind: 'project',
          altId: pin.project_alt_id,
          isContainer: true,
        }
      nav.navigate(project, [], null)
      onClose()
      return
    }

    if (!lookupId && !real) return // can't locate without a project id
    const project: Item =
      real ?? {
        id: lookupId!,
        name: t('pins.unknownProject'),
        kind: 'project',
        altId: pin.project_alt_id,
        isContainer: true,
      }
    const folderStack: Item[] = (pin.folder_path ?? []).map((f) => ({
      id: f.id,
      name: f.name,
      kind: 'folder',
      isContainer: true,
    }))
    const selected: Item | null =
      pin.kind === 'folder'
        ? null
        : { id: pin.id, name: pin.name, kind: pin.kind, isContainer: false }
    nav.navigate(project, folderStack, selected)
    onClose()
  }

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>
        {t('pins.title')}
        {nav.hubName ? (
          <Typography component="span" variant="body2" color="text.secondary" sx={{ ml: 1 }}>
            · {nav.hubName}
          </Typography>
        ) : null}
      </DialogTitle>
      <DialogContent dividers sx={{ p: 0 }}>
        {!nav.hubId ? (
          <Typography sx={{ p: 2 }} variant="body2" color="text.secondary">
            {t('pins.selectHub')}
          </Typography>
        ) : pinsQ.isLoading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
            <CircularProgress size={24} />
          </Box>
        ) : pins.length === 0 ? (
          <Typography sx={{ p: 2 }} variant="body2" color="text.secondary">
            {t('pins.empty')}
          </Typography>
        ) : (
          <List disablePadding>
            {GROUPS.map((g) => {
              const group = pins.filter(g.match)
              if (group.length === 0) return null
              return (
                <li key={g.key}>
                  <ul style={{ padding: 0 }}>
                    <ListSubheader sx={{ bgcolor: 'background.paper' }}>{t(g.labelKey)}</ListSubheader>
                    {group.map((pin) => (
                      <ListItem
                        key={pin.id}
                        secondaryAction={
                          <Box sx={{ display: 'flex', gap: 0.5 }}>
                            <Tooltip title={t('pins.navigate')}>
                              <IconButton size="small" onClick={() => navigateToPin(pin)}>
                                <FontAwesomeIcon icon={faLocationArrow} style={{ fontSize: 13 }} />
                              </IconButton>
                            </Tooltip>
                            <Tooltip title={t('pins.openInsertSoon')}>
                              <span>
                                <IconButton size="small" disabled>
                                  <FontAwesomeIcon
                                    icon={faArrowUpRightFromSquare}
                                    style={{ fontSize: 13 }}
                                  />
                                </IconButton>
                              </span>
                            </Tooltip>
                            <Tooltip title={t('pins.removePin')}>
                              <IconButton
                                size="small"
                                onClick={() => remove.mutate(pin.id)}
                                sx={{ color: 'text.disabled' }}
                              >
                                <FontAwesomeIcon icon={faTrash} style={{ fontSize: 13 }} />
                              </IconButton>
                            </Tooltip>
                          </Box>
                        }
                      >
                        <ListItemIcon sx={{ minWidth: 32 }}>
                          <ItemIcon item={{ kind: pin.kind }} style={{ fontSize: 14 }} />
                        </ListItemIcon>
                        <ListItemText
                          primary={pin.name}
                          primaryTypographyProps={{ variant: 'body2', noWrap: true }}
                          sx={{ pr: 10 }}
                        />
                      </ListItem>
                    ))}
                  </ul>
                </li>
              )
            })}
          </List>
        )}
      </DialogContent>
    </Dialog>
  )
}
