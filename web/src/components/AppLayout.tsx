import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faMoon,
  faRightFromBracket,
  faSun,
} from '@fortawesome/free-solid-svg-icons'
import {
  AppBar,
  Box,
  IconButton,
  Toolbar,
  Tooltip,
  Typography,
} from '@mui/material'
import { FusionLogo } from './FusionLogo'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import { useAuthMe, useMeta } from '../api/queries'
import { useColorMode } from '../state/colorMode'
import { saveLastHub } from '../state/hubKeys'
import { useNav } from '../state/nav'
import { teardownAndReload } from '../state/teardown'
import { ProductionScreen } from '../production/ProductionScreen'
import { TasksScreen } from '../tasks/TasksScreen'
import { BreadcrumbBar } from './BreadcrumbBar'
import { BrowserStage } from './BrowserStage'
import { FusionActionFeedback } from './DocumentActions'
import { NavRail } from './NavRail'
import { NotificationBell } from './notifications/NotificationBell'
import UserAvatar from './UserAvatar'
import { PinsDialog } from './PinsDialog'
import { SettingsDialog, type SettingsToolId } from './settings/SettingsDialog'
import { UploadDialog } from './UploadDialog'
import { UploadDropOverlay, UploadFooter } from './UploadFooter'

type DialogKind = 'pins' | 'settings' | null

export function AppLayout() {
  const { t } = useTranslation('browse')
  const [dialog, setDialog] = useState<DialogKind>(null)
  const [settingsTool, setSettingsTool] = useState<SettingsToolId | undefined>(undefined)
  const nav = useNav()
  const metaQ = useMeta()
  const authQ = useAuthMe()
  const qc = useQueryClient()
  const { mode, toggle } = useColorMode()

  const openSettings = (tool?: SettingsToolId) => {
    setSettingsTool(tool)
    setDialog('settings')
  }

  // The session is hub-locked before AppLayout mounts (Gate → HubGate), so
  // keep nav and fls.lastHub aligned with the lock: seed an empty nav, and
  // reset a permalink that names a DIFFERENT hub — its data could only 403
  // (the wire hub must match the session hub). Replaces the old
  // restore-last-hub effect; entering a hub now happens in the gate.
  const lockedHubId = authQ.data?.selectedHubId
  const lockedHubName = authQ.data?.selectedHubName ?? ''
  useEffect(() => {
    if (!lockedHubId) return
    saveLastHub(lockedHubId, lockedHubName)
    if (nav.hubId !== lockedHubId) nav.selectHub(lockedHubId, lockedHubName)
  }, [lockedHubId, lockedHubName, nav.hubId]) // eslint-disable-line react-hooks/exhaustive-deps

  // logout drops the server session, then runs the shared client teardown
  // (clear caches + reload at the root) so a different user on this browser
  // doesn't briefly see the prior user's data.
  const logout = async () => {
    try {
      await api.logout()
    } finally {
      teardownAndReload(qc)
    }
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      <AppBar position="static">
        <Toolbar variant="dense" disableGutters sx={{ gap: 1.5, pr: 1 }}>
          {/* Logo sits in a NavRail-width slot so it centers over the rail icons. */}
          <Box sx={{ width: 60, flexShrink: 0, display: 'flex', justifyContent: 'center' }}>
            <FusionLogo size={24} />
          </Box>
          {/* The locked hub's name. Switching hubs is consolidated in
              Settings → Connection; this label deep-links there. */}
          <Tooltip title={t('appLayout.changeHub')}>
            <Box
              component="button"
              onClick={() => openSettings('connection')}
              sx={{
                display: 'flex',
                alignItems: 'center',
                minWidth: 0,
                p: 0,
                border: 0,
                background: 'none',
                color: 'inherit',
                font: 'inherit',
                cursor: 'pointer',
                '&:hover .hubName': { textDecoration: 'underline' },
              }}
            >
              <Typography className="hubName" variant="h6" noWrap sx={{ fontWeight: 600 }}>
                {nav.hubName ?? (lockedHubName || t('appLayout.selectHub'))}
              </Typography>
            </Box>
          </Tooltip>
          {metaQ.data && (
            <Typography variant="caption" color="text.secondary">
              {metaQ.data.version}
            </Typography>
          )}
          <Box sx={{ flex: 1 }} />
          <NotificationBell />
          <Tooltip title={mode === 'dark' ? t('appLayout.switchToLight') : t('appLayout.switchToDark')}>
            <IconButton aria-label={t('appLayout.toggleTheme')} onClick={toggle} sx={{ color: 'text.secondary' }}>
              <FontAwesomeIcon icon={mode === 'dark' ? faSun : faMoon} style={{ fontSize: 16 }} />
            </IconButton>
          </Tooltip>
          {authQ.data?.user && (authQ.data.user.name || authQ.data.user.email) && (
            <>
              {/* The signed-in person's identity colour, the same one their
                  chat messages and history tracks carry. */}
              <UserAvatar
                id={authQ.data.user.id}
                name={authQ.data.user.name || authQ.data.user.email}
                size={24}
                tooltip={authQ.data.user.email || authQ.data.user.name}
              />
              <Typography variant="caption" color="text.secondary">
                {authQ.data.user.name || authQ.data.user.email}
              </Typography>
            </>
          )}
          <Tooltip title={t('appLayout.signOut')}>
            <IconButton aria-label={t('appLayout.signOut')} onClick={logout} sx={{ color: 'text.secondary' }}>
              <FontAwesomeIcon icon={faRightFromBracket} style={{ fontSize: 16 }} />
            </IconButton>
          </Tooltip>
        </Toolbar>
      </AppBar>

      <Box sx={{ display: 'flex', flex: 1, minHeight: 0 }}>
        <NavRail
          onOpenPins={() => setDialog('pins')}
          onOpenSettings={() => openSettings()}
        />
        {/* Both apps stay mounted (ProjectPanel philosophy): visiting Tasks
            must not lose the browser's drill-down state, and vice versa. */}
        <Box
          sx={{
            display: nav.app === 'browser' ? 'flex' : 'none',
            flexDirection: 'column',
            flex: 1,
            minWidth: 0,
          }}
        >
          <BreadcrumbBar />
          <BrowserStage />
        </Box>
        <Box
          sx={{
            display: nav.app === 'tasks' ? 'flex' : 'none',
            flexDirection: 'column',
            flex: 1,
            minWidth: 0,
          }}
        >
          <TasksScreen active={nav.app === 'tasks'} />
        </Box>
        <Box
          sx={{
            display: nav.app === 'production' ? 'flex' : 'none',
            flexDirection: 'column',
            flex: 1,
            minWidth: 0,
          }}
        >
          <ProductionScreen active={nav.app === 'production'} />
        </Box>
      </Box>

      <PinsDialog open={dialog === 'pins'} onClose={() => setDialog(null)} />
      <SettingsDialog
        open={dialog === 'settings'}
        initialTool={settingsTool}
        onClose={() => setDialog(null)}
      />
      <UploadDialog />
      <UploadFooter />
      <UploadDropOverlay />
      {/* App-wide, not per panel: an Open/Insert outlives the details panel
          that started it, so its outcome has to land somewhere that is still
          mounted when the helper finally reports. */}
      <FusionActionFeedback />
    </Box>
  )
}
