import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faArrowsRotate, faUpRightFromSquare } from '@fortawesome/free-solid-svg-icons'
import { Alert, Box, Button, CircularProgress, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useAdminLogTail, useAdminStatus } from '../../api/queries'
import { fmtBytes } from '../../fmt'

// LogsTool: the server log's recent tail (GET /api/admin/log?tailBytes=…)
// with a full-file download escape hatch (?download=1, a navigation so the
// browser handles the attachment).

export function LogsTool({ active }: { active: boolean }) {
  const { t } = useTranslation('settings')
  const tailQ = useAdminLogTail(active)
  const statusQ = useAdminStatus(active)
  const status = statusQ.data

  return (
    <Stack spacing={1.5} sx={{ height: '100%', minHeight: 0 }}>
      <Stack direction="row" spacing={1} alignItems="center" sx={{ flexShrink: 0 }}>
        <Button
          size="small"
          variant="outlined"
          startIcon={<FontAwesomeIcon icon={faArrowsRotate} style={{ fontSize: 11 }} />}
          disabled={tailQ.isFetching}
          onClick={() => void tailQ.refetch()}
        >
          {t('logs.refresh')}
        </Button>
        <Button
          size="small"
          variant="outlined"
          startIcon={<FontAwesomeIcon icon={faUpRightFromSquare} style={{ fontSize: 11 }} />}
          onClick={() => window.open('/api/admin/log?download=1')}
        >
          {t('logs.openFull')}
        </Button>
      </Stack>
      {status?.logPath && (
        <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0, wordBreak: 'break-all' }}>
          {t('logs.path', {
            path: status.logPath,
            size: status.logSizeBytes ? fmtBytes(status.logSizeBytes) : '—',
          })}
        </Typography>
      )}
      {tailQ.isLoading ? (
        <Box sx={{ p: 2, textAlign: 'center' }}>
          <CircularProgress size={18} />
        </Box>
      ) : tailQ.error ? (
        <Alert severity="error" variant="outlined">
          {(tailQ.error as Error).message}
        </Alert>
      ) : (
        <Box
          component="pre"
          sx={{
            m: 0,
            p: 1.5,
            flex: 1,
            minHeight: 0,
            maxHeight: '100%',
            overflow: 'auto',
            fontFamily: 'monospace',
            fontSize: 11,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            bgcolor: 'background.default',
            border: 1,
            borderColor: 'divider',
            borderRadius: 1,
          }}
        >
          {tailQ.data || t('logs.empty')}
        </Box>
      )}
    </Stack>
  )
}
