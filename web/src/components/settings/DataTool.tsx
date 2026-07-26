import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faBroom } from '@fortawesome/free-solid-svg-icons'
import {
  Alert,
  Box,
  Button,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  FormGroup,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from '@mui/material'
import { useEffect, useMemo, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useAdminCleanup, useAdminDisk, useAdminProjectDataDelete } from '../../api/queries'
import type { DiskUsage } from '../../api/types'
import { fmtBytes } from '../../fmt'
import { Field } from './Field'

// DataTool: disk usage of the server's local per-project stores (chat,
// tasks, production, whiteboards — the data that is OURS, not APS's), a
// per-project typed-confirm delete across those stores, and an allow-listed
// stale-artifact cleanup. Deleting here never touches Fusion documents or
// the APS project — only this server's local app data.

const APP_STORES = ['chat', 'tasks', 'production', 'whiteboards'] as const
type AppStore = (typeof APP_STORES)[number]

// One merged row per project across the four app stores. The delete call
// takes projectId when the envelope supplied one; otherwise the dir slug
// works too (sanitizeID is identity on its own output).
interface ProjectRow {
  key: string
  deleteId: string
  dir: string
  name?: string
  bytes: Partial<Record<AppStore, number>>
  total: number
}

function projectRows(disk: DiskUsage): ProjectRow[] {
  const rows = new Map<string, ProjectRow>()
  for (const store of disk.stores) {
    if (!(APP_STORES as readonly string[]).includes(store.name)) continue
    const app = store.name as AppStore
    for (const p of store.projects) {
      const key = p.projectId || p.dir
      let row = rows.get(key)
      if (!row) {
        row = { key, deleteId: p.projectId || p.dir, dir: p.dir, bytes: {}, total: 0 }
        rows.set(key, row)
      }
      if (!row.name && p.projectName) row.name = p.projectName
      row.bytes[app] = (row.bytes[app] ?? 0) + p.bytes
      row.total += p.bytes
    }
  }
  return [...rows.values()].sort((a, b) => b.total - a.total)
}

export function DataTool({ active }: { active: boolean }) {
  const { t } = useTranslation('settings')
  const diskQ = useAdminDisk(active)
  const cleanup = useAdminCleanup()
  const [deleteTarget, setDeleteTarget] = useState<ProjectRow | null>(null)

  const disk = diskQ.data
  const rows = useMemo(() => (disk ? projectRows(disk) : []), [disk])

  if (diskQ.isLoading) {
    return (
      <Box sx={{ p: 2, textAlign: 'center' }}>
        <CircularProgress size={18} />
      </Box>
    )
  }
  if (diskQ.error) {
    return (
      <Alert severity="error" variant="outlined">
        {(diskQ.error as Error).message}
      </Alert>
    )
  }
  if (!disk) return null

  const pinsBytes = disk.stores.find((s) => s.name === 'pins')?.bytes ?? 0

  return (
    <Stack spacing={3}>
      <Field label={t('data.usage.title')}>
        <Stack spacing={1.5}>
          {rows.length === 0 ? (
            <Typography variant="caption" color="text.secondary">
              {t('data.usage.empty')}
            </Typography>
          ) : (
            <Table size="small" sx={{ maxWidth: 720 }}>
              <TableHead>
                <TableRow>
                  <TableCell>{t('data.usage.project')}</TableCell>
                  {APP_STORES.map((app) => (
                    <TableCell key={app} align="right">
                      {t(`data.store.${app}`)}
                    </TableCell>
                  ))}
                  <TableCell align="right">{t('data.usage.total')}</TableCell>
                  <TableCell align="right" />
                </TableRow>
              </TableHead>
              <TableBody>
                {rows.map((row) => (
                  <TableRow key={row.key} hover>
                    <TableCell sx={{ maxWidth: 220 }}>
                      <Typography variant="body2" noWrap title={row.name ?? row.dir}>
                        {row.name ?? row.dir}
                      </Typography>
                    </TableCell>
                    {APP_STORES.map((app) => (
                      <TableCell key={app} align="right">
                        {row.bytes[app] !== undefined ? fmtBytes(row.bytes[app]!) : '—'}
                      </TableCell>
                    ))}
                    <TableCell align="right">{fmtBytes(row.total)}</TableCell>
                    <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                      <Button size="small" color="error" onClick={() => setDeleteTarget(row)}>
                        {t('data.delete.action')}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <Stack spacing={0.25}>
            {pinsBytes > 0 && (
              <Typography variant="caption" color="text.secondary">
                {t('data.usage.pins', { size: fmtBytes(pinsBytes) })}
              </Typography>
            )}
            <Typography variant="caption" color="text.secondary">
              {t('data.usage.other', { size: fmtBytes(disk.otherBytes) })}
            </Typography>
            <Typography variant="caption" sx={{ fontWeight: 600 }}>
              {t('data.usage.grandTotal', { size: fmtBytes(disk.totalBytes) })}
            </Typography>
          </Stack>
        </Stack>
      </Field>

      <Field label={t('data.cleanup.title')}>
        <Stack spacing={1} sx={{ maxWidth: 520 }}>
          <Typography variant="caption" color="text.secondary">
            {t('data.cleanup.hint')}
          </Typography>
          {/* eslint-disable-next-line i18next/no-literal-string -- file names */}
          <Typography variant="caption" sx={{ fontFamily: 'monospace' }}>
            tokens.json · models/ · debug.log · server-restart.log · *.bak · *.tmp
          </Typography>
          <Box>
            <Button
              size="small"
              variant="outlined"
              startIcon={<FontAwesomeIcon icon={faBroom} style={{ fontSize: 11 }} />}
              disabled={cleanup.isPending}
              onClick={() => cleanup.mutate()}
            >
              {cleanup.isPending ? t('data.cleanup.cleaning') : t('data.cleanup.action')}
            </Button>
          </Box>
          {cleanup.isSuccess &&
            (cleanup.data.removed.length > 0 ? (
              <Alert severity="success" variant="outlined" sx={{ py: 0 }}>
                <Typography variant="caption" component="div">
                  {t('data.cleanup.done', {
                    items: cleanup.data.removed.length,
                    size: fmtBytes(cleanup.data.bytesFreed),
                  })}
                </Typography>
                {cleanup.data.removed.map((name) => (
                  <Typography key={name} variant="caption" component="div" sx={{ fontFamily: 'monospace' }}>
                    {name}
                  </Typography>
                ))}
              </Alert>
            ) : (
              <Typography variant="caption" color="text.secondary">
                {t('data.cleanup.nothing')}
              </Typography>
            ))}
          {cleanup.error && (
            <Alert severity="error" variant="outlined" sx={{ py: 0 }}>
              {(cleanup.error as Error).message}
            </Alert>
          )}
        </Stack>
      </Field>

      <DeleteDialog target={deleteTarget} onClose={() => setDeleteTarget(null)} />
    </Stack>
  )
}

// DeleteDialog: typed-confirmation per-project delete. The user picks which
// apps' data to remove (all four by default) and must type the project's
// displayed name back exactly. Permanent, and deliberately unrelated to
// Fusion documents — this is only the server's local app data.
function DeleteDialog({ target, onClose }: { target: ProjectRow | null; onClose: () => void }) {
  const { t } = useTranslation('settings')
  const del = useAdminProjectDataDelete()
  const [confirm, setConfirm] = useState('')
  const [checked, setChecked] = useState<Record<AppStore, boolean>>({
    chat: true,
    tasks: true,
    production: true,
    whiteboards: true,
  })

  // Fresh dialog state per target.
  useEffect(() => {
    setConfirm('')
    setChecked({ chat: true, tasks: true, production: true, whiteboards: true })
    del.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset only when the target changes
  }, [target?.key])

  const displayName = target ? (target.name ?? target.dir) : ''
  const apps = APP_STORES.filter((a) => checked[a])
  const canDelete = confirm === displayName && apps.length > 0 && !del.isPending

  const doDelete = () => {
    if (!target || !canDelete) return
    del.mutate({ projectId: target.deleteId, apps }, { onSuccess: onClose })
  }

  return (
    <Dialog open={target !== null} onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('data.delete.title')}</DialogTitle>
      <DialogContent>
        <Stack spacing={1.5} sx={{ pt: 0.5 }}>
          <Alert severity="warning" sx={{ py: 0.5 }}>
            {t('data.delete.warning', { name: displayName })}
          </Alert>
          <Box>
            <Typography variant="caption" color="text.secondary">
              {t('data.delete.appsLabel')}
            </Typography>
            <FormGroup>
              {APP_STORES.map((app) => (
                <FormControlLabel
                  key={app}
                  control={
                    <Checkbox
                      size="small"
                      checked={checked[app]}
                      onChange={(e) => setChecked((prev) => ({ ...prev, [app]: e.target.checked }))}
                    />
                  }
                  label={
                    <Typography variant="body2">
                      {t(`data.store.${app}`)}
                      {target?.bytes[app] !== undefined && (
                        <Typography component="span" variant="caption" color="text.secondary" sx={{ ml: 0.75 }}>
                          {fmtBytes(target.bytes[app]!)}
                        </Typography>
                      )}
                    </Typography>
                  }
                />
              ))}
            </FormGroup>
          </Box>
          <Typography variant="body2">
            <Trans
              t={t}
              i18nKey="data.delete.typeToConfirm"
              values={{ name: displayName }}
              components={{ code: <code /> }}
            />
          </Typography>
          <TextField
            size="small"
            autoFocus
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && doDelete()}
            label={t('data.delete.confirmLabel')}
            placeholder={displayName}
            InputLabelProps={{ shrink: true }}
          />
          {del.error && (
            <Alert severity="error" variant="outlined" sx={{ py: 0 }}>
              {(del.error as Error).message}
            </Alert>
          )}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button size="small" onClick={onClose}>
          {t('common:cancel')}
        </Button>
        <Button size="small" color="error" variant="contained" disabled={!canDelete} onClick={doDelete}>
          {del.isPending ? t('data.delete.deleting') : t('data.delete.confirmButton')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
