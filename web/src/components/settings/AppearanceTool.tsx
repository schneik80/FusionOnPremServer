import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faRotateLeft } from '@fortawesome/free-solid-svg-icons'
import {
  Box,
  Button,
  IconButton,
  MenuItem,
  Stack,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from '@mui/material'
import type { ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { LOCALE_LABEL, type Locale } from '../../i18n'
import { useColorMode } from '../../state/colorMode'
import { useLocale } from '../../state/locale'
import { useThemeOverrides, type TokenKey } from '../../state/themeOverrides'
import { defaultAccent, defaultAccentAlt, defaultAccentWarn, tokens, type ThemeTokens } from '../../theme'
import { Field } from './Field'

// AppearanceTool: theme preference, language, and per-mode custom colors.
// Color edits apply live (App.tsx rebuilds the theme from the overrides) and
// target the CURRENT resolved mode — switch the theme to edit the other bag.

const TOKEN_KEYS: (keyof ThemeTokens)[] = [
  'bgPrimary',
  'bgPanel',
  'bgHover',
  'border',
  'textPrimary',
  'textSecondary',
  'textMuted',
  'accentHover',
]

export function AppearanceTool() {
  const { t } = useTranslation('settings')
  const { mode, preference, setPreference } = useColorMode()
  const { locale, setLocale, available } = useLocale()
  const { overrides, setToken, reset } = useThemeOverrides()

  const bag = overrides[mode] ?? {}
  const defaults = tokens[mode]
  const rows: { key: TokenKey; def: string }[] = [
    ...TOKEN_KEYS.map((key) => ({ key: key as TokenKey, def: defaults[key] })),
    { key: 'accent', def: defaultAccent },
    { key: 'accentAlt', def: defaultAccentAlt },
    { key: 'accentWarn', def: defaultAccentWarn },
  ]
  const anyOverridden = rows.some(({ key }) => bag[key] !== undefined)

  return (
    <Stack spacing={3}>
      <Field label={t('theme.label')}>
        <ToggleButtonGroup
          size="small"
          exclusive
          value={preference}
          onChange={(_, v) => v && setPreference(v)}
        >
          <ToggleButton value="light">{t('theme.light')}</ToggleButton>
          <ToggleButton value="dark">{t('theme.dark')}</ToggleButton>
          <ToggleButton value="system">{t('theme.system')}</ToggleButton>
        </ToggleButtonGroup>
        {/* Theme + custom colors are per-hub (state/hubKeys.ts); language is global. */}
        <Typography variant="caption" color="text.secondary">
          {t('appearance.perHubNote')}
        </Typography>
      </Field>

      <Field label={t('language.label')}>
        <TextField
          select
          size="small"
          value={locale}
          onChange={(e) => setLocale(e.target.value as Locale)}
          sx={{ width: 200 }}
        >
          {available.map((l) => (
            <MenuItem key={l} value={l}>
              {LOCALE_LABEL[l]}
            </MenuItem>
          ))}
        </TextField>
      </Field>

      <Field label={t('appearance.customColors')}>
        <Typography variant="caption" color="text.secondary">
          {t('appearance.customColorsHint')}
        </Typography>
        <Stack spacing={0.25} sx={{ maxWidth: 420, pt: 0.5 }}>
          {rows.map(({ key, def }) => {
            const overridden = bag[key] !== undefined
            const value = bag[key] ?? def
            return (
              <Stack key={key} direction="row" spacing={1} alignItems="center" sx={{ minHeight: 32 }}>
                <Box
                  component="input"
                  type="color"
                  value={value}
                  onChange={(e: ChangeEvent<HTMLInputElement>) =>
                    setToken(mode, key, e.target.value)
                  }
                  sx={{
                    width: 36,
                    height: 24,
                    p: 0,
                    border: 1,
                    borderColor: 'divider',
                    borderRadius: 0.5,
                    bgcolor: 'transparent',
                    cursor: 'pointer',
                    flexShrink: 0,
                  }}
                />
                <Typography variant="body2" sx={{ flex: 1, minWidth: 0 }}>
                  {t(`appearance.token.${key}`)}
                </Typography>
                <Typography
                  variant="caption"
                  sx={{ color: 'text.disabled', fontFamily: 'monospace', flexShrink: 0 }}
                >
                  {value}
                </Typography>
                <Box sx={{ width: 30, flexShrink: 0, textAlign: 'center' }}>
                  {overridden && (
                    <Tooltip title={t('appearance.resetToken')}>
                      <IconButton size="small" onClick={() => setToken(mode, key, null)}>
                        <FontAwesomeIcon icon={faRotateLeft} style={{ fontSize: 11 }} />
                      </IconButton>
                    </Tooltip>
                  )}
                </Box>
              </Stack>
            )
          })}
        </Stack>
        <Box sx={{ pt: 1 }}>
          <Button size="small" variant="outlined" disabled={!anyOverridden} onClick={() => reset(mode)}>
            {t('appearance.resetAll')}
          </Button>
        </Box>
      </Field>
    </Stack>
  )
}
