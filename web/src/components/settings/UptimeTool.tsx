import { Alert, Box, CircularProgress, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useAdminStatus } from '../../api/queries'
import { fmtBytes, fmtDate, getNumberFmt } from '../../fmt'

// UptimeTool: live process status (GET /api/admin/status), polled every 30s
// while this screen is visible.

// humanizeUptime renders whole seconds as "3d 4h 12m" (largest three units;
// sub-minute uptimes show as "0m"). Unit letters are non-translating glyphs.
function humanizeUptime(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds))
  const d = Math.floor(s / 86_400)
  const h = Math.floor((s % 86_400) / 3_600)
  const m = Math.floor((s % 3_600) / 60)
  const parts: string[] = []
  if (d > 0) parts.push(`${d}d`)
  if (d > 0 || h > 0) parts.push(`${h}h`)
  parts.push(`${m}m`)
  return parts.join(' ')
}

export function UptimeTool({ active }: { active: boolean }) {
  const { t } = useTranslation('settings')
  const statusQ = useAdminStatus(active)
  const status = statusQ.data

  if (statusQ.isLoading) {
    return (
      <Box sx={{ p: 2, textAlign: 'center' }}>
        <CircularProgress size={18} />
      </Box>
    )
  }
  if (statusQ.error) {
    return (
      <Alert severity="error" variant="outlined">
        {(statusQ.error as Error).message}
      </Alert>
    )
  }
  if (!status) return null

  const rows: { label: string; value: string }[] = [
    {
      label: t('uptime.startedAt'),
      value: fmtDate(status.startedAt, { dateStyle: 'medium', timeStyle: 'short' }),
    },
    { label: t('uptime.uptime'), value: humanizeUptime(status.uptimeSeconds) },
    { label: t('uptime.requests'), value: getNumberFmt().format(status.requestsServed) },
    { label: t('uptime.appVersion'), value: status.version || '—' },
    { label: t('uptime.goVersion'), value: status.goVersion || '—' },
    { label: t('uptime.logSize'), value: status.logSizeBytes ? fmtBytes(status.logSizeBytes) : '—' },
  ]

  return (
    <Stack spacing={1.5} sx={{ maxWidth: 480 }}>
      {rows.map(({ label, value }) => (
        <Stack key={label} direction="row" spacing={2} alignItems="baseline">
          <Typography variant="body2" color="text.secondary" sx={{ width: 160, flexShrink: 0 }}>
            {label}
          </Typography>
          <Typography variant="body2">{value}</Typography>
        </Stack>
      ))}
    </Stack>
  )
}
