import { faChartGantt, faPlus, faTableColumns, faTableList } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
} from '@mui/material'
import { useState } from 'react'
import { useTasks } from '../api/queries'
import { useNav } from '../state/nav'
import { TaskGantt } from './gantt/TaskGantt'
import type { TimeUnit } from './gantt/ganttMath'
import { TaskEditDialog } from './TaskEditDialog'
import { TaskKanban } from './TaskKanban'
import { TaskListView } from './TaskListView'

// TasksApp is the project-tab task manager (WikiApp/ChatApp contract:
// `active` gates fetching to the visible tab). Three views over the same
// query: list-with-details, a Kanban board and a Gantt schedule. Creation
// lives here — the create button is disabled (with the reason) for read-only
// roles instead of letting the POST bounce off the 403 (composer precedent).
export function TasksApp({ active = true }: { active?: boolean }) {
  const nav = useNav()
  const projectId = nav.project?.id ?? null
  const tasksQ = useTasks(projectId, active)

  const [view, setView] = useState<'list' | 'board' | 'gantt'>('list')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  // The Gantt zoom unit lives here so it survives view switches (the view
  // itself remounts like list/board).
  const [ganttUnit, setGanttUnit] = useState<TimeUnit>('week')

  const tasks = tasksQ.data?.tasks ?? []
  const caps = tasksQ.data?.capabilities
  const canWrite = caps?.write ?? false

  if (!projectId) return null

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
      <Stack
        direction="row"
        alignItems="center"
        spacing={1}
        sx={{ px: 1, py: 0.75, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
      >
        <ToggleButtonGroup
          size="small"
          exclusive
          value={view}
          onChange={(_, v) => v && setView(v)}
          sx={{ '& .MuiToggleButton-root': { py: 0.25, px: 1, textTransform: 'none' } }}
        >
          <ToggleButton value="list">
            <FontAwesomeIcon icon={faTableList} style={{ fontSize: 13, marginRight: 6 }} />
            List
          </ToggleButton>
          <ToggleButton value="board">
            <FontAwesomeIcon icon={faTableColumns} style={{ fontSize: 13, marginRight: 6 }} />
            Board
          </ToggleButton>
          <ToggleButton value="gantt">
            <FontAwesomeIcon icon={faChartGantt} style={{ fontSize: 13, marginRight: 6 }} />
            Gantt
          </ToggleButton>
        </ToggleButtonGroup>
        <Box sx={{ flex: 1 }} />
        {/* Matches RailHeader's action (same size/variant/icon/label) but lives
            here rather than in the rail: the Board view has no rail, and
            TaskKanban carries no create affordance, so a rail-only button would
            be unreachable there. */}
        <Tooltip
          title={
            canWrite || tasksQ.isLoading
              ? ''
              : 'Your project role is read-only — creating tasks needs Editor access'
          }
        >
          <span>
            <Button
              size="small"
              variant="contained"
              disabled={!canWrite}
              startIcon={<FontAwesomeIcon icon={faPlus} style={{ fontSize: 11 }} />}
              onClick={() => setCreateOpen(true)}
              sx={{ py: 0.25, textTransform: 'none' }}
            >
              New
            </Button>
          </span>
        </Tooltip>
      </Stack>

      {view === 'list' ? (
        <TaskListView
          tasks={tasks}
          caps={caps}
          loading={tasksQ.isLoading}
          error={tasksQ.error as Error | null}
          selectedId={selectedId}
          onSelect={setSelectedId}
        />
      ) : view === 'board' ? (
        <TaskKanban
          projectId={projectId}
          tasks={tasks}
          caps={caps}
          loading={tasksQ.isLoading}
          error={tasksQ.error as Error | null}
        />
      ) : (
        <TaskGantt
          projectId={projectId}
          tasks={tasks}
          caps={caps}
          loading={tasksQ.isLoading}
          error={tasksQ.error as Error | null}
          unit={ganttUnit}
          onUnitChange={setGanttUnit}
        />
      )}

      {createOpen && nav.project && (
        <TaskEditDialog
          open={createOpen}
          onClose={() => setCreateOpen(false)}
          projectId={projectId}
          hubId={nav.hubId ?? ''}
          projectName={nav.project.name}
          onSaved={(t) => {
            setSelectedId(t.id)
          }}
        />
      )}
    </Box>
  )
}
