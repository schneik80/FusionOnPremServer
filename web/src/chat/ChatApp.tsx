import { faHashtag, faLock } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Alert, Box, CircularProgress, Stack, Typography } from '@mui/material'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  useAuthMe,
  useChatChannels,
  useChatMessages,
  useChatMutations,
  useChatUnreads,
  useMarkChatRead,
} from '../api/queries'
import { useNav } from '../state/nav'
import { ChannelMenu } from './ChannelMenu'
import { ChannelSidebar } from './ChannelSidebar'
import { MessageComposer } from './MessageComposer'
import { MessageList } from './MessageList'
import { ThreadPanel } from './ThreadPanel'
import { TypingIndicator } from './TypingIndicator'
import { useTypingNames, useTypingPing } from './typing'
import type { ChatCaps } from './types'
import { localizeApiError } from '../i18n/apiError'

const NO_CAPS: ChatCaps = { post: false, createChannel: false, moderate: false }

// ChatApp is the Chat tab's content: channel sidebar, message timeline,
// composer, and an optional thread panel. Transport is the project's SSE
// stream (opened by ProjectPanel; `live` reports its health) writing into
// the react-query caches; the 2s polling from phase 1 survives only as the
// fallback while the stream is down. `active` gates fetching to the
// visible tab.
export function ChatApp({ active, live }: { active: boolean; live: boolean }) {
  const { t } = useTranslation('chat')
  const nav = useNav()
  const projectId = nav.project?.id ?? null
  const meId = useAuthMe().data?.user?.id ?? ''

  const channelsQ = useChatChannels(projectId, active, live)
  const channels = channelsQ.data?.channels ?? []
  const caps = channelsQ.data?.capabilities ?? NO_CAPS

  const [channelId, setChannelId] = useState<string | null>(null)
  const [threadRoot, setThreadRoot] = useState<number | null>(null)
  const current =
    channels.find((c) => c.id === channelId) ?? channels.find((c) => c.isRoot) ?? channels[0] ?? null
  const archived = !!current?.archivedAt

  // Selection resets when the project changes; the thread panel also closes
  // when switching channels.
  useEffect(() => {
    setChannelId(null)
    setThreadRoot(null)
  }, [projectId])
  useEffect(() => {
    setThreadRoot(null)
  }, [current?.id])

  const messagesQ = useChatMessages(projectId, current?.id ?? null, active, live)
  const { send, sending, remove, react } = useChatMutations(projectId, current?.id ?? null)

  // Server-backed read cursors (phase 4): viewing a channel marks it read
  // up to the newest fetched seq. The ref dedupes — one PATCH per new seq,
  // not one per render — and the server ignores non-advancing marks anyway.
  const latestSeq = messagesQ.data?.latestSeq ?? 0
  const currentId = current?.id
  const unreadsQ = useChatUnreads(projectId, live)
  const { mutate: markRead } = useMarkChatRead(projectId)
  const markedRef = useRef<{ cid: string; seq: number }>({ cid: '', seq: 0 })
  useEffect(() => {
    if (!active || !currentId || latestSeq <= 0) return
    const marked = markedRef.current
    if (marked.cid === currentId && marked.seq >= latestSeq) return
    markedRef.current = { cid: currentId, seq: latestSeq }
    markRead({ channelId: currentId, lastReadSeq: latestSeq })
  }, [active, currentId, latestSeq, markRead])

  // The badge for the channel being viewed lags the mark-read roundtrip by
  // a beat; suppress it locally so reading never shows as unread.
  const unread = new Map<string, number>()
  for (const u of unreadsQ.data?.unreads ?? []) {
    if (u.channelId !== currentId && u.unreadCount > 0) unread.set(u.channelId, u.unreadCount)
  }

  const typingNames = useTypingNames(projectId, current?.id ?? null, meId)
  const onTyping = useTypingPing(projectId, current?.id ?? null)

  const doDelete = (seq: number) => void remove.mutateAsync(seq).catch(() => {})
  const doToggleReaction = (seq: number, emoji: string, on: boolean) =>
    void react.mutateAsync({ seq, emoji, on }).catch(() => {})

  if (channelsQ.isError) {
    return (
      <Box sx={{ flex: 1, p: 2 }}>
        <Alert severity="warning">
          {t('unavailable', {
            message:
              channelsQ.error ? localizeApiError(t, channelsQ.error) : t('unknownError'),
          })}
        </Alert>
      </Box>
    )
  }
  if (!channelsQ.data) {
    return (
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <CircularProgress size={28} />
      </Box>
    )
  }

  return (
    <Box sx={{ flex: 1, display: 'flex', minHeight: 0, minWidth: 0 }}>
      <ChannelSidebar
        projectId={projectId}
        channels={channels}
        currentId={current?.id ?? null}
        caps={caps}
        unread={unread}
        onSelect={setChannelId}
      />
      <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 0 }}>
        {current && (
          <Stack
            direction="row"
            spacing={1}
            alignItems="baseline"
            sx={{ px: 1.5, py: 0.75, borderBottom: 1, borderColor: 'divider' }}
          >
            <Typography variant="subtitle2" sx={{ fontSize: 12 }}>
              <FontAwesomeIcon icon={current.isPrivate ? faLock : faHashtag} />
            </Typography>
            <Typography variant="subtitle2">{current.name}</Typography>
            {current.topic && (
              <Typography variant="caption" color="text.secondary" noWrap>
                {current.topic}
              </Typography>
            )}
            {archived && (
              <Typography variant="caption" color="warning.main">
                {t('header.archived')}
              </Typography>
            )}
            <Box sx={{ flex: 1 }} />
            {!live && (
              <Typography variant="caption" color="text.disabled">
                {t('header.reconnecting')}
              </Typography>
            )}
            <ChannelMenu projectId={projectId} channel={current} caps={caps} meId={meId} />
          </Stack>
        )}
        <MessageList
          messages={(messagesQ.data?.messages ?? []).filter((m) => !m.threadRoot)}
          meId={meId}
          caps={caps}
          emptyText={messagesQ.isLoading ? t('common:loading') : t('emptyState.noMessages')}
          onOpenThread={setThreadRoot}
          onDelete={doDelete}
          onToggleReaction={doToggleReaction}
        />
        <TypingIndicator names={typingNames} />
        <MessageComposer
          placeholder={
            current
              ? t('composer.placeholderChannel', { name: current.name })
              : t('composer.placeholder')
          }
          disabled={!caps.post || archived || !current}
          disabledReason={
            archived ? t('composer.disabledArchived') : t('composer.disabledReadOnly')
          }
          sending={sending}
          onSend={(body) => send(body)}
          onTyping={onTyping}
        />
      </Box>
      {threadRoot !== null && current && (
        <ThreadPanel
          projectId={projectId}
          channelId={current.id}
          rootSeq={threadRoot}
          active={active}
          live={live}
          meId={meId}
          caps={caps}
          archived={archived}
          onClose={() => setThreadRoot(null)}
          onSend={(body, root) => send(body, root)}
          onDelete={doDelete}
          onToggleReaction={doToggleReaction}
          onTyping={onTyping}
          sending={sending}
        />
      )}
    </Box>
  )
}
