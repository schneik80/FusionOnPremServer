import { faComments, faFaceSmile, faTrash } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, Chip, IconButton, Popover, Stack, Tooltip, Typography } from '@mui/material'
import UserAvatar from '../components/UserAvatar'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { DocumentCard } from '../components/doccard/DocumentCard'
import { ProductionCard } from '../components/productioncard/ProductionCard'
import { splitRefTokens } from '../components/reftokens'
import { splitMentions } from '../notifications/mentions'
import { TaskCard } from '../components/taskcard/TaskCard'
import { WhiteboardCard } from '../components/whiteboardcard/WhiteboardCard'
import { ImageCard } from '../components/imgcard/ImageCard'
import { REACTION_EMOJI, type ChatCaps, type ChatMessage } from './types'
import { fmtChatTime } from './fmt'

// MessageList renders a scrollable, ascending timeline. It backs both the
// channel view (top-level messages with thread badges) and the thread panel
// (root + replies, no badges). New messages keep the view pinned to the
// bottom unless the user has scrolled up to read history. Bodies render as
// plain text only (React escaping; no HTML/markdown) — see the XSS notes in
// docs/chat/PLAN.md.
export function MessageList({
  messages,
  meId,
  caps,
  emptyText,
  onOpenThread,
  onDelete,
  onToggleReaction,
}: {
  messages: ChatMessage[]
  meId: string
  caps: ChatCaps
  emptyText: string
  onOpenThread?: (rootSeq: number) => void
  onDelete: (seq: number) => void
  onToggleReaction: (seq: number, emoji: string, on: boolean) => void
}) {
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const pinnedRef = useRef(true)

  useEffect(() => {
    const el = scrollRef.current
    if (el && pinnedRef.current) el.scrollTop = el.scrollHeight
  }, [messages.length])

  return (
    <Box
      ref={scrollRef}
      onScroll={(e) => {
        const el = e.currentTarget
        pinnedRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
      }}
      sx={{ flex: 1, minHeight: 0, overflowY: 'auto', px: 1.5, py: 1 }}
    >
      {messages.length === 0 && (
        <Typography variant="body2" color="text.secondary" sx={{ p: 2, textAlign: 'center' }}>
          {emptyText}
        </Typography>
      )}
      {messages.map((m) => (
        <MessageRow
          key={m.seq}
          msg={m}
          meId={meId}
          canModerate={caps.moderate}
          canReact={caps.post}
          onOpenThread={onOpenThread}
          onDelete={onDelete}
          onToggleReaction={onToggleReaction}
        />
      ))}
    </Box>
  )
}

// MentionText renders a plain text run, highlighting any @mention tokens
// (fls:user) as inline "@Name" chips. Mentions sit in the text runs that
// survive splitRefTokens (which only splits card tokens), so cards and
// mentions compose without either eating the other's tokens.
function MentionText({ text }: { text: string }) {
  const parts = useMemo(() => splitMentions(text), [text])
  if (parts.length === 1 && 'text' in parts[0]) return <>{text}</>
  return (
    <>
      {parts.map((p, i) =>
        'mention' in p ? (
          <MentionChip key={i} name={p.mention.name} />
        ) : (
          <span key={i}>{p.text}</span>
        ),
      )}
    </>
  )
}

function MentionChip({ name }: { name: string }) {
  const { t } = useTranslation('chat')
  return (
    <Box
      component="span"
      sx={{
        color: 'primary.main',
        fontWeight: 600,
        bgcolor: 'action.selected',
        borderRadius: 0.5,
        px: 0.25,
      }}
    >
      {t('mention.at', { name: name || t('mention.unknown') })}
    </Box>
  )
}

// ChatBody renders a message body, unfurling any fls:doc tokens into
// DocumentCards, fls:task into TaskCards, fls:job/fls:batch into
// ProductionCards and fls:whiteboard into WhiteboardCards; @mention tokens
// highlight inline. Everything else stays
// plain text (React-escaped — the chat deliberately renders no
// HTML/markdown; a card is a React component, not injected markup, so the
// XSS posture is unchanged).
function ChatBody({ body }: { body: string }) {
  const parts = useMemo(() => splitRefTokens(body), [body])
  return (
    <>
      {parts.map((p, i) =>
        'doc' in p ? (
          <DocumentCard key={i} docRef={p.doc} />
        ) : 'task' in p ? (
          <TaskCard key={i} taskRef={p.task} />
        ) : 'batch' in p ? (
          <ProductionCard key={i} jobRef={p.batch} batchRef={p.batch} />
        ) : 'job' in p ? (
          <ProductionCard key={i} jobRef={p.job} />
        ) : 'whiteboard' in p ? (
          <WhiteboardCard key={i} whiteboardRef={p.whiteboard} />
        ) : 'img' in p ? (
          <ImageCard key={i} imgRef={p.img} />
        ) : (
          <MentionText key={i} text={p.text} />
        ),
      )}
    </>
  )
}

