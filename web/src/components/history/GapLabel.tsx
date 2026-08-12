import { useTranslation } from 'react-i18next'
import { Box, Typography } from '@mui/material'
import { fmtDuration } from '../../fmt'
import type { HistoryGap } from './historyLayout'
import { gapHeight } from './historyLayout'

// The band between two day rows, saying how much time passed: "Next day",
// "3 days later", "1 year, 2 months and 3 days later".
//
// The phrase comes from fmtDuration, which builds it out of Intl unit
// formatting — so translators only ever see "{{duration}} later", never a
// per-unit catalog. Weight scales with the gap: a hairline for a next-day step,
// a dashed rule for a week or more, so a long silence is felt before it is read.
//
// The height is a constant per tier (gapHeight) rather than whatever the text
// wraps to, because ThreadOverlay computes row positions arithmetically and a
// wrapped label would slide every row below it out from under the thread.
//
// `viewW` is the visible width of the scroll container, not the stack width: in
// thread mode the stack is thousands of pixels wide, and the label pins itself
// to the viewport so it stays readable however far right the reader has
// scrolled.
export default function GapLabel({ gap, viewW }: { gap: HistoryGap; viewW: number }) {
  const { t } = useTranslation('details')
  const h = gapHeight(gap.tier)
  const tight = gap.tier === 'nextDay'
  const wide = gap.tier === 'wide'
  const label = tight
    ? t('history.gap.nextDay')
    : t('history.gap.later', { duration: fmtDuration(gap.breakdown) })

  const rule = {
    flex: 1,
    borderTop: wide ? '1px dashed' : '1px solid',
    borderColor: 'divider',
    opacity: tight ? 0.6 : 1,
  } as const

  return (
    <Box sx={{ height: h, flexShrink: 0 }}>
      <Box
        sx={{
          position: 'sticky',
          left: 0,
          width: viewW,
          height: h,
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          px: 1,
        }}
      >
        <Box sx={rule} />
        <Typography
          variant="caption"
          noWrap
          sx={{ color: tight ? 'text.disabled' : 'text.secondary', fontSize: 10 }}
        >
          {label}
        </Typography>
        <Box sx={rule} />
      </Box>
    </Box>
  )
}
