import { faArrowLeft, faXmark } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, IconButton, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useChatThread } from '../api/queries'
import { THREAD_BASIS, THREAD_MIN } from './chatLayout'
import { MessageComposer } from './MessageComposer'
import { MessageList } from './MessageList'
import type { ChatCaps } from './types'

// ThreadPanel is one thread: the root message, its replies, and a composer that
// posts replies (threadRootSeq set). It polls on the same 2s cadence as the
// channel while open.
//
// Two shells, same body. As a `panel` it is the right-hand column it has always
// been. As a `page` it takes the whole pane and the close button becomes a back
// arrow — for hosts too narrow to seat a thread beside the message list, where
// the split would leave the list unreadable (see chatLayout.ts). Only the
// wrapper and the header differ; the list and composer already fill whatever
// they are given.
export function ThreadPanel({
  projectId,
  channelId,
  rootSeq,
  active,
  live,
  meId,
  caps,
  archived,
  variant = 'panel',
  channelName,
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
  variant?: 'panel' | 'page'
  /** Named in the page title, so `back` says where it goes before you press it. */
  channelName?: string
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
  const page = variant === 'page'

  return (
    <Box
      sx={{
        ...(page
          ? { flex: 1, minWidth: 0 }
          : {
              // Prefers the width at which an attached document card (thumbnail
              // + name + location) reads comfortably, and gives back 40px under
              // pressure rather than making the message list absorb the whole
              // squeeze. It never grows: the extra belongs to the conversation.
              flex: `0 1 ${THREAD_BASIS}px`,
              minWidth: THREAD_MIN,
              borderLeft: 1,
              borderColor: 'divider',
            }),
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 0.5,
          px: page ? 0.5 : 1.5,
          py: 0.75,
          borderBottom: 1,
          borderColor: 'divider',
        }}
      >
        {page && (
          <IconButton size="small" onClick={onClose} aria-label={t('thread.back')}>
            <FontAwesomeIcon icon={faArrowLeft} size="xs" />
          </IconButton>
        )}
        <Typography variant="subtitle2" noWrap sx={{ flex: 1, minWidth: 0 }}>
          {page && channelName
            ? t('thread.titleIn', { channel: channelName })
            : t('thread.title')}
        </Typography>
        {!page && (
          <IconButton size="small" onClick={onClose} aria-label={t('thread.close')}>
            <FontAwesomeIcon icon={faXmark} size="xs" />
          </IconButton>
        )}
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
