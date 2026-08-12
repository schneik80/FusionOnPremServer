import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faArrowUpRightFromSquare,
  faCirclePlus,
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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useProjects } from '../api/queries'
import { usePinMutations, usePins } from '../api/queries'
import type { FusionAction, Item, Pin } from '../api/types'
import { FUSION_NATIVE_KINDS, INSERTABLE_KINDS } from '../api/types'
import { useFusionActions } from '../state/fusionActions'
import { useNav } from '../state/nav'
import { ItemIcon } from './entityIcons'
import { RefTokenDialog, useRefTokenNavigate } from './RefTokenDialog'

// Groups for display, in order. The local-record kinds are matched BEFORE the
// document catch-all, which buckets "anything that isn't a project or folder"
// and would otherwise swallow every one of them.
const LOCAL_KINDS = new Set(['whiteboard', 'task', 'job', 'batch', 'channel'])

const GROUPS: Array<{ key: string; labelKey: string; match: (p: Pin) => boolean }> = [
  { key: 'project', labelKey: 'pins.groupProjects', match: (p) => p.kind === 'project' },
  { key: 'folder', labelKey: 'pins.groupFolders', match: (p) => p.kind === 'folder' },
  {
    key: 'document',
    labelKey: 'pins.groupDocuments',
    match: (p) => p.kind !== 'project' && p.kind !== 'folder' && !LOCAL_KINDS.has(p.kind),
  },
  { key: 'whiteboard', labelKey: 'pins.groupWhiteboards', match: (p) => p.kind === 'whiteboard' },
  { key: 'task', labelKey: 'pins.groupTasks', match: (p) => p.kind === 'task' },
  {
    key: 'production',
    labelKey: 'pins.groupProduction',
    match: (p) => p.kind === 'job' || p.kind === 'batch',
  },
  { key: 'channel', labelKey: 'pins.groupChannels', match: (p) => p.kind === 'channel' },
]

export function PinsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation('browse')
  const nav = useNav()
  const pinsQ = usePins(nav.hubId)
  const projectsQ = useProjects(nav.hubId)
  const { remove } = usePinMutations(nav.hubId)
  const pins = pinsQ.data ?? []
  const navigateToken = useRefTokenNavigate()
  // A task/job/batch pin opens a dialog rather than moving the browser, so the
  // Pins dialog stays open behind it — closing it would drop the list the user
  // is working through.
  const [openToken, setOpenToken] = useState<string | null>(null)

  const navigateToPin = (pin: Pin) => {
    // A local record is addressed by its fls: token, so it reuses the same
    // two-step every other token host does — try the navigating schemes
    // (whiteboard, channel), fall back to the dialog ones (task, job, batch).
    // Nothing here knows which is which; RefTokenDialog owns that mapping.
    if (pin.ref) {
      if (!navigateToken(pin.ref)) {
        setOpenToken(pin.ref)
        return
      }
      onClose()
      return
    }

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
                            <PinFusionActions pin={pin} />
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
                          // A local record's project is the only thing that
                          // separates two boards or two channels named the
                          // same in different projects; an APS pin gets that
                          // context from the breadcrumb it navigates to.
                          secondary={pin.ref ? pin.project_name : undefined}
                          primaryTypographyProps={{ variant: 'body2', noWrap: true }}
                          secondaryTypographyProps={{ variant: 'caption', noWrap: true }}
                          // Room for navigate + remove, plus whatever Fusion
                          // buttons this pin earns.
                          sx={{ pr: 10 + fusionActionCount(pin) * 3.5 }}
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
      {/* Rendered inside the pin list rather than beside it: MUI portals a
          Dialog to the body either way, and being a child means closing the
          pin list also dismisses the record it raised — which is what a user
          who dismissed the list expects. */}
      {openToken && <RefTokenDialog token={openToken} onClose={() => setOpenToken(null)} />}
    </Dialog>
  )
}

// fusionActionCount is how many Fusion buttons a pin shows: 0 for anything that
// is not an addressable native document, 1 for Open, 2 with Insert. The row's
// text reserve is computed from the same number, so the two can never disagree
// and let a long name run under the buttons.
//
// Local records (pin.ref), projects and folders are not documents Fusion can
// open; a pin with no captured project DM id cannot be addressed at all.
function fusionActionCount(pin: Pin): number {
  if (pin.ref || !pin.project_alt_id || !FUSION_NATIVE_KINDS.has(pin.kind)) return 0
  return INSERTABLE_KINDS.has(pin.kind) ? 2 : 1
}

// PinFusionActions is Open (and, for a 3D design, Insert) on a document pin.
// The pin list is the one place in the app that already knew where a document
// lives without a lookup — it captured the project's DM id at pin time — so
// these need no per-row APS call, which is the only reason a list can offer
// them at all (see the no-fan-out rule in CLAUDE.md).
//
// The kind is trusted here, unlike on a card. A pin can only be created from a
// browser row, and isPinnable (state/pins.ts) admits none of the kinds that a
// listing gets wrong: 'unknown' is not pinnable, so a document pin is always a
// Fusion-native one.
function PinFusionActions({ pin }: { pin: Pin }) {
  const { t } = useTranslation('browse')
  const { runAction, pendingFor } = useFusionActions()

  if (fusionActionCount(pin) === 0) return null

  const item: Item = { id: pin.id, name: pin.name, kind: pin.kind, isContainer: false }
  const pending = pendingFor(pin.id)
  const run = (action: FusionAction) => void runAction(item, pin.project_alt_id!, action)

  return (
    <>
      <PinActionButton
        label={t('pins.openInFusion')}
        icon={faArrowUpRightFromSquare}
        busy={pending === 'open'}
        disabled={pending !== null}
        onClick={() => run('open')}
      />
      {INSERTABLE_KINDS.has(pin.kind) && (
        <PinActionButton
          label={t('pins.insertInFusion')}
          icon={faCirclePlus}
          busy={pending === 'insert'}
          disabled={pending !== null}
          onClick={() => run('insert')}
        />
      )}
    </>
  )
}

function PinActionButton({
  label,
  icon,
  busy,
  disabled,
  onClick,
}: {
  label: string
  icon: typeof faCirclePlus
  busy: boolean
  disabled: boolean
  onClick: () => void
}) {
  return (
    <Tooltip title={label}>
      {/* A disabled IconButton swallows pointer events, and the tooltip with
          them — the span keeps the label reachable. */}
      <span>
        <IconButton size="small" aria-label={label} disabled={disabled} onClick={onClick}>
          {busy ? (
            <CircularProgress size={13} />
          ) : (
            <FontAwesomeIcon icon={icon} style={{ fontSize: 13 }} />
          )}
        </IconButton>
      </span>
    </Tooltip>
  )
}
