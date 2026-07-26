import { Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

// BackupsTool lands in a later phase; this is the placeholder screen.
export function BackupsTool() {
  const { t } = useTranslation('settings')
  return (
    <Typography variant="caption" color="text.secondary">
      {t('backups.comingSoon')}
    </Typography>
  )
}
