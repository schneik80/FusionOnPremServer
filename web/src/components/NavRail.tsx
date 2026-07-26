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
import { useTranslation } from 'react-i18next'
import { useNav } from '../state/nav'

// The rail carries the app switchers plus Pins/Settings. There is no hub
// button anymore: the session is locked to one hub and switching lives in
// Settings → Connection (the AppBar hub label deep-links there).
interface NavRailProps {
  onOpenPins: () => void
  onOpenSettings: () => void
}

export function NavRail({ onOpenPins, onOpenSettings }: NavRailProps) {
  const { t } = useTranslation('nav')
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
        <RailButton
          icon={faFolderTree}
          label={t('browser')}
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
          label={t('production')}
          active={nav.app === 'production'}
          onClick={() => nav.setApp('production')}
        />
        <RailButton
          icon={faListCheck}
          label={t('tasks')}
          active={nav.app === 'tasks'}
          onClick={() => nav.setApp('tasks')}
        />
        <Divider flexItem sx={{ mx: 1 }} />
        <RailButton icon={faStar} label={t('pins')} onClick={onOpenPins} />
        <RailButton icon={faGear} label={t('settings')} onClick={onOpenSettings} />
      </Stack>
    </Paper>
  )
}

function RailButton({
  icon,
  label,
  active,
  onClick,
}: {
  icon: IconDefinition
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
        <FontAwesomeIcon icon={icon} style={{ fontSize: 18 }} />
      </IconButton>
    </Tooltip>
  )
}
