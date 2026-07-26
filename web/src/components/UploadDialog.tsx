import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faBan,
  faCheck,
  faCloudArrowUp,
  faFile,
  faTriangleExclamation,
  faXmark,
} from '@fortawesome/free-solid-svg-icons'
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  LinearProgress,
  Tooltip,
  Typography,
} from '@mui/material'
import { useRef, type DragEvent } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import type { UploadJob } from '../api/types'
import { isActiveUpload, useUploads } from '../state/uploads'

// UploadDialog is the upload lightbox: a drop target + file browser that builds
// the list of files to send, and — once jobs exist — the job view showing each
// background upload's progress. Uploads keep running with the dialog closed;
// the footer overlay re-opens it.

export function fmtBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return ''
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n
  let i = -1
  do {
    v /= 1024
    i += 1
  } while (v >= 1024 && i < units.length - 1)
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

function jobProgress(j: UploadJob): number {
  if (j.status === 'done') return 100
  if (j.size <= 0) return 0
  return Math.min(100, (j.bytesSent / j.size) * 100)
}

function JobRow({ job }: { job: UploadJob }) {
  const { t } = useTranslation('browse')
  const up = useUploads()
  const active = isActiveUpload(job)
  // Localize the terminal statuses shown as the row caption; unknown values
  // degrade to the raw token.
  const statusLabel = (s: UploadJob['status']) => {
    const key = `upload.status.${s}`
    const out = t(key)
    return out === key ? s : out
  }
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, py: 0.5 }}>
      <Box
        sx={{
          width: 16,
          textAlign: 'center',
          flexShrink: 0,
          color:
            job.status === 'done'
              ? 'success.main'
              : job.status === 'error'
                ? 'error.main'
                : 'text.secondary',
        }}
      >
        {job.status === 'done' ? (
          <FontAwesomeIcon icon={faCheck} style={{ fontSize: 12 }} />
        ) : job.status === 'error' ? (
          <Tooltip title={job.error ?? t('upload.failedFallback')}>
            <FontAwesomeIcon icon={faTriangleExclamation} style={{ fontSize: 12 }} />
          </Tooltip>
        ) : job.status === 'canceled' ? (
          <FontAwesomeIcon icon={faBan} style={{ fontSize: 12 }} />
        ) : (
          <FontAwesomeIcon icon={faFile} style={{ fontSize: 12 }} />
        )}
      </Box>
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Box sx={{ display: 'flex', justifyContent: 'space-between', gap: 1 }}>
          <Typography variant="body2" noWrap title={job.fileName}>
            {job.fileName}
          </Typography>
          <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0 }}>
            {job.status === 'uploading'
              ? `${fmtBytes(job.bytesSent)} / ${fmtBytes(job.size)}`
              : statusLabel(job.status)}
          </Typography>
        </Box>
        {active && (
          <LinearProgress
            variant="determinate"
            value={jobProgress(job)}
            sx={{ mt: 0.25, height: 4, borderRadius: 2 }}
          />
        )}
        {job.status === 'error' && job.error && (
          <Typography variant="caption" color="error" noWrap title={job.error} sx={{ display: 'block' }}>
            {job.error}
          </Typography>
        )}
      </Box>
      <Tooltip title={active ? t('upload.cancelUpload') : t('upload.removeFromList')}>
        <IconButton
          size="small"
          aria-label={
            active
              ? t('upload.cancelUploadOf', { name: job.fileName })
              : t('upload.dismissFile', { name: job.fileName })
          }
          onClick={() => (active ? up.cancelJob(job.id) : up.dismissFinished(job.id))}
          sx={{ color: 'text.secondary', flexShrink: 0 }}
        >
          <FontAwesomeIcon icon={faXmark} style={{ fontSize: 12 }} />
        </IconButton>
      </Tooltip>
    </Box>
  )
}

