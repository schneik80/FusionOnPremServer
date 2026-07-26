import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faBoxArchive, faFolderOpen } from '@fortawesome/free-solid-svg-icons'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Stack,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from '@mui/material'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAdminBackupConfigSet, useAdminBackupRun, useAdminBackups } from '../../api/queries'
import { fmtBytes, fmtDate } from '../../fmt'
import { DirPickerDialog } from './DirPickerDialog'
import { Field } from './Field'

// BackupsTool: configure the GFS backup schedule (folder, daily time,
// enabled), trigger a manual backup, and browse existing snapshots. The
// server keeps 7 daily / 4 weekly / 12 monthly; manual backups are never
// pruned.

export function BackupsTool({ active }: { active: boolean }) {
  const { t } = useTranslation('settings')
  const backupsQ = useAdminBackups(active)
  const save = useAdminBackupConfigSet()
  const run = useAdminBackupRun()

  const cfg = backupsQ.data?.config
  const backups = backupsQ.data?.backups ?? []

  const [dir, setDir] = useState('')
  const [time, setTime] = useState('')
  const [enabled, setEnabled] = useState(false)
  const [seeded, setSeeded] = useState(false)
  const [pickerOpen, setPickerOpen] = useState(false)

  // Seed the form from the server config once per mount (not on every
  // refetch, which would clobber in-progress edits).
  useEffect(() => {
    if (cfg && !seeded) {
      setDir(cfg.backupDir)
      setTime(cfg.backupTime)
      setEnabled(cfg.backupEnabled)
      setSeeded(true)
    }
  }, [cfg, seeded])

  if (backupsQ.isLoading) {
    return (
      <Box sx={{ p: 2, textAlign: 'center' }}>
        <CircularProgress size={18} />
      </Box>
    )
  }
  if (backupsQ.error) {
    return (
      <Alert severity="error" variant="outlined">
        {(backupsQ.error as Error).message}
      </Alert>
    )
  }
  if (!cfg) return null

  const dirty = dir !== cfg.backupDir || time !== cfg.backupTime || enabled !== cfg.backupEnabled
  const canSave = dirty && !save.isPending && (dir.trim() !== '' || !enabled)
  const configured = cfg.backupDir !== ''

  const doSave = () => {
    if (!canSave) return
    save.mutate({ backupDir: dir.trim(), backupTime: time, backupEnabled: enabled })
  }

  return (
    <Stack spacing={3}>
      <Field label={t('backups.configTitle')}>
        <Stack spacing={1.5} sx={{ maxWidth: 520 }}>
          <Stack direction="row" spacing={1} alignItems="flex-start">
            <TextField
              size="small"
              fullWidth
              value={dir}
              onChange={(e) => setDir(e.target.value)}
              label={t('backups.dirLabel')}
              placeholder={t('backups.dirPlaceholder')}
              InputLabelProps={{ shrink: true }}
            />
            <Button
              size="small"
              variant="outlined"
              startIcon={<FontAwesomeIcon icon={faFolderOpen} style={{ fontSize: 11 }} />}
              onClick={() => setPickerOpen(true)}
              sx={{ flexShrink: 0, mt: 0.25 }}
            >
              {t('backups.browse')}
            </Button>
          </Stack>
          <Stack direction="row" spacing={2} alignItems="center">
            <TextField
              size="small"
              type="time"
              value={time}
              onChange={(e) => setTime(e.target.value)}
              label={t('backups.timeLabel')}
              InputLabelProps={{ shrink: true }}
              sx={{ width: 150 }}
            />
            <Stack direction="row" spacing={0.5} alignItems="center">
              <Switch size="small" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
              <Typography variant="body2">{t('backups.enabledLabel')}</Typography>
            </Stack>
          </Stack>
          <Stack direction="row" spacing={1} alignItems="center">
            <Button size="small" variant="contained" disabled={!canSave} onClick={doSave}>
              {t('common:save')}
            </Button>
            {save.isSuccess && !dirty && (
              <Typography variant="caption" color="success.main">
                {t('backups.saved')}
              </Typography>
            )}
          </Stack>
          {save.error && (
            <Alert severity="error" variant="outlined" sx={{ py: 0 }}>
              {(save.error as Error).message}
            </Alert>
          )}
        </Stack>
      </Field>

      <Field label={t('backups.table.title')}>
        <Stack spacing={1.5}>
          <Stack direction="row" spacing={1} alignItems="center">
            <Button
              size="small"
              variant="outlined"
              startIcon={<FontAwesomeIcon icon={faBoxArchive} style={{ fontSize: 11 }} />}
              disabled={run.isPending || !configured}
              onClick={() => run.mutate()}
            >
              {run.isPending ? t('backups.backingUp') : t('backups.backUpNow')}
            </Button>
            {run.isSuccess && !run.isPending && (
              <Typography variant="caption" color="success.main">
                {t('backups.runDone', {
                  files: run.data.fileCount,
                  size: fmtBytes(run.data.totalBytes),
                })}
              </Typography>
            )}
          </Stack>
          {run.error && (
            <Alert severity="error" variant="outlined" sx={{ py: 0 }}>
              {(run.error as Error).message}
            </Alert>
          )}
          {!configured ? (
            <Typography variant="caption" color="text.secondary">
              {t('backups.noDir')}
            </Typography>
          ) : backups.length === 0 ? (
            <Typography variant="caption" color="text.secondary">
              {t('backups.table.empty')}
            </Typography>
          ) : (
            <Table size="small" sx={{ maxWidth: 640 }}>
              <TableHead>
                <TableRow>
                  <TableCell>{t('backups.table.kind')}</TableCell>
                  <TableCell>{t('backups.table.created')}</TableCell>
                  <TableCell align="right">{t('backups.table.files')}</TableCell>
                  <TableCell align="right">{t('backups.table.size')}</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {backups.map((b) => (
                  <TableRow key={b.path} hover>
                    <TableCell>
                      {t(`backups.kinds.${b.kind}`, { defaultValue: b.kind })}
                    </TableCell>
                    <TableCell>
                      {b.createdAt
                        ? fmtDate(b.createdAt, { dateStyle: 'medium', timeStyle: 'short' })
                        : '—'}
                    </TableCell>
                    <TableCell align="right">{b.warning ? '—' : b.fileCount}</TableCell>
                    <TableCell align="right">{b.warning ? '—' : fmtBytes(b.totalBytes)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Stack>
      </Field>

      <DirPickerDialog
        open={pickerOpen}
        initialPath={dir.trim() || undefined}
        onClose={() => setPickerOpen(false)}
        onSelect={(path) => {
          setDir(path)
          setPickerOpen(false)
        }}
      />
    </Stack>
  )
}
