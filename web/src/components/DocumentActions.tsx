import {
  faArrowUpRightFromSquare,
  faBoxArchive,
  faCircleExclamation,
  faCirclePlus,
  faDownload,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  IconButton,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { Item } from '../api/types'
import { FUSION_NATIVE_KINDS, INSERTABLE_KINDS } from '../api/types'
import { useFusionActions } from '../state/fusionActions'

// The three things you can do with a Fusion-native document from the details
// header: send it to the Fusion desktop client (Open / Insert), or ask APS to
// build a downloadable archive of it (Archive).
//
// Only native documents get these. An uploaded STEP or PDF is not something
// Fusion opens by lineage urn, and it already has a plain download in its
// preview tab — offering the same thing twice under a different name would be
// worse than offering it once.

export function DocumentActions({
  item,
  projectAltId,
}: {
  item: Item
  /** the project's DM id; every APS byte-level call needs it */
  projectAltId?: string
}) {
  const { t } = useTranslation(['details', 'errors'])
  const { runAction, pendingFor, startArchive, archiveFor } = useFusionActions()

  // Gate on nativeness first, then on having the DM project id — without it
  // nothing here can be addressed, and a button that always fails is worse
  // than no button.
  if (!FUSION_NATIVE_KINDS.has(item.kind) || !projectAltId) return null

  const pending = pendingFor(item.id)
  const archiving = archiveFor(item.id)
  const canInsert = INSERTABLE_KINDS.has(item.kind)

  return (
    <Stack direction="row" sx={{ gap: 0.25, flexShrink: 0 }}>
      <ActionButton
        label={t('details.actions.open')}
        icon={faArrowUpRightFromSquare}
        busy={pending === 'open'}
        disabled={pending !== null}
        onClick={() => void runAction(item, projectAltId, 'open')}
      />
      {canInsert && (
        <ActionButton
          label={t('details.actions.insert')}
          icon={faCirclePlus}
          busy={pending === 'insert'}
          disabled={pending !== null}
          onClick={() => void runAction(item, projectAltId, 'insert')}
        />
      )}
      <ActionButton
        // While a job is running the tooltip says so rather than repeating the
        // verb, so a disabled button explains itself instead of looking broken.
        label={archiving ? t('details.actions.archiving') : t('details.actions.archive')}
        icon={faBoxArchive}
        busy={!!archiving}
        disabled={!!archiving}
        onClick={() => void startArchive(item, projectAltId)}
      />
    </Stack>
  )
}

function ActionButton({
  label,
  icon,
  busy,
  disabled,
  onClick,
}: {
  label: string
  icon: typeof faBoxArchive
  busy: boolean
  disabled: boolean
  onClick: () => void
}) {
  return (
    <Tooltip title={label}>
      {/* A disabled IconButton swallows pointer events, which would take the
          tooltip with it — the span keeps the explanation reachable. */}
      <span>
        <IconButton
          size="small"
          aria-label={label}
          disabled={disabled}
          onClick={onClick}
          sx={{ flexShrink: 0 }}
        >
          {busy ? (
            <CircularProgress size={14} />
          ) : (
            <FontAwesomeIcon icon={icon} style={{ fontSize: 14 }} />
          )}
        </IconButton>
      </span>
    </Tooltip>
  )
}

// FusionActionFeedback reports how an Open or Insert went. It is mounted once,
// app-wide, rather than per document: an action outlives the panel that started
// it (the user can navigate while the helper is launching), and its result
// still has to land somewhere.
//
// Success is deliberately quiet — Fusion coming to the front is the real
// confirmation — so this only opens for a failure or a missing helper.
export function FusionActionFeedback() {
  const { t } = useTranslation(['details', 'errors'])
  const { feedback, dismissFeedback } = useFusionActions()

  const open = !!feedback && feedback.status !== 'ok'
  if (!feedback) return null

  const missing = feedback.status === 'helperMissing'
  const title = missing
    ? t('details.fusion.helperMissingTitle')
    : feedback.kind === 'archive'
      ? t('details.fusion.archiveFailedTitle')
      : feedback.action === 'insert'
        ? t('details.fusion.insertFailedTitle')
        : t('details.fusion.openFailedTitle')

  // A code we recognize gets its sentence from the errors catalog; anything
  // else falls back to a generic line rather than showing a raw token.
  const generic =
    feedback.kind === 'archive'
      ? t('details.fusion.archiveGenericFailure')
      : t('details.fusion.genericFailure')
  const detail = missing
    ? null
    : feedback.code
      ? t(`errors:${feedback.code}`, { defaultValue: generic })
      : generic

  return (
    <Dialog open={open} onClose={dismissFeedback} maxWidth="xs" fullWidth>
      <DialogTitle sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Box sx={{ color: 'warning.main', display: 'inline-flex' }}>
          <FontAwesomeIcon icon={faCircleExclamation} style={{ fontSize: 16 }} />
        </Box>
        {title}
      </DialogTitle>
      <DialogContent>
        {detail && <DialogContentText>{detail}</DialogContentText>}
        {missing && (
          <>
            <DialogContentText>{t('details.fusion.helperMissingBody')}</DialogContentText>
            <Typography
              variant="body2"
              component="pre"
              sx={{
                mt: 2,
                p: 1.5,
                borderRadius: 1,
                bgcolor: 'action.hover',
                fontFamily: 'monospace',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
              }}
            >
              {`fls-helper register\nfls-helper pair ${window.location.origin}`}
            </Typography>
          </>
        )}
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 2 }}>
          {feedback.docName}
        </Typography>
      </DialogContent>
      <DialogActions>
        <Button onClick={dismissFeedback}>{t('common:close')}</Button>
      </DialogActions>
    </Dialog>
  )
}

// ArchiveReadyLink is the download affordance for a finished archive, used by
// the notification bell's row.
export function ArchiveReadyLink({
  id,
  docName,
  label,
  onDownload,
}: {
  id: string
  docName: string
  label: string
  /** called when the user starts a download — the caller marks the row read */
  onDownload?: () => void
}) {
  const { t } = useTranslation(['details', 'notifications'])
  const { archiveReady, downloadArchive, downloadingArchive } = useFusionActions()

  // A notification is persisted per user; the job it points at lives only in
  // the server's memory. So a "ready" row can outlive its archive — after a
  // restart, or once the retention window closes. When that happens the row
  // says so rather than offering a button that cannot work.
  if (!archiveReady(id)) {
    return (
      <Tooltip title={t('details.archiveGone')}>
        <span>
          <IconButton
            size="small"
            disabled
            aria-label={t('details.archiveGone')}
            sx={{ color: 'text.disabled' }}
          >
            <FontAwesomeIcon icon={faDownload} style={{ fontSize: 12 }} />
          </IconButton>
        </span>
      </Tooltip>
    )
  }

  const busy = downloadingArchive(id)
  return (
    <Tooltip title={label}>
      <span>
        <IconButton
          size="small"
          aria-label={label}
          disabled={busy}
          // A button, not an <a download>: a download link hands whatever
          // comes back to the browser's download manager, so a server error
          // is saved as a file instead of shown. Fetching first means only
          // real bytes ever reach the download manager.
          onClick={(e) => {
            e.stopPropagation()
            onDownload?.()
            void downloadArchive(id, docName)
          }}
          sx={{ color: 'text.secondary' }}
        >
          {busy ? (
            <CircularProgress size={12} />
          ) : (
            <FontAwesomeIcon icon={faDownload} style={{ fontSize: 12 }} />
          )}
        </IconButton>
      </span>
    </Tooltip>
  )
}
