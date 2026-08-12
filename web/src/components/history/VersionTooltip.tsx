import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Stack, Typography } from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { thumbnailSrc } from '../../api/thumbnails'
import type { VersionSummary } from '../../api/types'
import { fmtDate } from '../../fmt'
import UserAvatar from '../UserAvatar'

// The hover card for one save: its thumbnail (rendered directly — no polling,
// a 404 just drops the image), version number, milestone/release/share
// markers, exact local timestamp, author, and the save comment.
export default function VersionTooltip({ v }: { v: VersionSummary }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const [imgFailed, setImgFailed] = useState(false)
  const thumb = v.rootComponentVersionId
    ? thumbnailSrc({ kind: 'design', cvId: v.rootComponentVersionId })
    : null
  const showThumb = !!thumb && !imgFailed
  // Two different reasons a preview can be absent, worth telling apart: the
  // version carries no root component version at all (older/unmigrated saves
  // resolve `rootComponentVersion` to null, so there is nothing to ask APS
  // for), or it has one and the fetch came back empty — the thumbnail was
  // never generated, is still generating, or the request was rate-limited.
  const missingReason = !thumb ? 'none' : imgFailed ? 'failed' : null

  return (
    <Box sx={{ py: 0.25, maxWidth: 210 }}>
      {showThumb && (
        <Box
          component="img"
          src={thumb!}
          alt=""
          draggable={false} // a native image drag would fight the panel's gestures
          onError={() => setImgFailed(true)}
          sx={{
            display: 'block',
            width: '100%',
            maxHeight: 140,
            objectFit: 'contain',
            borderRadius: 0.5,
            mb: 0.5,
            bgcolor: alpha(theme.palette.common.black, 0.15),
          }}
        />
      )}
      {missingReason && (
        <Typography
          variant="caption"
          sx={{ display: 'block', mb: 0.5, fontStyle: 'italic', opacity: 0.6 }}
        >
          {missingReason === 'none' ? t('history.noPreview') : t('history.previewFailed')}
        </Typography>
      )}
      <Typography variant="caption" sx={{ fontWeight: 600, display: 'block' }}>
        {t('history.versionShort', { number: v.number })}
        {v.isMilestone ? ` · ${t('history.milestone')}` : ''}
        {v.revision ? ` · ${t('history.release', { revision: v.revision })}` : ''}
      </Typography>
      {v.publicShare && (
        <Typography
          variant="caption"
          sx={{ display: 'block', color: 'secondary.main', fontWeight: 600 }}
        >
          {t('history.publicShare')}
        </Typography>
      )}
      {/* The description the author typed on save — the headline of the card,
          not a footnote after the metadata. Shown even when empty, so "this
          save has no description" is distinguishable from "the description did
          not render". */}
      <Typography
        variant="caption"
        sx={{
          display: 'block',
          mt: 0.5,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          ...(v.comment ? {} : { fontStyle: 'italic', opacity: 0.6 }),
        }}
      >
        {v.comment || t('history.noDescription')}
      </Typography>
      <Typography variant="caption" sx={{ display: 'block', mt: 0.5, opacity: 0.85 }}>
        {v.createdOn ? fmtDate(v.createdOn, { dateStyle: 'medium', timeStyle: 'short' }) : '—'}
      </Typography>
      {/* The same identity disc as the row's gutter, so the hover card and the
          track it came from read as the same person. */}
      <Stack direction="row" spacing={0.75} alignItems="center" sx={{ mt: 0.25 }}>
        <UserAvatar id={v.createdById} name={v.createdBy} size={18} />
        <Typography variant="caption" sx={{ opacity: 0.95 }}>
          {v.createdBy || t('history.unknownAuthor')}
        </Typography>
      </Stack>
    </Box>
  )
}
