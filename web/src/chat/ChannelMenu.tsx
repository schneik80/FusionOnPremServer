import {
  faBoxArchive,
  faEllipsisVertical,
  faPen,
  faRightFromBracket,
  faUserGroup,
  faXmark,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Autocomplete,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  IconButton,
  List,
  ListItem,
  ListItemText,
  ListItemIcon,
  Menu,
  MenuItem,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useChatChannelAdmin, useChatMembers } from '../api/queries'
import { roleLabel } from '../i18n/enums'
import type { ChatCaps, ChatChannel } from './types'

// ChannelMenu is the per-channel management menu in the channel header
// (docs/chat/PLAN.md phase 5): rename/topic for moderators and the channel
// creator, private-channel member management, archive, and leave. The
// server is the authority on every rule here — the menu only hides actions
// that would certainly 403.
export function ChannelMenu({
  projectId,
  channel,
  caps,
  meId,
}: {
  projectId: string | null
  channel: ChatChannel
  caps: ChatCaps
  meId: string
}) {
  const { t } = useTranslation('chat')
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const [dialog, setDialog] = useState<'edit' | 'members' | 'archive' | null>(null)
  const admin = useChatChannelAdmin(projectId)

  const canManage = caps.moderate || (meId !== '' && channel.createdBy === meId)
  const isMember = meId !== '' && (channel.memberIds ?? []).includes(meId)
  const canLeave = channel.isPrivate && isMember && channel.createdBy !== meId
  const canArchive = canManage && !channel.isRoot && !channel.archivedAt
  const items: { label: string; icon: typeof faPen; onClick: () => void }[] = []
  if (canManage)
    items.push({ label: t('channelMenu.edit'), icon: faPen, onClick: () => setDialog('edit') })
  if (channel.isPrivate && canManage)
    items.push({ label: t('channelMenu.members'), icon: faUserGroup, onClick: () => setDialog('members') })
  if (canArchive)
    items.push({
      label: t('channelMenu.archive'),
      icon: faBoxArchive,
      onClick: () => setDialog('archive'),
    })
  if (canLeave)
    items.push({
      label: t('channelMenu.leave'),
      icon: faRightFromBracket,
      onClick: () => {
        if (projectId && meId) admin.removeMember.mutate({ channelId: channel.id, userId: meId })
      },
    })
  if (items.length === 0) return null

  return (
    <>
      <Tooltip title={t('channelMenu.options')}>
        <IconButton
          size="small"
          onClick={(e) => setAnchor(e.currentTarget)}
          aria-label={t('channelMenu.options')}
        >
          <FontAwesomeIcon icon={faEllipsisVertical} size="xs" />
        </IconButton>
      </Tooltip>
      <Menu open={anchor !== null} anchorEl={anchor} onClose={() => setAnchor(null)}>
        {items.map((it) => (
          <MenuItem
            key={it.label}
            onClick={() => {
              setAnchor(null)
              it.onClick()
            }}
          >
            <ListItemIcon sx={{ fontSize: 13 }}>
              <FontAwesomeIcon icon={it.icon} />
            </ListItemIcon>
            {it.label}
          </MenuItem>
        ))}
      </Menu>
      {dialog === 'edit' && (
        <EditChannelDialog
          projectId={projectId}
          channel={channel}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'members' && (
        <MembersDialog projectId={projectId} channel={channel} meId={meId} onClose={() => setDialog(null)} />
      )}
      <Dialog open={dialog === 'archive'} onClose={() => setDialog(null)} maxWidth="xs" fullWidth>
        <DialogTitle>{t('archiveDialog.title', { name: channel.name })}</DialogTitle>
        <DialogContent>
          <DialogContentText>{t('archiveDialog.body')}</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDialog(null)}>{t('common:cancel')}</Button>
          <Button
            color="warning"
            variant="contained"
            disabled={admin.archive.isPending}
            onClick={() => {
              admin.archive
                .mutateAsync(channel.id)
                .catch(() => {})
                .finally(() => setDialog(null))
            }}
          >
            {t('archiveDialog.confirm')}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  )
}