export function UploadDialog() {
  const { t } = useTranslation('browse')
  const up = useUploads()
  const inputRef = useRef<HTMLInputElement>(null)

  // The dialog's own drop zone: stop propagation so the window-level handler
  // (which would add the files and re-open the dialog) doesn't fire too.
  const onZoneDragOver = (e: DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
  }
  const onZoneDrop = (e: DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    up.addFiles(Array.from(e.dataTransfer?.files ?? []))
  }

  const finished = up.jobs.filter((j) => !isActiveUpload(j))
  const canUpload = !!up.target && up.pending.length > 0 && !up.submitting
  const n = up.pending.length
  const uploadLabel = up.submitting
    ? t('upload.uploading')
    : n === 0
      ? t('upload.upload')
      : t('upload.uploadCount', { count: n })

  return (
    <Dialog open={up.dialogOpen} onClose={up.closeDialog} maxWidth="sm" fullWidth>
      <DialogTitle sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', pr: 1 }}>
        {t('upload.title')}
        <IconButton size="small" aria-label={t('common:close')} onClick={up.closeDialog} sx={{ color: 'text.secondary' }}>
          <FontAwesomeIcon icon={faXmark} style={{ fontSize: 14 }} />
        </IconButton>
      </DialogTitle>
      <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        {up.target ? (
          <Typography variant="body2" color="text.secondary">
            <Trans
              t={t}
              i18nKey="upload.uploadingTo"
              values={{ target: up.target.label }}
              components={{ strong: <b /> }}
            />
          </Typography>
        ) : (
          <Typography variant="body2" color="text.secondary">
            {t('upload.chooseTarget')}
          </Typography>
        )}

        {up.target && (
          <Box
            onDragOver={onZoneDragOver}
            onDrop={onZoneDrop}
            onClick={() => inputRef.current?.click()}
            sx={{
              border: '2px dashed',
              borderColor: 'divider',
              borderRadius: 1,
              p: 3,
              textAlign: 'center',
              cursor: 'pointer',
              color: 'text.secondary',
              '&:hover': { borderColor: 'primary.main', color: 'text.primary' },
            }}
          >
            <FontAwesomeIcon icon={faCloudArrowUp} style={{ fontSize: 26, opacity: 0.6 }} />
            <Typography variant="body2" sx={{ mt: 1 }}>
              {t('upload.dropHint')}
            </Typography>
            <Button size="small" sx={{ mt: 0.5 }} onClick={(e) => { e.stopPropagation(); inputRef.current?.click() }}>
              {t('upload.addFiles')}
            </Button>
            <input
              ref={inputRef}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                up.addFiles(Array.from(e.target.files ?? []))
                e.target.value = '' // allow re-picking the same file
              }}
            />
          </Box>
        )}

        {up.pending.length > 0 && (
          <Box>
            <Typography variant="caption" color="text.secondary" sx={{ textTransform: 'uppercase', letterSpacing: 0.5 }}>
              {t('upload.readyToUpload', { count: up.pending.length })}
            </Typography>
            {up.pending.map((p) => (
              <Box key={p.key} sx={{ display: 'flex', alignItems: 'center', gap: 1, py: 0.25 }}>
                <FontAwesomeIcon icon={faFile} style={{ fontSize: 12, opacity: 0.55 }} />
                <Typography variant="body2" noWrap sx={{ flex: 1, minWidth: 0 }} title={p.file.name}>
                  {p.file.name}
                </Typography>
                <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0 }}>
                  {fmtBytes(p.file.size)}
                </Typography>
                <IconButton
                  size="small"
                  aria-label={t('upload.removeFile', { name: p.file.name })}
                  onClick={() => up.removeFile(p.key)}
                  sx={{ color: 'text.secondary' }}
                >
                  <FontAwesomeIcon icon={faXmark} style={{ fontSize: 12 }} />
                </IconButton>
              </Box>
            ))}
          </Box>
        )}

        {up.jobs.length > 0 && (
          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <Typography variant="caption" color="text.secondary" sx={{ textTransform: 'uppercase', letterSpacing: 0.5 }}>
                {t('upload.uploadsHeading')}
              </Typography>
              {finished.length > 0 && (
                <Button size="small" onClick={() => up.dismissFinished()}>
                  {t('upload.clearFinished')}
                </Button>
              )}
            </Box>
            {up.jobs.map((j) => (
              <JobRow key={j.id} job={j} />
            ))}
          </Box>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={up.closeDialog}>{t('common:close')}</Button>
        <Button variant="contained" disabled={!canUpload} onClick={up.startUpload}>
          {uploadLabel}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
