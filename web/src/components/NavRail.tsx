import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import type { IconDefinition } from '@fortawesome/fontawesome-svg-core'
import {
  faDiagramProject,
  faFolderTree,
  faGear,
  faListCheck,
  faStar,
} from '@fortawesome/free-solid-svg-icons'
import { Divider, IconButton, Paper, Stack, Tooltip } from '@mui/material'
import type { ReactNode } from 'react'
import { useNav } from '../state/nav'
import { HubIcon } from './entityIcons'

interface NavRailProps {
  onOpenHubs: () => void
  onOpenPins: () => void
  onOpenSettings: () => void
}

export function NavRail({ onOpenHubs, onOpenPins, onOpenSettings }: NavRailProps) {
  const nav = useNav()
  return (
    <Paper
      square
      variant="outlined"
      sx={{
        width: 60,
        flexShrink: 0,
        borderTop: 0,
        borderBottom: 0,
        borderLeft: 0,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        pt: 1.5,
      }}
    >
      <Stack spacing={1.5}>
        <RailButton glyph={<HubIcon style={{ fontSize: 18 }} />} label="Hubs" onClick={onOpenHubs} />
        <Divider flexItem sx={{ mx: 1 }} />
        <RailButton
          icon={faFolderTree}
          label="Browser"
          active={nav.app === 'browser'}
          onClick={() => {
            // Already browsing → back to the projects list (the old
            // behaviour); from another app → return to the browser as-is.
            if (nav.app === 'browser') nav.clearProject()
            else nav.setApp('browser')
          }}
        />
        <RailButton
          icon={faDiagramProject}
          label="Production"
          active={nav.app === 'production'}
          onClick={() => nav.setApp('production')}
        />
        <RailButton
          icon={faListCheck}
          label="Tasks"
          active={nav.app === 'tasks'}
          onClick={() => nav.setApp('tasks')}
        />
        <Divider flexItem sx={{ mx: 1 }} />
        <RailButton icon={faStar} label="Pins" onClick={onOpenPins} />
        <RailButton icon={faGear} label="Settings" onClick={onOpenSettings} />
      </Stack>
    </Paper>
  )
}

// A rail button takes either a FontAwesome `icon` or a pre-rendered `glyph`
// (for the custom hub/project marks, which aren't FA IconDefinitions).
function RailButton({
  icon,
  glyph,
  label,
  active,
  onClick,
}: {
  icon?: IconDefinition
  glyph?: ReactNode
  label: string
  active?: boolean
  onClick: () => void
}) {
  return (
    <Tooltip title={label} placement="right">
      <IconButton
        aria-label={label}
        onClick={onClick}
        sx={{
          width: 44,
          height: 44,
          color: active ? 'primary.main' : 'text.secondary',
          bgcolor: active ? 'action.selected' : 'transparent',
        }}
      >
        {glyph ?? (icon && <FontAwesomeIcon icon={icon} style={{ fontSize: 18 }} />)}
      </IconButton>
    </Tooltip>
  )
}
