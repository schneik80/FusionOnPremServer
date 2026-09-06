import { Box, Slide } from '@mui/material'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useNav } from '../state/nav'
import { ContentsColumn } from './ContentsColumn'
import { DetailsPanel } from './DetailsPanel'
import { ProjectsColumn } from './ProjectsColumn'
import { HubDashboard } from './Dashboards'
import { ProjectPanel } from './ProjectPanel'

// BrowserStage is the progressive drill-down browser. The level is derived
// purely from nav state, and two clipped "slots" cross-slide their panes:
//   • Slot A (fixed width): Projects list ⟷ Contents
//   • Slot B (flex):        Hub Dashboard ⟷ Project Dashboard ⟷ Details
// Panes stay mounted (slid off-screen) so scroll position, selection, and query
// state survive transitions; an absolute-positioned pane + overflow-hidden slot
// lets the outgoing and incoming panes overlap without reflow.

type Level = 'hub' | 'project' | 'document'
const DEPTH: Record<Level, number> = { hub: 0, project: 1, document: 2 }
const SLOT_A_WIDTH = 320

export function BrowserStage() {
  const nav = useNav()
  const level: Level = nav.project === null ? 'hub' : nav.selected === null ? 'project' : 'document'
  // The browser app itself can be hidden behind Tasks / Production (display:none).
  const shown = nav.app === 'browser'

  // Drilling deeper shifts panes left (new enters from the right, old exits
  // left); drilling shallower reverses (shift right). We compare against the
  // previous level — read during render, committed after — to pick the direction.
  const prevDepth = useRef(DEPTH[level])
  const deeper = DEPTH[level] >= prevDepth.current
  useEffect(() => {
    prevDepth.current = DEPTH[level]
  }, [level])

  // Hold the slot DOM nodes in state so each Slide's `container` (which sets how
  // far a pane translates off-screen) is defined on the first transition.
  const [slotA, setSlotA] = useState<HTMLDivElement | null>(null)
  const [slotB, setSlotB] = useState<HTMLDivElement | null>(null)

  // MUI Slide direction is the direction of motion: "left" = enter from the
  // right / exit left; "right" = enter from the left / exit right.
  const dir = (show: boolean): 'left' | 'right' =>
    deeper ? (show ? 'left' : 'right') : show ? 'right' : 'left'

  const pane = (show: boolean, container: HTMLElement | null, node: ReactNode) => (
    <Slide
      direction={dir(show)}
      in={show}
      container={container}
      appear={false}
      mountOnEnter={false}
      unmountOnExit={false}
    >
      <Box sx={{ position: 'absolute', inset: 0, display: 'flex' }}>{node}</Box>
    </Slide>
  )

  return (
    <Box sx={{ display: 'flex', flex: 1, minHeight: 0 }}>
      <Box
        ref={setSlotA}
        sx={{ position: 'relative', overflow: 'hidden', width: SLOT_A_WIDTH, flexShrink: 0 }}
      >
        {pane(level === 'hub', slotA, <ProjectsColumn />)}
        {pane(level !== 'hub', slotA, <ContentsColumn />)}
      </Box>
      <Box ref={setSlotB} sx={{ position: 'relative', overflow: 'hidden', flex: 1, minWidth: 0 }}>
        {/* A pane that slid away stays mounted (so coming back is instant) but
            must stop fetching and polling: `active` says whether it is the one
            on screen AND the browser app is showing at all. */}
        {pane(level === 'hub', slotB, <HubDashboard active={shown && level === 'hub'} />)}
        {pane(level === 'project', slotB, <ProjectPanel active={shown && level === 'project'} />)}
        {pane(level === 'document', slotB, <DetailsPanel active={shown && level === 'document'} />)}
      </Box>
    </Box>
  )
}
