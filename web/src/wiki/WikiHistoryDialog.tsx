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
  List,
  ListItemButton,
  Stack,
  Typography,
} from '@mui/material'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { WikiVersion } from '../api/types'
import { useWikiPageVersion, useWikiVersions } from '../api/queries'
import { fmtDate } from '../fmt'
import { MarkdownView } from './MarkdownView'

// WikiHistoryDialog browses a published page's versions (every publish is a DM
// version) with a rendered preview of the selected one, and offers to make an
// older version current again. A restore is copy-forward — the old bytes become
// a *new* version — so the list only ever grows: nothing is deleted, and the
// restore itself shows up as the newest entry. The confirm step spells that out
// before anything is written.

export interface WikiHistoryTarget {
  itemId: string
  title: string
  /** the tip the caller saw — sent as baseVersion so a concurrent publish 409s */
  tipVersion?: string
}

interface WikiHistoryDialogProps {
  open: boolean
  target: WikiHistoryTarget | null
  dmProjectId: string | null
  /** false when the session cannot write (no hub/project ids) — history is still browsable */
  canRestore: boolean
  restoring: boolean
  onRestore: (version: WikiVersion) => Promise<void>
  onClose: () => void
}

const RAIL_WIDTH = 240

export function WikiHistoryDialog({
  open,
  target,
  dmProjectId,
  canRestore,
  restoring,
  onRestore,
  onClose,
}: WikiHistoryDialogProps) {
  const { t } = useTranslation('wiki')
  const itemId = target?.itemId ?? null
  const versionsQ = useWikiVersions(dmProjectId, itemId, open)
  const versions = versionsQ.data ?? []

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [confirming, setConfirming] = useState(false)

  // The tip is whatever the page list said, else the highest number we listed.
  const tipId = useMemo(() => {
    if (target?.tipVersion && versions.some((v) => v.versionId === target.tipVersion)) return target.tipVersion
    return versions[0]?.versionId ?? null
  }, [target?.tipVersion, versions])
  const nextNumber = versions.reduce((m, v) => Math.max(m, v.number), 0) + 1

  // Start on the newest version whenever the dialog (re)opens on a page; reset
  // the confirm step too so a stale prompt never survives a reopen.
  useEffect(() => {
    if (!open) return
    setConfirming(false)
    setSelectedId(null)
  }, [open, itemId])
  const selected = versions.find((v) => v.versionId === selectedId) ?? versions[0] ?? null
  const selectedIsTip = !!selected && selected.versionId === tipId

  const previewQ = useWikiPageVersion(dmProjectId, itemId, selected?.versionId ?? null, open)

  async function confirmRestore() {
    if (!selected) return
    await onRestore(selected)
    setConfirming(false)
  }

  return (
    <Dialog open={open} onClose={restoring ? undefined : onClose} maxWidth="md" fullWidth>
      <DialogTitle sx={{ pb: 1 }}>
        <Typography component="div" variant="h6" noWrap>
          {t('history.title')}
        </Typography>
        {target && (
          <Typography variant="body2" color="text.secondary" noWrap>
            {target.title}
          </Typography>
        )}
      </DialogTitle>
      <DialogContent dividers sx={{ p: 0, display: 'flex', height: '65vh', minHeight: 320 }}>
        <Box
          sx={{
            width: RAIL_WIDTH,
            flexShrink: 0,
            borderRight: 1,
            borderColor: 'divider',
            overflowY: 'auto',
            minHeight: 0,
          }}
        >
          {versionsQ.isLoading ? (
            <Box sx={{ p: 2, textAlign: 'center' }}>
              <CircularProgress size={18} />
            </Box>
          ) : versionsQ.error ? (
            <Alert severity="error" variant="outlined" sx={{ m: 1 }}>
              {t('history.loadFailed')}
            </Alert>
          ) : versions.length === 0 ? (
            <Typography variant="body2" color="text.secondary" sx={{ p: 2, textAlign: 'center' }}>
              {t('history.empty')}
            </Typography>
          ) : (
            <List dense disablePadding>
              {versions.map((v) => {
                const isTip = v.versionId === tipId
                return (
                  <ListItemButton
                    key={v.versionId}
                    selected={selected?.versionId === v.versionId}
                    onClick={() => {
                      setSelectedId(v.versionId)
                      setConfirming(false)
                    }}
                    sx={{ alignItems: 'flex-start', flexDirection: 'column', gap: 0.25, py: 0.75, px: 1.5 }}
                  >
                    <Stack direction="row" spacing={1} alignItems="center" sx={{ width: '100%' }}>
                      <Typography variant="body2" sx={{ fontWeight: 600, flex: 1, minWidth: 0 }} noWrap>
                        {t('history.version', { number: v.number })}
                      </Typography>
                      {isTip && (
                        <Chip
                          size="small"
                          color="success"
                          variant="outlined"
                          label={t('history.current')}
                          sx={{ height: 17, fontSize: 9.5 }}
                        />
                      )}
                    </Stack>
                    {v.createdOn && (
                      <Typography variant="caption" color="text.secondary" noWrap sx={{ maxWidth: '100%' }}>
                        {fmtDate(v.createdOn, { dateStyle: 'medium', timeStyle: 'short' })}
                      </Typography>
                    )}
                    {v.createdBy && (
                      <Typography variant="caption" color="text.secondary" noWrap sx={{ maxWidth: '100%' }}>
                        {v.createdBy}
                      </Typography>
                    )}
                  </ListItemButton>
                )
              })}
            </List>
          )}
        </Box>

        <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
          {!selected ? null : previewQ.isLoading ? (
            <Box sx={{ flex: 1, p: 2, textAlign: 'center' }}>
              <CircularProgress size={18} />
            </Box>
          ) : previewQ.error ? (
            <Box sx={{ flex: 1, p: 2 }}>
              <Alert severity="error" variant="outlined">
                {t('history.previewFailed')}
              </Alert>
            </Box>
          ) : (
            <MarkdownView>{previewQ.data?.markdown ?? ''}</MarkdownView>
          )}
        </Box>
      </DialogContent>
      <DialogActions sx={{ px: 2, py: 1.5, gap: 1, flexWrap: 'wrap' }}>
        {confirming && selected && target ? (
          <>
            <Typography variant="body2" sx={{ flex: 1, minWidth: 240 }}>
              {t('history.confirmBody', { number: selected.number, title: target.title, next: nextNumber })}
            </Typography>
            <Button color="inherit" onClick={() => setConfirming(false)} disabled={restoring}>
              {t('common:cancel')}
            </Button>
            <Button variant="contained" onClick={() => void confirmRestore()} disabled={restoring}>
              {restoring ? t('history.restoring') : t('history.confirm')}
            </Button>
          </>
        ) : (
          <>
            <Box sx={{ flex: 1 }} />
            <Button color="inherit" onClick={onClose} disabled={restoring}>
              {t('common:close')}
            </Button>
            <Button
              variant="contained"
              onClick={() => setConfirming(true)}
              disabled={!canRestore || !selected || selectedIsTip || !!previewQ.error || restoring}
            >
              {t('history.restore')}
            </Button>
          </>
        )}
      </DialogActions>
    </Dialog>
  )
}
