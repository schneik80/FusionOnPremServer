import { faMagnifyingGlass } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  CircularProgress,
  InputAdornment,
  List,
  ListSubheader,
  MenuItem,
  Paper,
  Stack,
  TextField,
  Typography,
} from '@mui/material'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMyTasks } from '../api/queries'
import { taskPriorityLabel, taskStatusLabel } from '../i18n/enums'
import { TaskDetails } from './TaskDetails'
import { TaskRow } from './TaskListView'
import {
  PRIORITIES,
  STATUSES,
  type Task,
  type TaskPriority,
  type TaskStatus,
} from './types'

type StatusFilter = 'open' | 'all' | TaskStatus
type PriorityFilter = 'all' | TaskPriority

// TasksScreen is the rail's top-level Tasks app: every task assigned to or
// created by the signed-in user, across all projects on this server, with
// search and filters (all client-side — the whole list is one small
// response). Selection shows the shared TaskDetails pane. Editing here
// can't know the caller's per-project role cheaply, so write affordances
// stay enabled and a 403 surfaces from the server.
export function TasksScreen({ active }: { active: boolean }) {
  const { t } = useTranslation('tasks')
  const myQ = useMyTasks(active)
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState<StatusFilter>('open')
  const [priority, setPriority] = useState<PriorityFilter>('all')
  const [projectId, setProjectId] = useState<string>('all')
  const [selectedKey, setSelectedKey] = useState<string | null>(null)

  const tasks = useMemo(() => myQ.data?.tasks ?? [], [myQ.data])

  // Project filter options, from the data itself.
  const projects = useMemo(() => {
    const seen = new Map<string, string>()
    for (const t of tasks) if (!seen.has(t.projectId)) seen.set(t.projectId, t.projectName)
    return [...seen.entries()].sort((a, b) => a[1].localeCompare(b[1]))
  }, [tasks])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return tasks.filter((t) => {
      if (status === 'open' ? t.status === 'done' : status !== 'all' && t.status !== status) return false
      if (priority !== 'all' && t.priority !== priority) return false
      if (projectId !== 'all' && t.projectId !== projectId) return false
      if (q && !`${t.title}\n${t.description ?? ''}`.toLowerCase().includes(q)) return false
      return true
    })
  }, [tasks, search, status, priority, projectId])

  // Group by project, tasks newest-first within each.
  const groups = useMemo(() => {
    const m = new Map<string, { name: string; tasks: Task[] }>()
    for (const t of filtered) {
      const g = m.get(t.projectId) ?? { name: t.projectName, tasks: [] }
      g.tasks.push(t)
      m.set(t.projectId, g)
    }
    for (const g of m.values()) g.tasks.sort((a, b) => b.num - a.num)
    return [...m.values()].sort((a, b) => a.name.localeCompare(b.name))
  }, [filtered])

  const selected = filtered.find((t) => taskKey(t) === selectedKey) ?? null

  return (
    <Box sx={{ flex: 1, minHeight: 0, display: 'flex' }}>
      <Paper
        square
        variant="outlined"
        sx={{
          width: 360,
          flexShrink: 0,
          display: 'flex',
          flexDirection: 'column',
          borderTop: 0,
          borderBottom: 0,
          borderLeft: 0,
        }}
      >
        <Stack spacing={1} sx={{ p: 1, borderBottom: 1, borderColor: 'divider' }}>
          <TextField
            size="small"
            placeholder={t('screen.searchPlaceholder')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            InputProps={{
              startAdornment: (
                <InputAdornment position="start">
                  <FontAwesomeIcon icon={faMagnifyingGlass} style={{ fontSize: 13 }} />
                </InputAdornment>
              ),
            }}
          />
          <Stack direction="row" spacing={1}>
            <TextField
              select
              size="small"
              label={t('screen.statusFilterLabel')}
              value={status}
              onChange={(e) => setStatus(e.target.value as StatusFilter)}
              sx={{ flex: 1 }}
            >
              <MenuItem value="open">{t('screen.filterOpen')}</MenuItem>
              <MenuItem value="all">{t('screen.filterAll')}</MenuItem>
              {STATUSES.map((s) => (
                <MenuItem key={s} value={s}>
                  {taskStatusLabel(t, s)}
                </MenuItem>
              ))}
            </TextField>
            <TextField
              select
              size="small"
              label={t('screen.priorityFilterLabel')}
              value={priority}
              onChange={(e) => setPriority(e.target.value as PriorityFilter)}
              sx={{ flex: 1 }}
            >
              <MenuItem value="all">{t('screen.filterAll')}</MenuItem>
              {PRIORITIES.map((p) => (
                <MenuItem key={p} value={p}>
                  {taskPriorityLabel(t, p)}
                </MenuItem>
              ))}
            </TextField>
          </Stack>
          <TextField
            select
            size="small"
            label={t('screen.projectFilterLabel')}
            value={projectId}
            onChange={(e) => setProjectId(e.target.value)}
          >
            <MenuItem value="all">{t('screen.allProjects')}</MenuItem>
            {projects.map(([id, name]) => (
              <MenuItem key={id} value={id}>
                {name}
              </MenuItem>
            ))}
          </TextField>
        </Stack>

        <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
          {myQ.isLoading ? (
            <Centered>
              <CircularProgress size={22} />
            </Centered>
          ) : myQ.error ? (
            <Centered>
              <Typography variant="body2" color="error" sx={{ px: 2, textAlign: 'center' }}>
                {(myQ.error as Error).message}
              </Typography>
            </Centered>
          ) : filtered.length === 0 ? (
            <Centered>
              <Typography variant="body2" color="text.secondary" sx={{ px: 2, textAlign: 'center' }}>
                {tasks.length === 0 ? t('screen.emptyNoTasks') : t('screen.emptyFiltered')}
              </Typography>
            </Centered>
          ) : (
            <List dense disablePadding>
              {groups.map((g) => (
                <Box key={g.name}>
                  <ListSubheader
                    sx={{
                      lineHeight: '28px',
                      bgcolor: 'background.default',
                      textTransform: 'uppercase',
                      fontSize: 11,
                      letterSpacing: 0.5,
                    }}
                  >
                    {g.name}
                  </ListSubheader>
                  {g.tasks.map((t) => (
                    <TaskRow
                      key={taskKey(t)}
                      task={t}
                      selected={taskKey(t) === selectedKey}
                      onClick={() => setSelectedKey(taskKey(t))}
                    />
                  ))}
                </Box>
              ))}
            </List>
          )}
        </Box>
      </Paper>

      <Box sx={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        {selected ? (
          <TaskDetails key={taskKey(selected)} task={selected} onDeleted={() => setSelectedKey(null)} />
        ) : (
          <Centered>
            <Typography variant="body2" color="text.secondary">
              {t('list.selectPrompt')}
            </Typography>
          </Centered>
        )}
      </Box>
    </Box>
  )
}

// Task ids are per-project ("t1"), so cross-project keys need both parts.
function taskKey(t: Task): string {
  return `${t.projectId}::${t.id}`
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <Box sx={{ flex: 1, height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', p: 2 }}>
      {children}
    </Box>
  )
}
