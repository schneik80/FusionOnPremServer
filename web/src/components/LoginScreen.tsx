import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faCubes } from '@fortawesome/free-solid-svg-icons'
import { Alert, Box, Button, Paper, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

// authErrorKey maps the ?auth_error=<reason> the server appends on a failed
// callback to the catalog key for something a person can read.
function authErrorKey(reason: string): string {
  switch (reason) {
    case 'state_mismatch':
    case 'state_expired':
      return 'login.errors.expired'
    case 'no_code':
    case 'exchange_failed':
      return 'login.errors.incomplete'
    case 'session_failed':
      return 'login.errors.session'
    case 'access_denied':
      return 'login.errors.denied'
    default:
      return 'login.errors.generic'
  }
}

export function LoginScreen() {
  const { t } = useTranslation('browse')
  const authError = new URLSearchParams(window.location.search).get('auth_error')

  return (
    <Box
      sx={{
        height: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        p: 2,
      }}
    >
      <Paper elevation={3} sx={{ p: 4, maxWidth: 380, width: '100%' }}>
        <Stack spacing={2.5} alignItems="center" textAlign="center">
          <FontAwesomeIcon icon={faCubes} style={{ fontSize: 40, color: '#0696d7' }} />
          <Typography variant="h5" sx={{ fontWeight: 600 }}>
            fusionlocalserver
          </Typography>
          <Typography variant="body2" color="text.secondary">
            {t('login.tagline')}
          </Typography>
          {authError && (
            <Alert severity="error" sx={{ width: '100%' }}>
              {t(authErrorKey(authError), { reason: authError })}
            </Alert>
          )}
          <Button
            variant="contained"
            size="large"
            fullWidth
            onClick={() => window.location.assign('/api/auth/login')}
          >
            {t('login.signInButton')}
          </Button>
        </Stack>
      </Paper>
    </Box>
  )
}
