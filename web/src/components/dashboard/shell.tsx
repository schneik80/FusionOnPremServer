import { Box, CircularProgress, Paper, Stack, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import type { ReactNode } from 'react'

// Shared dashboard primitives, lifted out of Dashboards.tsx so the hub
// dashboard's own components (components/hub/*) can reuse the exact same chrome
// as the project dashboard without a circular import. Pure presentation — no
// data or app-state dependencies — so both callers stay decoupled.

export interface Stat {
  label: string
  value: ReactNode
}

export function DashboardShell({
  title,
  subtitle,
  stats,
  action,
  children,
  fill,
}: {
  title: string
  subtitle?: string
  stats?: Stat[]
  // Optional control rendered at the top-right of the header row (the hub
  // dashboard's Overview/Explore view toggle). The project dashboard passes
  // nothing, so its header is unchanged.
  action?: ReactNode
  children?: ReactNode
  // When true the content area is a full-height flex column and the children
  // wrapper grows, so a widget marked to fill can consume the pane's leftover
  // vertical space (the project activity chart). Off by default: the hub
  // dashboard just flows and scrolls.
  fill?: boolean
}) {
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
      <Box
        sx={{
          flex: 1,
          overflowY: 'auto',
          p: 2,
          ...(fill ? { display: 'flex', flexDirection: 'column', minHeight: 0 } : {}),
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1.5 }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography variant="h6" noWrap title={title} sx={{ fontWeight: 600, lineHeight: 1.2 }}>
              {title}
            </Typography>
            {subtitle && (
              <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
                {subtitle}
              </Typography>
            )}
          </Box>
          {action && <Box sx={{ flexShrink: 0 }}>{action}</Box>}
        </Box>
        {stats && stats.length > 0 && (
          <Stack direction="row" spacing={1.5} sx={{ flexWrap: 'wrap', rowGap: 1.5, mt: 1.5 }}>
            {stats.map((s) => (
              <StatCard key={s.label} label={s.label} value={s.value} />
            ))}
          </Stack>
        )}
        {children && (
          <Box
            sx={{
              mt: 1.5,
              ...(fill ? { flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' } : {}),
            }}
          >
            {children}
          </Box>
        )}
      </Box>
    </Paper>
  )
}

export function StatCard({ label, value }: Stat) {
  const theme = useTheme()
  return (
    <Box
      sx={{
        minWidth: 110,
        px: 2,
        py: 1.25,
        borderRadius: 1.5,
        border: 1,
        borderColor: 'divider',
        bgcolor: alpha(theme.palette.primary.main, 0.08),
      }}
    >
      <Typography variant="h5" sx={{ fontWeight: 700, lineHeight: 1.1 }}>
        {value}
      </Typography>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ textTransform: 'uppercase', letterSpacing: 0.5 }}
      >
        {label}
      </Typography>
    </Box>
  )
}

export function WidgetCard({
  title,
  span,
  fill,
  children,
}: {
  title: string
  span?: boolean
  // When true the card grows to fill its flex parent and its content area
  // becomes a flex column, so a chart inside can fill the card's height.
  fill?: boolean
  children: ReactNode
}) {
  return (
    <Box
      sx={{
        border: 1,
        borderColor: 'divider',
        borderRadius: 1.5,
        p: 1.5,
        gridColumn: span ? '1 / -1' : undefined,
        ...(fill ? { flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' } : {}),
      }}
    >
      <Typography variant="overline" color="text.secondary" sx={{ display: 'block', mb: 1, lineHeight: 1.4 }}>
        {title}
      </Typography>
      {fill ? (
        <Box sx={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>{children}</Box>
      ) : (
        children
      )}
    </Box>
  )
}

export function Hint({ children }: { children: ReactNode }) {
  return (
    <Typography variant="body2" color="text.secondary" sx={{ py: 2, textAlign: 'center' }}>
      {children}
    </Typography>
  )
}

export const spinner = <CircularProgress size={18} />

// The categorical chart palette (Atlassian-ish), shared by the project
// dashboard's type donut and the hub dashboard's multi-series charts. Assign in
// fixed order, never cycled beyond its length.
export const DONUT_PALETTE = ['#0696d7', '#36b37e', '#ff8b00', '#6554c0', '#ff5630', '#00b8d9', '#8993a4', '#e91e63']
