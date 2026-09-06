import { faGaugeHigh } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { alpha, Box, Collapse, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useQuotaStatus } from '../state/useQuotaStatus'

// QuotaBanner is the app's ONE slow-mode surface: a thin strip under the app
// bar while the server's APS budget is throttling, with a countdown when a
// cooldown is running. It appears and disappears on its own — no dismiss —
// and widgets keep their own localized errors; this only explains why things
// are slower.
export function QuotaBanner() {
  const { t } = useTranslation('browse')
  const { slow, cooldownMs } = useQuotaStatus()
  const seconds = Math.ceil(cooldownMs / 1000)
  return (
    <Collapse in={slow} unmountOnExit>
      <Box
        role="status"
        aria-live="polite"
        sx={(theme) => ({
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          px: 2,
          py: 0.5,
          bgcolor: alpha(theme.palette.warning.main, 0.16),
          borderBottom: 1,
          borderColor: alpha(theme.palette.warning.main, 0.4),
        })}
      >
        <FontAwesomeIcon icon={faGaugeHigh} style={{ fontSize: 14 }} />
        <Typography variant="body2">{t('quota.slowMode')}</Typography>
        {seconds > 0 ? (
          <Typography variant="body2" sx={{ color: 'text.secondary', fontVariantNumeric: 'tabular-nums' }}>
            {t('quota.resumesIn', { seconds })}
          </Typography>
        ) : null}
      </Box>
    </Collapse>
  )
}
