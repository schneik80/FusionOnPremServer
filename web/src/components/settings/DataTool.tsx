import { Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

// DataTool lands in a later phase; this is the placeholder screen.
export function DataTool() {
  const { t } = useTranslation('settings')
  return (
    <Typography variant="caption" color="text.secondary">
      {t('data.comingSoon')}
    </Typography>
  )
}
