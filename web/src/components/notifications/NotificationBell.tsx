import {
  faAt,
  faBell,
  faBoxArchive,
  faCheckDouble,
  faClock,
  faComments,
  faDiagramProject,
  faListCheck,
  faPlugCircleXmark,
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
import { useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { useNotificationActions, useNotifications, useProjects } from '../../api/queries'
import { fmtRelative } from '../../fmt'
import { useNav } from '../../state/nav'
import type { Item } from '../../api/types'
import type { Notification, NotifKind } from '../../notifications/types'
import { ArchiveReadyLink } from '../DocumentActions'

// The tab a notification's kind deep-links to inside the target project's
// panel (see ProjectPanel's ProjectTab). Mentions land on chat; task kinds on
// tasks; production on production.
//
// The document kinds are absent on purpose: an archive resolves to a download
// and a failed Fusion action resolves to nothing at all, so neither has a
// project tab to open. Rows whose kind is missing here simply don't navigate.
const KIND_TAB: Partial<Record<NotifKind, string>> = {
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
  archive: faBoxArchive,
  archive_failed: faTriangleExclamation,
  fusion_failed: faPlugCircleXmark,
  chat_unread: faComments,
}

// archiveJobId reads the job id out of an 'archive' row's ref token
// ("fls:archive?id=<jobId>"). Returns null for anything else, so a row with a
// missing or malformed ref degrades to a plain, unclickable entry rather than
// producing a broken download link.
function archiveJobId(ref?: string): string | null {
  if (!ref?.startsWith('fls:archive?')) return null
  const id = new URLSearchParams(ref.slice('fls:archive?'.length)).get('id')
  return id || null
}

// fusionFailureCode reads the outcome code out of a 'fusion_failed' row's ref
// ("fls:fusion?action=open&code=fusion_not_running").
function fusionFailureCode(ref?: string): { action: string; code: string } | null {
  if (!ref?.startsWith('fls:fusion?')) return null
  const q = new URLSearchParams(ref.slice('fls:fusion?'.length))
  return { action: q.get('action') || 'open', code: q.get('code') || '' }
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
    case 'archive':
      return t('notifications:text.archive', { subject })
    case 'archive_failed':
      return t('notifications:text.archive_failed', { subject })
    case 'fusion_failed': {
      // The reason is an enum token from the server, localized through the
      // errors catalog; an unrecognized one degrades to the bare sentence
      // rather than rendering the raw token at the user.
      const f = fusionFailureCode(n.ref)
      const key =
        f?.action === 'insert'
          ? 'notifications:text.fusion_failed_insert'
          : 'notifications:text.fusion_failed_open'
      const reason = f?.code ? t(`errors:${f.code}`, { defaultValue: '' }) : ''
      const line = t(key, { subject })
      return reason ? `${line} — ${reason}` : line
    }
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
  const { t } = useTranslation(['notifications', 'browse', 'errors'])
  const nav = useNav()
  const qc = useQueryClient()
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
    // Kinds that resolve to something other than a project tab are handled
    // first: a ready archive is a download (the row's own link button does the
    // transfer), and a failed Fusion action has nowhere to go.
    const tab = KIND_TAB[n.kind]
    if (!tab) return
    const project = n.projectId ? projectById.get(n.projectId) : undefined
    if (project) {
      // openProjectApp, not navigate: navigate's `tab` is a DOCUMENT's Details
      // tab, read only by DetailsPanel and only when something is selected —
      // which it isn't here, so this deep-link used to land on the dashboard
      // every time.
      nav.openProjectApp(project, tab)
      setAnchor(null)
    }
  }

  return (
    <>
      <Tooltip title={t('notifications:bell.title')}>
        <Box sx={{ position: 'relative', display: 'inline-flex' }}>
          <IconButton
            aria-label={t('notifications:bell.aria')}
            onClick={(e) => {
              setAnchor(e.currentTarget)
              // Opening the bell is the moment its contents matter most, and
              // the poll behind it is a 45-second backstop. Without this the
              // list can be most of a minute old exactly when it is being read.
              void q.refetch()
              // The archive rows' download buttons are enabled from the job
              // list, whose polling stops once nothing is in flight — so it
              // needs the same nudge, or a row can offer (or refuse) a
              // download based on minutes-old state.
              void qc.invalidateQueries({ queryKey: ['archives'] })
            }}
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
        <Stack direction="row" alignItems="center" sx={{ px: 1.5, py: 1, gap: 1 }}>
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
            {list.map((n) => {
              // A ready archive gets a download button beside the dismiss X,
              // and the row needs room for both.
              const archiveId = archiveJobId(n.ref)
              return (
                <ListItem
                  key={n.id}
                  disablePadding
                  // A derived row has nothing to dismiss — it exists only while
                  // the channel it names has unread messages, and reading them is
                  // what removes it. Offering an X that did nothing would be worse
                  // than offering none.
                  secondaryAction={
                    n.derived ? undefined : (
                      <Stack direction="row" sx={{ alignItems: 'center' }}>
                        {archiveId && (
                          <ArchiveReadyLink
                            id={archiveId}
                            docName={n.subject || ''}
                            label={t('notifications:bell.downloadArchive')}
                            // Downloading IS acting on the notification. The
                            // button stops propagation so the row's own click
                            // never fires, which left a row you had already
                            // acted on still showing as unread.
                            onDownload={() => {
                              if (!n.read) markRead.mutate([n.id])
                            }}
                          />
                        )}
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
                      </Stack>
                    )
                  }
                >
                  <ListItemButton
                    onClick={() => onRowClick(n)}
                    sx={{
                      alignItems: 'flex-start',
                      gap: 1,
                      py: 1,
                      pr: archiveId ? 9 : 5,
                    }}
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
                      <FontAwesomeIcon
                        icon={KIND_ICON[n.kind] ?? faBell}
                        style={{ fontSize: 14 }}
                      />
                    </Box>
                    <Stack sx={{ minWidth: 0, flex: 1 }}>
                      <Typography
                        variant="body2"
                        sx={{
                          fontWeight: n.read ? 400 : 600,
                          whiteSpace: 'normal',
                        }}
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
              )
            })}
          </List>
        )}
      </Popover>
    </>
  )
}
