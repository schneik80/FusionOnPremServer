import { useTranslation } from 'react-i18next'
import { Box, Stack, Typography } from '@mui/material'
import type { HistoryChange } from '../../api/types'
import { fmtDate } from '../../fmt'
import { historyChangeLabel } from '../../i18n/enums'
import UserAvatar from '../UserAvatar'

// The hover card for an edit that made no version: what kind of change, the
// detail the API recorded ("Estimated Cost: 100"), the exact local time, and
// the author. No version number because no version was made, and no thumbnail
// because there is nothing to show one of — so unlike a save's card, resting
// on one of these spends no APS quota.
export default function ChangeTooltip({ change }: { change: HistoryChange }) {
  const { t } = useTranslation('details')
  return (
    <Box sx={{ py: 0.25, maxWidth: 210 }}>
      <Typography variant="caption" sx={{ fontWeight: 600, display: 'block' }}>
        {historyChangeLabel(t, change.type)}
      </Typography>
      <Typography
        variant="caption"
        sx={{
          display: 'block',
          mt: 0.5,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          ...(change.comment ? {} : { fontStyle: 'italic', opacity: 0.6 }),
        }}
      >
        {change.comment || t('history.noDetail')}
      </Typography>
      <Typography variant="caption" sx={{ display: 'block', mt: 0.5, opacity: 0.85 }}>
        {change.createdOn ? fmtDate(change.createdOn, { dateStyle: 'medium', timeStyle: 'short' }) : '—'}
      </Typography>
      <Stack direction="row" spacing={0.75} alignItems="center" sx={{ mt: 0.25 }}>
        <UserAvatar id={change.createdById} name={change.createdBy} size={18} />
        <Typography variant="caption" sx={{ opacity: 0.95 }}>
          {change.createdBy || t('history.unknownAuthor')}
        </Typography>
      </Stack>
    </Box>
  )
}
