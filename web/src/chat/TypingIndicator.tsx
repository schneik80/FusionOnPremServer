import { Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

// TypingIndicator is the "N is typing…" caption pinned above the composer.
// It always renders (with a non-breaking space when idle) so the timeline
// doesn't jump when someone starts or stops typing.
export function TypingIndicator({ names }: { names: string[] }) {
  const { t } = useTranslation('chat')
  let text = ' '
  if (names.length === 2) text = t('typingTwo', { a: names[0], b: names[1] })
  else if (names.length >= 1) text = t('typing', { name: names[0], count: names.length })

  return (
    <Typography
      variant="caption"
      color="text.secondary"
      sx={{ px: 1.5, display: 'block', lineHeight: '18px', fontStyle: 'italic' }}
    >
      {text}
    </Typography>
  )
}
