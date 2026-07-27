import {
  faAt,
  faBell,
  faCheckDouble,
  faClock,
  faComments,
  faDiagramProject,
  faListCheck,
  faTriangleExclamation,
  faXmark,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Divider,
  IconButton,
  List,
  ListItem,
  ListItemButton,
  Popover,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material'
import type { IconDefinition } from '@fortawesome/fontawesome-svg-core'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { useNotificationActions, useNotifications, useProjects } from '../../api/queries'
import { fmtRelative } from '../../fmt'
import { useNav } from '../../state/nav'
import type { Item } from '../../api/types'
import type { Notification, NotifKind } from '../../notifications/types'

// The tab a notification's kind deep-links to inside the target project's
// panel (see ProjectPanel's ProjectTab). Mentions land on chat; task kinds on
// tasks; production on production.
const KIND_TAB: Record<NotifKind, string> = {
  mention: 'chat',
  assigned: 'tasks',
  due_soon: 'tasks',
  overdue: 'tasks',
  production: 'production',
  chat_unread: 'chat',
}

const KIND_ICON: Record<NotifKind, IconDefinition> = {
  mention: faAt,
  assigned: faListCheck,
  due_soon: faClock,
  overdue: faTriangleExclamation,
  production: faDiagramProject,
  chat_unread: faComments,
}

// notifText composes the localized one-line summary from the kind plus the
// captured params. Actor names and subjects are user data, interpolated
// verbatim (React escapes the resulting text node); the sentence frame is
// what localizes.
function notifText(t: TFunction, n: Notification): string {
  const actor = n.actor?.name || n.actor?.email || t('notifications:unknownUser')
  const subject = n.subject || ''
  switch (n.kind) {
    case 'mention':
      return n.channelName
        ? t('notifications:text.mention', { actor, channel: n.channelName })
        : t('notifications:text.mention_nochannel', { actor })
    case 'assigned':
      return n.actor
        ? t('notifications:text.assigned', { actor, subject })
        : t('notifications:text.assigned_noactor', { subject })
    case 'due_soon':
      return t('notifications:text.due_soon', { subject })
    case 'overdue':
      return t('notifications:text.overdue', { subject })
    case 'production':
      return n.actor
        ? t('notifications:text.production', { actor, subject })
        : t('notifications:text.production_noactor', { subject })
    case 'chat_unread':
      return t('notifications:text.chat_unread', {
        count: n.count ?? 0,
        channel: n.channelName || subject,
      })
    default:
      return subject
  }
}

// NotificationBell is the app-chrome inbox: a bell with an unread-count pill
// that opens a popover list of the caller's notifications. Clicking a row
// marks it read and best-effort navigates to the project it references.
export function NotificationBell() {
  const { t } = useTranslation(['notifications', 'browse'])
  const nav = useNav()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const open = Boolean(anchor)
  // Poll while the chrome is mounted; the popover being open isn't required
  // for the badge to stay current.
  const q = useNotifications(true)
  const { markRead, markAllRead, dismiss } = useNotificationActions()
  // Resolve a notification's project to a real nav Item for click-through.
  // Notifications are per-hub (session-locked), so the current hub's project
  // list is the right lookup table.
  const projects = useProjects(nav.hubId)
  const projectById = useMemo(() => {
    const m = new Map<string, Item>()
    for (const p of projects.data ?? []) m.set(p.id, p)
    return m
  }, [projects.data])

  const list = q.data?.notifications ?? []
  const unread = q.data?.unread ?? 0

  const onRowClick = (n: Notification) => {
    // A derived row has no store id to mark: it clears itself once the channel
    // it points at has been read.
    if (!n.read && !n.derived) markRead.mutate([n.id])
    const project = n.projectId ? projectById.get(n.projectId) : undefined
    if (project) {
      nav.navigate(project, [], null, KIND_TAB[n.kind])
      setAnchor(null)
    }
  }

  return (
    <>
      <Tooltip title={t('notifications:bell.title')}>
        <Box sx={{ position: 'relative', display: 'inline-flex' }}>
          <IconButton
            aria-label={t('notifications:bell.aria')}
            onClick={(e) => setAnchor(e.currentTarget)}
            sx={{ color: 'text.secondary' }}
          >
            <FontAwesomeIcon icon={faBell} style={{ fontSize: 16 }} />
          </IconButton>
          {unread > 0 && (
            <Box
              sx={{
                position: 'absolute',
                top: 2,
                right: 2,
                px: 0.5,
                minWidth: 16,
                height: 16,
                borderRadius: 8,
                bgcolor: 'error.main',
                color: 'error.contrastText',
                fontSize: 10,
                fontWeight: 700,
                lineHeight: '16px',
                textAlign: 'center',
                pointerEvents: 'none',
              }}
            >
              {unread > 99 ? '99+' : unread}
            </Box>
          )}
        </Box>
      </Tooltip>
      <Popover
        open={open}
        anchorEl={anchor}
        onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        slotProps={{ paper: { sx: { width: 380, maxWidth: '90vw' } } }}
      >
        <Stack
          direction="row"
          alignItems="center"
          sx={{ px: 1.5, py: 1, gap: 1 }}
        >
          <Typography variant="subtitle2" sx={{ flex: 1 }}>
            {t('notifications:bell.title')}
          </Typography>
          {unread > 0 && (
            <Tooltip title={t('notifications:bell.markAllRead')}>
              <IconButton
                size="small"
                aria-label={t('notifications:bell.markAllRead')}
                onClick={() => markAllRead.mutate()}
                sx={{ color: 'text.secondary' }}
              >
                <FontAwesomeIcon icon={faCheckDouble} style={{ fontSize: 13 }} />
              </IconButton>
            </Tooltip>
          )}
        </Stack>
        <Divider />
        {list.length === 0 ? (
          <Typography
            variant="body2"
            color="text.secondary"
            sx={{ px: 2, py: 4, textAlign: 'center' }}
          >
            {t('notifications:bell.empty')}
          </Typography>
        ) : (
          <List disablePadding sx={{ maxHeight: 420, overflowY: 'auto' }}>
            {list.map((n) => (
              <ListItem
                key={n.id}
                disablePadding
                // A derived row has nothing to dismiss — it exists only while
                // the channel it names has unread messages, and reading them is
                // what removes it. Offering an X that did nothing would be worse
                // than offering none.
                secondaryAction={
                  n.derived ? undefined : (
                  <Tooltip title={t('notifications:bell.dismiss')}>
                    <IconButton
                      edge="end"
                      size="small"
                      aria-label={t('notifications:bell.dismiss')}
                      onClick={(e) => {
                        e.stopPropagation()
                        dismiss.mutate(n.id)
                      }}
                      sx={{ color: 'text.secondary' }}
                    >
                      <FontAwesomeIcon icon={faXmark} style={{ fontSize: 12 }} />
                    </IconButton>
                  </Tooltip>
                  )
                }
              >
                <ListItemButton
                  onClick={() => onRowClick(n)}
                  sx={{ alignItems: 'flex-start', gap: 1, py: 1, pr: 5 }}
                >
                  <Box
                    sx={{
                      mt: 0.25,
                      color: n.read ? 'text.disabled' : 'primary.main',
                      width: 18,
                      flexShrink: 0,
                      textAlign: 'center',
                    }}
                  >
                    <FontAwesomeIcon icon={KIND_ICON[n.kind] ?? faBell} style={{ fontSize: 14 }} />
                  </Box>
                  <Stack sx={{ minWidth: 0, flex: 1 }}>
                    <Typography
                      variant="body2"
                      sx={{ fontWeight: n.read ? 400 : 600, whiteSpace: 'normal' }}
                    >
                      {notifText(t, n)}
                    </Typography>
                    <Typography variant="caption" color="text.secondary">
                      {[n.projectName, fmtRelative(n.createdAt)].filter(Boolean).join(' · ')}
                    </Typography>
                  </Stack>
                  {!n.read && (
                    <Box
                      sx={{
                        mt: 0.75,
                        width: 8,
                        height: 8,
                        borderRadius: '50%',
                        bgcolor: 'primary.main',
                        flexShrink: 0,
                      }}
                    />
                  )}
                </ListItemButton>
              </ListItem>
            ))}
          </List>
        )}
      </Popover>
    </>
  )
}
