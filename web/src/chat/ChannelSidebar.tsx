import { faHashtag, faLock } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Autocomplete,
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  TextField,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthMe, useChatMembers, useCreateChatChannel } from '../api/queries'
import { APP_RAIL_WIDTH } from '../components/Column'
import { RailHeader } from '../components/RailHeader'
import type { ChatCaps, ChatChannel, ChatMember } from './types'

// ChannelSidebar lists the channels the server says this user can see and
// hosts the create-channel dialog, including the private-channel member
// picker (the creator becomes owner; picked members join the ACL).
export function ChannelSidebar({
  projectId,
  channels,
  currentId,
  caps,
  unread,
  onSelect,
}: {
  projectId: string | null
  channels: ChatChannel[]
  currentId: string | null
  caps: ChatCaps
  // per-channel unread counts (server read cursors, kept live by
  // channel.activity / read.updated events); >0 renders bold with a badge.
  unread: Map<string, number>
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation('chat')
  const [createOpen, setCreateOpen] = useState(false)

  return (
    <Box
      sx={{
        width: APP_RAIL_WIDTH,
        flexShrink: 0,
        borderRight: 1,
        borderColor: 'divider',
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
      }}
    >
      <RailHeader
        title={t('sidebar.title')}
        onNew={() => setCreateOpen(true)}
        newDisabled={!caps.createChannel}
        newDisabledReason={t('sidebar.newDisabledReason')}
      />
      <List dense disablePadding sx={{ flex: 1, overflowY: 'auto' }}>
        {channels.map((ch) => {
          const count = unread.get(ch.id) ?? 0
          return (
            <ListItemButton
              key={ch.id}
              selected={ch.id === currentId}
              onClick={() => onSelect(ch.id)}
              sx={{ py: 0.25 }}
            >
              <ListItemIcon sx={{ minWidth: 26, fontSize: 12 }}>
                <FontAwesomeIcon icon={ch.isPrivate ? faLock : faHashtag} />
              </ListItemIcon>
              <ListItemText
                primary={ch.name}
                secondary={ch.archivedAt ? t('sidebar.archived') : undefined}
                primaryTypographyProps={{
                  noWrap: true,
                  fontSize: 14,
                  fontWeight: count > 0 ? 700 : undefined,
                }}
              />
              {count > 0 && (
                <Box
                  sx={{
                    px: 0.75,
                    minWidth: 18,
                    borderRadius: 9,
                    bgcolor: 'primary.main',
                    color: 'primary.contrastText',
                    fontSize: 11,
                    lineHeight: '18px',
                    textAlign: 'center',
                    flexShrink: 0,
                  }}
                >
                  {count > 99 ? '99+' : count}
                </Box>
              )}
            </ListItemButton>
          )
        })}
      </List>
      <CreateChannelDialog
        projectId={projectId}
        open={createOpen}
        onClose={() => setCreateOpen(false)}
      />
    </Box>
  )
}

function CreateChannelDialog({
  projectId,
  open,
  onClose,
}: {
  projectId: string | null
  open: boolean
  onClose: () => void
}) {
  const { t } = useTranslation('chat')
  const create = useCreateChatChannel(projectId)
  const [name, setName] = useState('')
  const [topic, setTopic] = useState('')
  const [isPrivate, setIsPrivate] = useState(false)
  const [members, setMembers] = useState<ChatMember[]>([])
  const [error, setError] = useState<string | null>(null)

  // The roster loads only once the private box is ticked; the creator is
  // implicit (they become the channel owner) and filtered from the picker.
  const meId = useAuthMe().data?.user?.id ?? ''
  const membersQ = useChatMembers(projectId, open && isPrivate)
  const candidates = (membersQ.data ?? []).filter((m) => m.userId !== meId)

  const close = () => {
    setName('')
    setTopic('')
    setIsPrivate(false)
    setMembers([])
    setError(null)
    onClose()
  }

  const submit = async () => {
    setError(null)
    try {
      await create.mutateAsync({
        name: name.trim(),
        topic: topic.trim() || undefined,
        isPrivate,
        memberIds: isPrivate ? members.map((m) => m.userId) : undefined,
      })
      close()
    } catch (e) {
      setError(e instanceof Error ? e.message : t('createDialog.createFailed'))
    }
  }

  return (
    <Dialog open={open} onClose={close} maxWidth="xs" fullWidth>
      <DialogTitle>{t('createDialog.title')}</DialogTitle>
      <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: '8px !important' }}>
        <TextField
          autoFocus
          label={t('createDialog.name')}
          size="small"
          value={name}
          onChange={(e) => setName(e.target.value)}
          inputProps={{ maxLength: 80 }}
        />
        <TextField
          label={t('createDialog.topicOptional')}
          size="small"
          value={topic}
          onChange={(e) => setTopic(e.target.value)}
          inputProps={{ maxLength: 500 }}
        />
        <FormControlLabel
          control={
            <Checkbox checked={isPrivate} onChange={(e) => setIsPrivate(e.target.checked)} />
          }
          label={t('createDialog.private')}
        />
        {isPrivate && (
          <Autocomplete
            multiple
            size="small"
            options={candidates}
            getOptionLabel={(m) => m.name || m.email || m.userId}
            isOptionEqualToValue={(a, b) => a.userId === b.userId}
            loading={membersQ.isLoading}
            value={members}
            onChange={(_, v) => setMembers(v)}
            renderInput={(params) => (
              <TextField
                {...params}
                label={t('createDialog.members')}
                placeholder={t('createDialog.membersPlaceholder')}
              />
            )}
          />
        )}
        {error && (
          <Typography variant="caption" color="error">
            {error}
          </Typography>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={close}>{t('common:cancel')}</Button>
        <Button
          variant="contained"
          disabled={!name.trim() || create.isPending}
          onClick={() => void submit()}
        >
          {t('common:create')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
