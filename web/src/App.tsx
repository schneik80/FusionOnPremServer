import CssBaseline from '@mui/material/CssBaseline'
import { ThemeProvider } from '@mui/material/styles'
import { Box, CircularProgress } from '@mui/material'
import { useMemo, useState } from 'react'
import { useAuthMe } from './api/queries'
import { AppLayout } from './components/AppLayout'
import { DmDeepLinkGate } from './components/DmDeepLinkGate'
import { HubGate } from './components/HubGate'
import { LoginScreen } from './components/LoginScreen'
import { readDmDeepLinkHere } from './state/dmDeepLink'
import { useColorMode } from './state/colorMode'
import { useThemeOverrides } from './state/themeOverrides'
import { FusionActionsProvider } from './state/fusionActions'
import { NavProvider } from './state/nav'
import { UploadsProvider } from './state/uploads'
import { makeTheme } from './theme'

// Gate decides what to render based on login state: a spinner while the probe
// is in flight, the login screen when signed out, then the Fusion deep link
// (when the URL carries one) and the hub gate until the session is locked to a
// hub (every data route 409s until it is), and finally the app.
function Gate() {
  const authQ = useAuthMe()
  // Read once, at mount: DmDeepLinkGate rewrites the URL when it is done, so
  // re-reading the location would flip this to null mid-flight.
  const [deepLink, setDeepLink] = useState(readDmDeepLinkHere)

  if (authQ.isLoading) {
    return (
      <Box sx={{ height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <CircularProgress />
      </Box>
    )
  }
  if (!authQ.data?.authenticated) {
    return <LoginScreen />
  }
  // A Fusion deep link resolves its Data Management ids and rewrites itself as
  // a permalink before anything else mounts — including the hub lock it may
  // need to take, which is why it precedes HubGate.
  if (deepLink) {
    return <DmDeepLinkGate link={deepLink} onDone={() => setDeepLink(null)} />
  }
  // No hub lock yet: HubGate auto-relocks the remembered hub or shows the
  // full-screen picker. A remembered SESSION (selectedHubId set) skips it.
  if (!authQ.data.selectedHubId) {
    return <HubGate />
  }
  return (
    <NavProvider>
      <UploadsProvider>
        <FusionActionsProvider>
          <AppLayout />
        </FusionActionsProvider>
      </UploadsProvider>
    </NavProvider>
  )
}

export default function App() {
  const { mode } = useColorMode()
  const { overrides } = useThemeOverrides()
  const theme = useMemo(() => makeTheme(mode, overrides[mode]), [mode, overrides])

  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <Gate />
    </ThemeProvider>
  )
}
