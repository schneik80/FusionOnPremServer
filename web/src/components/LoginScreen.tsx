import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faCubes } from '@fortawesome/free-solid-svg-icons'
import { Alert, Box, Button, Paper, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import { useMeta } from '../api/queries'

// The sign-in logo's display cap. The stored image is never resized — the
// operator's file is kept verbatim (see server/branding.go) — so this is the
// one place its rendered size is decided: at most this tall, width scaling
// with it, and scaled down further to fit a narrow window.
export const LOGIN_LOGO_MAX_H = 480

// Without a logo the card is the classic narrow sign-in box. With one it grows
// to whatever the logo needs, because a banner mark at 480px tall can be far
// wider than a 380px card and cropping someone's logo is not an option.
const CARD_MIN_W = 380

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
  // /api/meta is public, which is what makes this possible before sign-in.
  const metaQ = useMeta()
  const logo = metaQ.data?.logo
  const authError = new URLSearchParams(window.location.search).get('auth_error')

  return (
    <Box
      sx={{
        // minHeight, not height: a 480px logo can make the card taller than the
        // window, and a fixed-height centred flex box would put the top of it
        // out of reach — no scroll, no way back to it. Vertical centring is an
        // auto margin on the card rather than align-items for the same reason:
        // an auto margin clamps at the start edge when the item overflows.
        minHeight: '100vh',
        display: 'flex',
        justifyContent: 'center',
        p: 2,
      }}
    >
      <Paper
        elevation={3}
        sx={{
          p: 4,
          my: 'auto',
          // Shrink-to-fit, floored at the classic card width and capped by the
          // window: the surface takes its size from the logo rather than the
          // logo being squeezed into a fixed surface. `fit-content` resolves to
          // min(max-content, available), so a logo wider than the viewport
          // pulls the card only as wide as there is room for — and the image's
          // own max-width then scales it down proportionally inside that.
          width: 'fit-content',
          minWidth: `min(${CARD_MIN_W}px, 100%)`,
          maxWidth: '100%',
        }}
      >
        <Stack spacing={2.5} alignItems="center" textAlign="center">
          {/* Nothing is drawn until we know which mark is the right one: a
              generic icon that swaps to the operator's logo a moment later
              reads as a glitch, and the probe is a small same-origin call the
              client has usually already cached. */}
          {metaQ.isLoading ? null : logo ? (
            <Box
              component="img"
              src={api.logoUrl(logo.version)}
              alt={t('login.logoAlt')}
              // width:auto with a max-height is the whole scaling rule: the
              // height caps at 480 and the width follows the aspect ratio;
              // max-width then takes over on a window too narrow for that, and
              // because height stays auto the image scales down as a whole
              // instead of distorting.
              sx={{
                display: 'block',
                width: 'auto',
                height: 'auto',
                maxHeight: LOGIN_LOGO_MAX_H,
                maxWidth: '100%',
                // Reserve the right box before the bytes arrive so the card
                // doesn't jump. Absent for formats whose size the server
                // couldn't read — layout is still correct without it.
                ...(logo.width && logo.height
                  ? { aspectRatio: `${logo.width} / ${logo.height}` }
                  : {}),
              }}
            />
          ) : (
            <FontAwesomeIcon icon={faCubes} style={{ fontSize: 40, color: '#0696d7' }} />
          )}
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
