import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faBoxArchive, faFolderOpen } from '@fortawesome/free-solid-svg-icons'
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
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
import { Fragment, useEffect, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import {
  useAdminBackupConfigSet,
  useAdminBackupRestore,
  useAdminBackupRun,
  useAdminBackups,
  useAdminBackupVerify,
} from '../../api/queries'
import type { BackupFileResult, BackupSummary, BackupVerifyReport } from '../../api/types'
import { fmtBytes, fmtDate } from '../../fmt'
import { useNav } from '../../state/nav'
import { DirPickerDialog } from './DirPickerDialog'
import { Field } from './Field'

// BackupsTool: configure the GFS backup schedule (folder, daily time,
// enabled), trigger a manual backup, browse existing snapshots, and — per
// snapshot — verify integrity or restore. The server keeps 7 daily /
// 4 weekly / 12 monthly; manual and pre-restore backups are never pruned.
//
// Everything here is PER HUB: the config being edited is the current hub's
// own backup.json, the destination/schedule apply to that hub alone, and the
// snapshot table lists only that hub's tree — the server exposes no other
// hub's backups to this session.

// Reconnect delay after a restore: the server rebinds ~0.5s after acking
// (same mechanism as a port change), so a short pause lets the new listener
// come up before the page reloads.
const RECONNECT_DELAY_MS = 2500

// fileOk mirrors the server's per-file pass rule.
const fileOk = (f: BackupFileResult) =>
  !f.missing && f.hashOK && f.parseOK && f.versionOK && !f.detail

export function BackupsTool({ active }: { active: boolean }) {
  const { t } = useTranslation('settings')
  const { hubName } = useNav()
  const backupsQ = useAdminBackups(active)
  const save = useAdminBackupConfigSet()
  const run = useAdminBackupRun()
  const verify = useAdminBackupVerify()

  const cfg = backupsQ.data?.config
  const backups = backupsQ.data?.backups ?? []

  const [dir, setDir] = useState('')
  const [time, setTime] = useState('')
  const [enabled, setEnabled] = useState(false)
  const [seeded, setSeeded] = useState(false)
  const [pickerOpen, setPickerOpen] = useState(false)
  // One verify report per snapshot path, shown expanded under its row until
  // dismissed. Re-verifying replaces the stored report.
  const [reports, setReports] = useState<Record<string, BackupVerifyReport | undefined>>({})
  const [restoreTarget, setRestoreTarget] = useState<BackupSummary | null>(null)

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

  const doVerify = (path: string) => {
    verify.mutate(path, {
      onSuccess: (rep) => setReports((prev) => ({ ...prev, [path]: rep })),
    })
  }

  return (
    <Stack spacing={3}>
      <Field label={hubName ? t('backups.configTitleHub', { hub: hubName }) : t('backups.configTitle')}>
        <Stack spacing={1.5} sx={{ maxWidth: 520 }}>
          <Typography variant="caption" color="text.secondary">
            {t('backups.perHubNote')}
          </Typography>
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
          {verify.error && (
            <Alert severity="error" variant="outlined" sx={{ py: 0 }}>
              {(verify.error as Error).message}
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
            <Table size="small" sx={{ maxWidth: 720 }}>
              <TableHead>
                <TableRow>
                  <TableCell>{t('backups.table.kind')}</TableCell>
                  <TableCell>{t('backups.table.created')}</TableCell>
                  <TableCell align="right">{t('backups.table.files')}</TableCell>
                  <TableCell align="right">{t('backups.table.size')}</TableCell>
                  <TableCell align="right" />
                </TableRow>
              </TableHead>
              <TableBody>
                {backups.map((b) => {
                  const report = reports[b.path]
                  const verifying = verify.isPending && verify.variables === b.path
                  return (
                    <Fragment key={b.path}>
                      <TableRow hover>
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
                        <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                          <Button
                            size="small"
                            disabled={!!b.warning || verifying}
                            onClick={() => doVerify(b.path)}
                          >
                            {verifying ? t('backups.verify.checking') : t('backups.verify.action')}
                          </Button>
                          <Button
                            size="small"
                            color="error"
                            disabled={!!b.warning}
                            onClick={() => setRestoreTarget(b)}
                          >
                            {t('backups.restore.action')}
                          </Button>
                        </TableCell>
                      </TableRow>
                      {report && (
                        <TableRow>
                          <TableCell colSpan={5} sx={{ py: 1, bgcolor: 'action.hover' }}>
                            <VerifyResult
                              report={report}
                              onHide={() =>
                                setReports((prev) => ({ ...prev, [b.path]: undefined }))
                              }
                            />
                          </TableCell>
                        </TableRow>
                      )}
                    </Fragment>
                  )
                })}
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

      <RestoreDialog target={restoreTarget} onClose={() => setRestoreTarget(null)} />
    </Stack>
  )
}

// VerifyResult renders one snapshot's verify report inline under its table
// row: a green all-clear, or the failing files with a chip per failed check.
function VerifyResult({ report, onHide }: { report: BackupVerifyReport; onHide: () => void }) {
  const { t } = useTranslation('settings')
  const failures = report.files.filter((f) => !fileOk(f))

  const reasons = (f: BackupFileResult): string[] => {
    const out: string[] = []
    if (f.missing) out.push(t('backups.verify.missing'))
    else {
      if (!f.hashOK) out.push(t('backups.verify.hash'))
      if (!f.parseOK) out.push(t('backups.verify.parse'))
      if (!f.versionOK) out.push(t('backups.verify.version'))
    }
    if (f.detail) out.push(f.detail)
    return out
  }

  return (
    <Stack spacing={0.75}>
      <Stack direction="row" spacing={1} alignItems="center">
        {report.ok ? (
          <Typography variant="caption" color="success.main">
            {t('backups.verify.ok', { files: report.files.length })}
          </Typography>
        ) : (
          <Typography variant="caption" color="error">
            {t('backups.verify.failSummary', {
              failed: failures.length,
              files: report.files.length,
            })}
          </Typography>
        )}
        <Button size="small" onClick={onHide} sx={{ minWidth: 0 }}>
          {t('backups.verify.hide')}
        </Button>
      </Stack>
      {report.warning && (
        <Typography variant="caption" color="warning.main">
          {report.warning}
        </Typography>
      )}
      {failures.map((f) => (
        <Stack key={f.path} direction="row" spacing={0.75} alignItems="center" flexWrap="wrap">
          <Typography variant="caption" sx={{ fontFamily: 'monospace' }}>
            {f.path}
          </Typography>
          {reasons(f).map((r) => (
            <Chip key={r} label={r} size="small" color="error" variant="outlined" sx={{ height: 18 }} />
          ))}
        </Stack>
      ))}
    </Stack>
  )
}

// RestoreDialog: typed-confirmation restore. The user must type the
// snapshot's timestamp name exactly (the server enforces the same match);
// on success it shows the reconnect notice and reloads once the restarted
// listener is back — the port-change reconnect pattern.
function RestoreDialog({
  target,
  onClose,
}: {
  target: BackupSummary | null
  onClose: () => void
}) {
  const { t } = useTranslation('settings')
  const restore = useAdminBackupRestore()
  const [confirm, setConfirm] = useState('')
  const [reconnecting, setReconnecting] = useState(false)

  // Fresh dialog state per target.
  useEffect(() => {
    setConfirm('')
    restore.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset only when the target changes
  }, [target?.path])

  const name = target?.path.split('/').pop() ?? ''
  const canRestore = confirm === name && !restore.isPending && !reconnecting

  const doRestore = () => {
    if (!target || !canRestore) return
    restore.mutate(
      { path: target.path, confirm },
      {
        onSuccess: () => {
          setReconnecting(true)
          window.setTimeout(() => {
            window.location.reload()
          }, RECONNECT_DELAY_MS)
        },
      },
    )
  }

  return (
    <Dialog open={target !== null} onClose={reconnecting ? undefined : onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('backups.restore.title')}</DialogTitle>
      <DialogContent>
        {reconnecting ? (
          <Alert severity="info" sx={{ py: 0.5 }}>
            {t('backups.restore.reconnecting')}
          </Alert>
        ) : (
          <Stack spacing={1.5} sx={{ pt: 0.5 }}>
            <Alert severity="warning" sx={{ py: 0.5 }}>
              {t('backups.restore.warning')}
            </Alert>
            <Typography variant="body2">
              <Trans
                t={t}
                i18nKey="backups.restore.typeToConfirm"
                values={{ name }}
                components={{ code: <code /> }}
              />
            </Typography>
            <TextField
              size="small"
              autoFocus
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && doRestore()}
              label={t('backups.restore.confirmLabel')}
              placeholder={name}
              InputLabelProps={{ shrink: true }}
            />
            {restore.error && (
              <Alert severity="error" variant="outlined" sx={{ py: 0 }}>
                {(restore.error as Error).message}
              </Alert>
            )}
          </Stack>
        )}
      </DialogContent>
      {!reconnecting && (
        <DialogActions>
          <Button size="small" onClick={onClose}>
            {t('common:cancel')}
          </Button>
          <Button size="small" color="error" variant="contained" disabled={!canRestore} onClick={doRestore}>
            {restore.isPending ? t('backups.restore.restoring') : t('backups.restore.confirmButton')}
          </Button>
        </DialogActions>
      )}
    </Dialog>
  )
}
