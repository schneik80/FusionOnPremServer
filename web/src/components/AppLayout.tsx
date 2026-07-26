import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faChevronDown,
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
import { QUERY_CACHE_KEY } from '../queryPersist'
import { useAuthMe, useHubs, useMeta } from '../api/queries'
import { useColorMode } from '../state/colorMode'
import { loadLastHub, useNav } from '../state/nav'
import { ProductionScreen } from '../production/ProductionScreen'
import { TasksScreen } from '../tasks/TasksScreen'
import { BreadcrumbBar } from './BreadcrumbBar'
import { BrowserStage } from './BrowserStage'
import { HubSwitcher } from './HubSwitcher'
import { NavRail } from './NavRail'
import { PinsDialog } from './PinsDialog'
import { SettingsDialog } from './SettingsDialog'
import { UploadDialog } from './UploadDialog'
import { UploadDropOverlay, UploadFooter } from './UploadFooter'

type DialogKind = 'hubs' | 'pins' | 'settings' | null

export function AppLayout() {
  const { t } = useTranslation('browse')
  const [dialog, setDialog] = useState<DialogKind>(null)
  const nav = useNav()
  const metaQ = useMeta()
  const authQ = useAuthMe()
  const hubsQ = useHubs()
  const qc = useQueryClient()
  const { mode, toggle } = useColorMode()

  // Restore the last-used hub once the hub list loads, but only if it's still
  // one of the user's hubs (so a since-revoked hub or a different user on this
  // browser falls back to picking manually).
  useEffect(() => {
    if (nav.hubId || !hubsQ.data) return
    const saved = loadLastHub()
    if (!saved) return
    const hub = hubsQ.data.find((h) => h.id === saved.id)
    if (hub) nav.selectHub(hub.id, hub.name)
  }, [nav.hubId, hubsQ.data]) // eslint-disable-line react-hooks/exhaustive-deps

  // logout drops the server session and clears the persisted query cache (so a
  // different user on this browser doesn't briefly see the prior user's data),
  // then reloads at the root so the gate shows the login screen.
  const logout = async () => {
    try {
      await api.logout()
    } finally {
      qc.clear()
      try {
        localStorage.removeItem(QUERY_CACHE_KEY)
      } catch {
        /* storage unavailable — nothing to clear */
      }
      window.location.assign('/')
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
          {/* Active hub name; clicking opens the hub switcher. */}
          <Tooltip title={t('appLayout.changeHub')}>
            <Box
              component="button"
              onClick={() => setDialog('hubs')}
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 0.75,
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
                {nav.hubName ?? t('appLayout.selectHub')}
              </Typography>
              <FontAwesomeIcon icon={faChevronDown} style={{ fontSize: 11, opacity: 0.55 }} />
            </Box>
          </Tooltip>
          {metaQ.data && (
            <Typography variant="caption" color="text.secondary">
              {metaQ.data.version}
            </Typography>
          )}
          <Box sx={{ flex: 1 }} />
          <Tooltip title={mode === 'dark' ? t('appLayout.switchToLight') : t('appLayout.switchToDark')}>
            <IconButton aria-label={t('appLayout.toggleTheme')} onClick={toggle} sx={{ color: 'text.secondary' }}>
              <FontAwesomeIcon icon={mode === 'dark' ? faSun : faMoon} style={{ fontSize: 16 }} />
            </IconButton>
          </Tooltip>
          {authQ.data?.user && (authQ.data.user.name || authQ.data.user.email) && (
            <Typography variant="caption" color="text.secondary">
              {authQ.data.user.name || authQ.data.user.email}
            </Typography>
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
          onOpenHubs={() => setDialog('hubs')}
          onOpenPins={() => setDialog('pins')}
          onOpenSettings={() => setDialog('settings')}
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
          <BreadcrumbBar onOpenHubs={() => setDialog('hubs')} />
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

      <HubSwitcher open={dialog === 'hubs'} onClose={() => setDialog(null)} />
      <PinsDialog open={dialog === 'pins'} onClose={() => setDialog(null)} />
      <SettingsDialog open={dialog === 'settings'} onClose={() => setDialog(null)} />
      <UploadDialog />
      <UploadFooter />
      <UploadDropOverlay />
    </Box>
  )
}