function MessageRow({
  msg,
  meId,
  canModerate,
  canReact,
  onOpenThread,
  onDelete,
  onToggleReaction,
}: {
  msg: ChatMessage
  meId: string
  canModerate: boolean
  canReact: boolean
  onOpenThread?: (rootSeq: number) => void
  onDelete: (seq: number) => void
  onToggleReaction: (seq: number, emoji: string, on: boolean) => void
}) {
  const { t } = useTranslation('chat')
  const own = meId !== '' && msg.authorId === meId
  const [pickerAnchor, setPickerAnchor] = useState<HTMLElement | null>(null)

  // Group reactions into per-emoji chips, marking the ones I placed.
  const grouped = new Map<string, { count: number; mine: boolean }>()
  for (const r of msg.reactions) {
    const g = grouped.get(r.emoji) ?? { count: 0, mine: false }
    g.count++
    if (meId !== '' && r.userId === meId) g.mine = true
    grouped.set(r.emoji, g)
  }

  return (
    <Stack
      direction="row"
      spacing={1.25}
      sx={{
        py: 0.75,
        px: 0.5,
        borderRadius: 1,
        // Optimistic copies render dimmed until the server echo replaces them.
        opacity: msg.pending ? 0.55 : 1,
        '&:hover': { bgcolor: 'action.hover' },
        '&:hover .msg-actions': { opacity: 1 },
      }}
    >
      <UserAvatar id={msg.authorId} name={msg.authorName} size={28} sx={{ mt: 0.25 }} />
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Stack direction="row" spacing={1} alignItems="baseline">
          <Typography variant="subtitle2" noWrap>
            {msg.authorName || t('message.unknownAuthor')}
          </Typography>
          <Typography variant="caption" color="text.secondary">
            {fmtChatTime(msg.createdAt)}
          </Typography>
          {msg.editedAt && !msg.deleted && (
            <Typography variant="caption" color="text.secondary">
              {t('message.edited')}
            </Typography>
          )}
          <Box sx={{ flex: 1 }} />
          <Stack
            direction="row"
            className="msg-actions"
            sx={{ opacity: 0, transition: 'opacity 120ms' }}
          >
            {canReact && !msg.deleted && !msg.pending && (
              <Tooltip title={t('message.addReaction')}>
                <IconButton size="small" onClick={(e) => setPickerAnchor(e.currentTarget)}>
                  <FontAwesomeIcon icon={faFaceSmile} size="xs" />
                </IconButton>
              </Tooltip>
            )}
            {onOpenThread && !msg.deleted && !msg.pending && (
              <Tooltip title={t('message.replyInThread')}>
                <IconButton size="small" onClick={() => onOpenThread(msg.seq)}>
                  <FontAwesomeIcon icon={faComments} size="xs" />
                </IconButton>
              </Tooltip>
            )}
            {(own || canModerate) && !msg.deleted && !msg.pending && (
              <Tooltip title={t('message.delete')}>
                <IconButton size="small" onClick={() => onDelete(msg.seq)}>
                  <FontAwesomeIcon icon={faTrash} size="xs" />
                </IconButton>
              </Tooltip>
            )}
          </Stack>
        </Stack>
        {msg.deleted ? (
          <Typography variant="body2" color="text.disabled" fontStyle="italic">
            {t('message.deleted')}
          </Typography>
        ) : (
          <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
            <ChatBody body={msg.body} />
          </Typography>
        )}
        {grouped.size > 0 && (
          <Stack direction="row" spacing={0.5} sx={{ mt: 0.5, flexWrap: 'wrap' }}>
            {[...grouped.entries()].map(([emoji, g]) => (
              <Chip
                key={emoji}
                size="small"
                variant={g.mine ? 'filled' : 'outlined'}
                label={`${emoji} ${g.count}`}
                onClick={canReact ? () => onToggleReaction(msg.seq, emoji, !g.mine) : undefined}
              />
            ))}
          </Stack>
        )}
        <Popover
          open={pickerAnchor !== null}
          anchorEl={pickerAnchor}
          onClose={() => setPickerAnchor(null)}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        >
          <Stack direction="row" sx={{ p: 0.5, maxWidth: 220, flexWrap: 'wrap' }}>
            {REACTION_EMOJI.map((emoji) => (
              <IconButton
                key={emoji}
                size="small"
                sx={{ fontSize: 18, borderRadius: 1 }}
                onClick={() => {
                  onToggleReaction(msg.seq, emoji, !grouped.get(emoji)?.mine)
                  setPickerAnchor(null)
                }}
              >
                {emoji}
              </IconButton>
            ))}
          </Stack>
        </Popover>
        {onOpenThread && msg.replyCount > 0 && (
          <Chip
            size="small"
            variant="outlined"
            icon={<FontAwesomeIcon icon={faComments} size="xs" />}
            label={t('message.replies', { count: msg.replyCount })}
            onClick={() => onOpenThread(msg.seq)}
            // Secondary text rather than the accent: the accent-on-outlined
            // chip read as low-contrast against the message body. Hovering
            // lifts it to primary text so it still announces it's clickable.
            sx={{
              mt: 0.5,
              color: 'text.secondary',
              borderColor: 'divider',
              transition: 'color 120ms, border-color 120ms',
              '& .MuiChip-icon': { color: 'inherit' },
              '&:hover': { color: 'text.primary', borderColor: 'text.secondary' },
            }}
          />
        )}
      </Box>
    </Stack>
  )
}
