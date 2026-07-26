import { faXmark } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, IconButton, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useChatThread } from '../api/queries'
import { MessageComposer } from './MessageComposer'
import { MessageList } from './MessageList'
import type { ChatCaps } from './types'

// ThreadPanel is the right-hand drawer for one thread: the root message,
// its replies, and a composer that posts replies (threadRootSeq set). It
// polls on the same 2s cadence as the channel while open.
export function ThreadPanel({
  projectId,
  channelId,
  rootSeq,
  active,
  live,
  meId,
  caps,
  archived,
  onClose,
  onSend,
  onDelete,
  onToggleReaction,
  onTyping,
  sending,
}: {
  projectId: string | null
  channelId: string | null
  rootSeq: number
  active: boolean
  live: boolean
  meId: string
  caps: ChatCaps
  archived: boolean
  onClose: () => void
  onSend: (body: string, threadRootSeq: number) => Promise<unknown>
  onDelete: (seq: number) => void
  onToggleReaction: (seq: number, emoji: string, on: boolean) => void
  onTyping?: () => void
  sending: boolean
}) {
  const { t } = useTranslation('chat')
  const threadQ = useChatThread(projectId, channelId, rootSeq, active, live)
  const messages = threadQ.data?.messages ?? []

  return (
    <Box
      sx={{
        // Wide enough that an attached document card (thumbnail + name +
        // location) reads comfortably; the card itself also shrinks-to-fit.
        width: 360,
        flexShrink: 0,
        borderLeft: 1,
        borderColor: 'divider',
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          px: 1.5,
          py: 0.75,
          borderBottom: 1,
          borderColor: 'divider',
        }}
      >
        <Typography variant="subtitle2" sx={{ flex: 1 }}>
          {t('thread.title')}
        </Typography>
        <IconButton size="small" onClick={onClose} aria-label={t('thread.close')}>
          <FontAwesomeIcon icon={faXmark} size="xs" />
        </IconButton>
      </Box>
      <MessageList
        messages={messages}
        meId={meId}
        caps={caps}
        emptyText={threadQ.isLoading ? t('common:loading') : t('thread.notFound')}
        onDelete={onDelete}
        onToggleReaction={onToggleReaction}
      />
      <MessageComposer
        placeholder={t('thread.replyPlaceholder')}
        disabled={!caps.post || archived}
        disabledReason={
          archived ? t('composer.disabledArchived') : t('composer.disabledReadOnly')
        }
        sending={sending}
        onSend={(body) => onSend(body, rootSeq)}
        onTyping={onTyping}
      />
    </Box>
  )
}
