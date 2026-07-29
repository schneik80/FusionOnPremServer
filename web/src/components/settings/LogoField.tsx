import { faTrash, faUpload } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Alert, Box, Button, CircularProgress, Stack, Typography } from '@mui/material'
import { useRef, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../api/client'
import { useLogoMutations, useMeta } from '../../api/queries'
import { localizeApiError } from '../../i18n/apiError'
import { LOGIN_LOGO_MAX_H } from '../LoginScreen'
import { Field } from './Field'

// The logo the sign-in screen shows in place of the built-in mark.
//
// Two things about it are unlike every other setting in Appearance, and the UI
// says both out loud rather than letting someone discover them: theme and
// colors are this browser's preference for this hub, whereas the logo is
// stored on the server, shared by everyone it serves — and it is on a page
// that renders BEFORE sign-in, so it is visible to anyone who can reach the
// server at all.
const ACCEPT = 'image/png,image/jpeg,image/gif,image/webp,image/svg+xml'

// Preview height. Small enough to sit in a settings row, and wide enough that
// a banner mark still reads; the real cap is LOGIN_LOGO_MAX_H on the sign-in
// screen, which the caption states.
const PREVIEW_H = 72

export function LogoField() {
  const { t } = useTranslation('settings')
  const metaQ = useMeta()
  const logo = metaQ.data?.logo
  const { upload, remove } = useLogoMutations()
  const fileRef = useRef<HTMLInputElement>(null)

  const busy = upload.isPending || remove.isPending
  const error = upload.error ?? remove.error

  const onPick = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // Reset the input so re-picking the same file still fires a change event.
    e.target.value = ''
    if (file) upload.mutate(file)
  }

  return (
    <Field label={t('logo.label')}>
      <Typography variant="caption" color="text.secondary">
        {t('logo.hint', { height: LOGIN_LOGO_MAX_H })}
      </Typography>
      <Typography variant="caption" color="text.secondary">
        {t('logo.scopeHint')}
      </Typography>

      <Box sx={{ pt: 1 }}>
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            height: PREVIEW_H + 16,
            px: 2,
            border: 1,
            borderColor: 'divider',
            borderRadius: 1,
            bgcolor: 'background.canvas',
            maxWidth: 420,
          }}
        >
          {busy ? (
            <CircularProgress size={20} />
          ) : logo ? (
            <Box
              component="img"
              src={api.logoUrl(logo.version)}
              alt={t('logo.previewAlt')}
              // Same rule as the sign-in screen, just a smaller cap: height
              // bounded, width follows the aspect ratio, and never wider than
              // the box.
              sx={{
                display: 'block',
                width: 'auto',
                height: 'auto',
                maxHeight: PREVIEW_H,
                maxWidth: '100%',
              }}
            />
          ) : (
            <Typography variant="caption" color="text.disabled">
              {t('logo.none')}
            </Typography>
          )}
        </Box>
      </Box>

      {logo && (
        <Typography variant="caption" sx={{ color: 'text.disabled' }}>
          {logo.width && logo.height
            ? t('logo.detailsSized', {
                width: logo.width,
                height: logo.height,
                size: Math.max(1, Math.round(logo.size / 1024)),
              })
            : t('logo.details', { size: Math.max(1, Math.round(logo.size / 1024)) })}
        </Typography>
      )}

      {error && (
        <Alert severity="error" sx={{ maxWidth: 420 }}>
          {localizeApiError(t, error)}
        </Alert>
      )}

      <Stack direction="row" spacing={1} sx={{ pt: 1 }}>
        <Button
          size="small"
          variant="outlined"
          disabled={busy}
          startIcon={<FontAwesomeIcon icon={faUpload} style={{ fontSize: 11 }} />}
          onClick={() => fileRef.current?.click()}
        >
          {logo ? t('logo.replace') : t('logo.choose')}
        </Button>
        {logo && (
          <Button
            size="small"
            color="error"
            disabled={busy}
            startIcon={<FontAwesomeIcon icon={faTrash} style={{ fontSize: 11 }} />}
            onClick={() => remove.mutate()}
          >
            {t('logo.remove')}
          </Button>
        )}
        <Box
          component="input"
          ref={fileRef}
          type="file"
          accept={ACCEPT}
          onChange={onPick}
          sx={{ display: 'none' }}
        />
      </Stack>
    </Field>
  )
}