function EditChannelDialog({
  projectId,
  channel,
  onClose,
}: {
  projectId: string | null
  channel: ChatChannel
  onClose: () => void
}) {
  const { t } = useTranslation('chat')
  const admin = useChatChannelAdmin(projectId)
  const [name, setName] = useState(channel.name)
  const [topic, setTopic] = useState(channel.topic)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    setError(null)
    try {
      await admin.update.mutateAsync({
        channelId: channel.id,
        // The root channel's name is fixed server-side; don't send it.
        name: channel.isRoot ? undefined : name.trim(),
        topic: topic.trim(),
      })
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : t('editDialog.updateFailed'))
    }
  }

  return (
    <Dialog open onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('editDialog.title', { name: channel.name })}</DialogTitle>
      <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: '8px !important' }}>
        <TextField
          label={t('editDialog.name')}
          size="small"
          value={name}
          disabled={channel.isRoot}
          helperText={channel.isRoot ? t('editDialog.rootNoRename') : undefined}
          onChange={(e) => setName(e.target.value)}
          inputProps={{ maxLength: 80 }}
        />
        <TextField
          label={t('editDialog.topic')}
          size="small"
          value={topic}
          onChange={(e) => setTopic(e.target.value)}
          inputProps={{ maxLength: 500 }}
        />
        {error && (
          <Typography variant="caption" color="error">
            {error}
          </Typography>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common:cancel')}</Button>
        <Button
          variant="contained"
          disabled={(!channel.isRoot && !name.trim()) || admin.update.isPending}
          onClick={() => void submit()}
        >
          {t('common:save')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

function MembersDialog({
  projectId,
  channel,
  meId,
  onClose,
}: {
  projectId: string | null
  channel: ChatChannel
  meId: string
  onClose: () => void
}) {
  const { t } = useTranslation('chat')
  const admin = useChatChannelAdmin(projectId)
  const membersQ = useChatMembers(projectId, true)
  const roster = membersQ.data ?? []
  const [error, setError] = useState<string | null>(null)

  const inChannel = channel.memberIds ?? []
  const byId = new Map(roster.map((m) => [m.userId, m]))
  const candidates = roster.filter((m) => !inChannel.includes(m.userId))

  const act = (p: Promise<unknown>) => {
    setError(null)
    p.catch((e) => setError(e instanceof Error ? e.message : t('membersDialog.changeFailed')))
  }

  return (
    <Dialog open onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('membersDialog.title', { name: channel.name })}</DialogTitle>
      <DialogContent sx={{ pt: '8px !important' }}>
        <Autocomplete
          size="small"
          options={candidates}
          getOptionLabel={(m) => m.name || m.email || m.userId}
          loading={membersQ.isLoading}
          value={null}
          blurOnSelect
          onChange={(_, m) => {
            if (m) act(admin.addMember.mutateAsync({ channelId: channel.id, userId: m.userId }))
          }}
          renderInput={(params) => <TextField {...params} label={t('membersDialog.addMember')} />}
          noOptionsText={t('membersDialog.everyoneHere')}
        />
        <List dense>
          {inChannel.map((uid) => {
            const m = byId.get(uid)
            const isCreator = uid === channel.createdBy
            return (
              <ListItem
                key={uid}
                disableGutters
                secondaryAction={
                  // The creator/owner row is fixed — orphaning a private
                  // channel from its own management UI helps nobody.
                  !isCreator && (
                    <Tooltip
                      title={
                        uid === meId ? t('channelMenu.leave') : t('membersDialog.removeFromChannel')
                      }
                    >
                      <IconButton
                        size="small"
                        edge="end"
                        onClick={() =>
                          act(admin.removeMember.mutateAsync({ channelId: channel.id, userId: uid }))
                        }
                      >
                        <FontAwesomeIcon icon={faXmark} size="xs" />
                      </IconButton>
                    </Tooltip>
                  )
                }
              >
                <ListItemText
                  primary={m ? m.name || m.email || uid : uid}
                  secondary={
                    isCreator ? t('membersDialog.owner') : m ? roleLabel(t, m.role) : undefined
                  }
                />
              </ListItem>
            )
          })}
        </List>
        {error && (
          <Typography variant="caption" color="error">
            {error}
          </Typography>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('membersDialog.done')}</Button>
      </DialogActions>
    </Dialog>
  )
}
