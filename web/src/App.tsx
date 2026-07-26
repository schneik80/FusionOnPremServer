import CssBaseline from '@mui/material/CssBaseline'
import { ThemeProvider } from '@mui/material/styles'
import { Box, CircularProgress } from '@mui/material'
import { useMemo } from 'react'
import { useAuthMe } from './api/queries'
import { AppLayout } from './components/AppLayout'
import { LoginScreen } from './components/LoginScreen'
import { useColorMode } from './state/colorMode'
import { useThemeOverrides } from './state/themeOverrides'
import { NavProvider } from './state/nav'
import { UploadsProvider } from './state/uploads'
import { makeTheme } from './theme'

// Gate decides what to render based on login state: a spinner while the probe
// is in flight, the login screen when signed out, the app once authenticated.
function Gate() {
  const authQ = useAuthMe()

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
  return (
    <NavProvider>
      <UploadsProvider>
        <AppLayout />
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
