import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faCloudArrowUp } from '@fortawesome/free-solid-svg-icons'
import { Box, Button, LinearProgress, Paper, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { isActiveUpload, useUploads } from '../state/uploads'

// UploadFooter is the persistent overlay shown while background uploads exist:
// an aggregate progress bar with counts, a button into the job view (the upload
// dialog), and cancel/dismiss. It floats above the browser panes but below
// modals, and disappears once the job list is dismissed or pruned.

export function UploadFooter() {
  const { t } = useTranslation('browse')
  const up = useUploads()
  if (up.jobs.length === 0 && !up.submitting) return null

  const active = up.jobs.filter(isActiveUpload)
  const done = up.jobs.filter((j) => j.status === 'done')
  const failed = up.jobs.filter((j) => j.status === 'error' || j.status === 'canceled')

  const totalBytes = up.jobs.reduce((s, j) => s + j.size, 0)
  const sentBytes = up.jobs.reduce(
    (s, j) => s + (j.status === 'done' ? j.size : Math.min(j.bytesSent, j.size)),
    0,
  )
  const pct = totalBytes > 0 ? Math.round((sentBytes / totalBytes) * 100) : 0

  const busy = active.length > 0 || up.submitting
  const summary =
    up.submitting && up.jobs.length === 0
      ? t('uploadFooter.starting')
      : busy
        ? t('uploadFooter.progress', {
            current: Math.min(done.length + 1, up.jobs.length),
            total: `${up.jobs.length}${up.submitting ? '+' : ''}`,
            pct,
          })
        : failed.length > 0
          ? t('uploadFooter.doneAndFailed', { done: done.length, failed: failed.length })
          : t('uploadFooter.filesUploaded', { count: done.length })

  const cancelAll = () => active.forEach((j) => up.cancelJob(j.id))

  return (
    <Paper
      elevation={6}
      sx={{
        position: 'fixed',
        left: '50%',
        bottom: 16,
        transform: 'translateX(-50%)',
        zIndex: (t) => t.zIndex.modal - 1,
        px: 2,
        py: 1,
        width: 380,
        maxWidth: 'calc(100vw - 32px)',
        borderRadius: 2,
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <FontAwesomeIcon icon={faCloudArrowUp} style={{ fontSize: 14, opacity: 0.6 }} />
        <Typography variant="body2" noWrap sx={{ flex: 1, minWidth: 0 }}>
          {summary}
        </Typography>
        <Button size="small" onClick={up.openDialog}>
          {t('uploadFooter.view')}
        </Button>
        {busy ? (
          <Button size="small" color="inherit" onClick={cancelAll} disabled={active.length === 0}>
            {t('common:cancel')}
          </Button>
        ) : (
          <Button size="small" color="inherit" onClick={() => up.dismissFinished()}>
            {t('uploadFooter.dismiss')}
          </Button>
        )}
      </Box>
      {busy && (
        <LinearProgress
          variant="determinate"
          value={pct}
          sx={{ mt: 0.75, height: 5, borderRadius: 2.5 }}
        />
      )}
    </Paper>
  )
}

// UploadDropOverlay is the full-window visual shown while files are dragged
// over the app with a valid target folder. Purely visual (pointer-events off) —
// the actual drop is handled by the window listeners in UploadsProvider.
export function UploadDropOverlay() {
  const { t } = useTranslation('browse')
  const up = useUploads()
  if (!up.dragActive || !up.target) return null
  return (
    <Box
      sx={{
        position: 'fixed',
        inset: 0,
        zIndex: (t) => t.zIndex.modal + 1,
        pointerEvents: 'none',
        bgcolor: 'rgba(0,0,0,0.45)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <Paper
        elevation={8}
        sx={{
          border: '2px dashed',
          borderColor: 'primary.main',
          borderRadius: 2,
          px: 5,
          py: 4,
          textAlign: 'center',
        }}
      >
        <FontAwesomeIcon icon={faCloudArrowUp} style={{ fontSize: 34, opacity: 0.7 }} />
        <Typography variant="h6" sx={{ mt: 1 }}>
          {t('uploadFooter.dropTitle')}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {t('uploadFooter.dropTarget', { target: up.target.label })}
        </Typography>
      </Paper>
    </Box>
  )
}
