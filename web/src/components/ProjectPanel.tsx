import { Box, Paper, Slide, Stack, Tab, Tabs } from '@mui/material'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useChatUnreads } from '../api/queries'
import { ChatApp } from '../chat/ChatApp'
import { useChatEvents } from '../chat/useChatEvents'
import { useNav } from '../state/nav'
import { ProductionApp } from '../production/ProductionApp'
import { TasksApp } from '../tasks/TasksApp'
import { WhiteboardsApp } from '../whiteboards/WhiteboardsApp'
import { WikiApp } from '../wiki/WikiApp'
import { ProjectDashboard } from './Dashboards'
import { TAB_SLIDE_TIMEOUT } from './motion'

// ProjectPanel is the project-level pane: a tab shell over the dashboard, the
// wiki, and chat. It replaces the bare <ProjectDashboard/> that used to fill
// slot B. Wiki and Chat are project-ROOT concepts — drilling into any folder
// hides them (they belong to the project, not a folder), and selected
// documents render DetailsPanel instead (see BrowserStage). It owns the same
// left-bordered Paper frame the dashboard used, so the slide swap to a
// document's DetailsPanel stays seamless.
//
// All tabs stay mounted (hidden ones are slid off-screen) so switching preserves
// the dashboard's scroll, the wiki editor's in-progress state, and the chat
// scroll/draft. Every tab therefore takes `active`, which gates its fetching to
// the visible tab — otherwise a hidden tab keeps spending APS quota. The chat
// SSE stream (below) lives regardless of the tab, keeping the chat caches warm
// from any tab.
//
// Changing tab cross-slides the content region, borrowing BrowserStage's idiom
// one level down: a clipped slot holds every pane absolutely positioned, and
// panes slide off-screen rather than unmounting, so "stays mounted" still holds.
// MUI marks a fully-exited pane `visibility: hidden`, which keeps off-screen
// panes out of paint, the a11y tree and the tab order — the job `display: none`
// used to do.

type ProjectTab = 'dashboard' | 'wiki' | 'chat' | 'tasks' | 'production' | 'whiteboards'

// Left-to-right order of the tab strip below; the slide direction is just the
// sign of the change in index, so this MUST match the <Tab> render order.
const TAB_ORDER: ProjectTab[] = ['dashboard', 'production', 'tasks', 'whiteboards', 'wiki', 'chat']


export function ProjectPanel() {
  const { t } = useTranslation('browse')
  const nav = useNav()
  const [tab, setTab] = useState<ProjectTab>('dashboard')

  // One SSE stream per open project, opened here (not inside the Chat tab) so
  // events keep the channel list and activity badges warm from any tab. `live`
  // demotes chat's polling to a fallback while the stream is healthy.
  const { live } = useChatEvents(nav.project?.id ?? null)

  // Unread total for the Chat tab badge — server read cursors, kept live by
  // the same stream (channel.activity / read.updated events).
  const unreadsQ = useChatUnreads(nav.project?.id ?? null, live)
  const totalUnread = (unreadsQ.data?.unreads ?? []).reduce((n, u) => n + u.unreadCount, 0)

  // Inside a folder the Wiki/Chat tabs are hidden, so the dashboard shows
  // regardless of the chosen tab. The choice itself is kept, not reset —
  // returning to the project root lands back where the user was.
  const atRoot = nav.folderStack.length === 0
  const effectiveTab: ProjectTab = atRoot ? tab : 'dashboard'

  // Moving rightwards along the strip slides content left (the new pane enters
  // from the right); moving leftwards reverses it. Compared against the previous
  // tab — read during render, committed after — exactly as BrowserStage derives
  // its drill-down direction.
  const index = TAB_ORDER.indexOf(effectiveTab)
  const prevIndex = useRef(index)
  const forward = index >= prevIndex.current
  useEffect(() => {
    prevIndex.current = index
  }, [index])

  // The slot node lives in state, not a ref, so each Slide's `container` (which
  // sets how far a pane travels off-screen) is defined on the first transition.
  const [slot, setSlot] = useState<HTMLDivElement | null>(null)

  // MUI's direction is the direction of travel: "left" = enter from the right.
  const dir = (show: boolean): 'left' | 'right' =>
    forward ? (show ? 'left' : 'right') : show ? 'right' : 'left'

  const pane = (name: ProjectTab, node: ReactNode) => (
    <Slide
      key={name}
      direction={dir(effectiveTab === name)}
      in={effectiveTab === name}
      container={slot}
      appear={false}
      mountOnEnter={false}
      unmountOnExit={false}
      timeout={TAB_SLIDE_TIMEOUT}
    >
      <Box sx={{ position: 'absolute', inset: 0, display: 'flex' }}>{node}</Box>
    </Slide>
  )

  return (
    <Paper
      square
      variant="outlined"
      sx={{
        flex: 1,
        minWidth: 320,
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        borderTop: 0,
        borderBottom: 0,
        borderRight: 0,
        overflow: 'hidden',
      }}
    >
      <Tabs
        value={effectiveTab}
        onChange={(_, v) => setTab(v as ProjectTab)}
        sx={{
          minHeight: 40,
          px: 1,
          borderBottom: 1,
          borderColor: 'divider',
          '& .MuiTab-root': { minHeight: 40, py: 0, textTransform: 'none' },
        }}
      >
        <Tab label={t('projectPanel.tabs.dashboard')} value="dashboard" />
        {atRoot && <Tab label={t('projectPanel.tabs.production')} value="production" />}
        {atRoot && <Tab label={t('projectPanel.tabs.tasks')} value="tasks" />}
        {atRoot && <Tab label={t('projectPanel.tabs.whiteboards')} value="whiteboards" />}
        {atRoot && <Tab label={t('projectPanel.tabs.wiki')} value="wiki" />}
        {atRoot && (
          <Tab
            value="chat"
            label={
              totalUnread > 0 ? (
                <Stack direction="row" spacing={0.75} alignItems="center">
                  <span>{t('projectPanel.tabs.chat')}</span>
                  <Box
                    component="span"
                    sx={{
                      px: 0.75,
                      minWidth: 18,
                      borderRadius: 9,
                      bgcolor: 'primary.main',
                      color: 'primary.contrastText',
                      fontSize: 11,
                      lineHeight: '18px',
                      textAlign: 'center',
                    }}
                  >
                    {totalUnread > 99 ? '99+' : totalUnread}
                  </Box>
                </Stack>
              ) : (
                t('projectPanel.tabs.chat')
              )
            }
          />
        )}
      </Tabs>
      <Box ref={setSlot} sx={{ flex: 1, minHeight: 0, position: 'relative', overflow: 'hidden' }}>
        {pane('dashboard', <ProjectDashboard active={effectiveTab === 'dashboard'} />)}
        {pane('production', <ProductionApp active={effectiveTab === 'production'} />)}
        {pane('tasks', <TasksApp active={effectiveTab === 'tasks'} />)}
        {pane('whiteboards', <WhiteboardsApp active={effectiveTab === 'whiteboards'} />)}
        {pane('wiki', <WikiApp active={effectiveTab === 'wiki'} />)}
        {pane('chat', <ChatApp active={effectiveTab === 'chat'} live={live} />)}
      </Box>
    </Paper>
  )
}
