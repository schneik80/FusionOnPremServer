import {
  Box,
  CircularProgress,
  List,
  Paper,
  Typography,
} from '@mui/material'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { localizeApiError } from '../i18n/apiError'

// The width of a project app's left-hand list rail — every tab in ProjectPanel
// that has one: Tasks, Wiki, Production, Whiteboards and Chat. They had drifted
// to 320/240/260/240/200, so the pane jumped whenever you changed tab. Defined
// once so it can only ever drift on purpose.
//
// 260 is the middle of that range: wide enough for the densest rows (a task's
// "id · status · assignee · due" meta line), narrow enough to leave the detail
// pane — a kanban board, a wiki page, a flow canvas, a whiteboard — the room.
export const APP_RAIL_WIDTH = 260

interface ColumnProps {
  title: string
  width?: number | string
  flex?: number
  loading?: boolean
  error?: Error | null
  empty?: boolean
  emptyText?: string
  // Optional control rendered at the right of the header row (e.g. a sort menu).
  action?: ReactNode
  children?: ReactNode
}

// Column is the shared shell for the Projects and Contents panes: a titled,
// scrollable surface that renders a centered spinner while loading, the error
// message on failure, an empty-state hint, or its row children.
export function Column({
  title,
  width,
  flex,
  loading,
  error,
  empty,
  emptyText,
  action,
  children,
}: ColumnProps) {
  const { t } = useTranslation('browse')
  return (
    <Paper
      square
      variant="outlined"
      sx={{
        width,
        flex,
        minWidth: 0,
        display: 'flex',
        flexDirection: 'column',
        borderTop: 0,
        borderBottom: 0,
        borderLeft: 0,
        overflow: 'hidden',
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 1,
          borderBottom: 1,
          borderColor: 'divider',
          flexShrink: 0,
          pr: action ? 0.5 : 0,
        }}
      >
        <Typography
          variant="subtitle2"
          sx={{
            px: 1.5,
            py: 1,
            color: 'text.secondary',
            textTransform: 'uppercase',
            letterSpacing: 0.5,
            fontSize: 11,
          }}
        >
          {title}
        </Typography>
        {action}
      </Box>
      <Box sx={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
        {loading ? (
          <Centered>
            <CircularProgress size={22} />
          </Centered>
        ) : error ? (
          <Centered>
            <Typography variant="body2" color="error" sx={{ px: 2, textAlign: 'center' }}>
              {localizeApiError(t, error)}
            </Typography>
          </Centered>
        ) : empty ? (
          <Centered>
            <Typography variant="body2" color="text.secondary">
              {emptyText ?? t('column.emptyDefault')}
            </Typography>
          </Centered>
        ) : (
          <List dense disablePadding>
            {children}
          </List>
        )}
      </Box>
    </Paper>
  )
}

function Centered({ children }: { children: ReactNode }) {
  return (
    <Box
      sx={{
        height: '100%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        p: 2,
      }}
    >
      {children}
    </Box>
  )
}
