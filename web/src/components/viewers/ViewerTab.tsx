// The Preview tab: dispatches a selected uploaded file to the right viewer
// (image / video / pdf / markdown / text+g-code), falling back to a download
// card for types we can't render inline. Mounted only while the tab is open, so
// content isn't fetched until the user looks.

import { Typography } from '@mui/material'
import { lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../api/client'
import type { Details, Item } from '../../api/types'
import { ImageViewer } from './ImageViewer'
import { MarkdownViewer } from './MarkdownViewer'
import { TextViewer } from './TextViewer'
import { VideoViewer } from './VideoViewer'
import { viewerKindFor, type ViewerFile } from './kind'
import { FallbackViewer, ViewerSpinner } from './ui'

// pdf.js is ~1 MB — load it (and its worker) only when a PDF is actually opened,
// keeping it out of the initial app bundle.
const PdfViewer = lazy(() => import('./PdfViewer').then((m) => ({ default: m.PdfViewer })))

export function ViewerTab({
  item,
  details,
  dmProjectId,
}: {
  item: Item
  details?: Details
  dmProjectId?: string
}) {
  const { t } = useTranslation('browse')
  if (!dmProjectId) {
    return (
      <Typography variant="body2" color="text.secondary">
        {t('viewer.previewUnavailable')}
      </Typography>
    )
  }

  const name = details?.name || item.name
  const file: ViewerFile = {
    name,
    url: api.fileUrl(dmProjectId, item.id, name),
    webUrl: details?.fusionWebUrl,
    size: details?.size,
  }

  switch (viewerKindFor(name, details?.mimeType)) {
    case 'image':
      return <ImageViewer file={file} />
    case 'video':
      return <VideoViewer file={file} />
    case 'pdf':
      return (
        <Suspense fallback={<ViewerSpinner />}>
          <PdfViewer file={file} />
        </Suspense>
      )
    case 'markdown':
      return <MarkdownViewer file={file} dmProjectId={dmProjectId} itemId={item.id} />
    case 'gcode':
    case 'text':
      return <TextViewer file={file} dmProjectId={dmProjectId} itemId={item.id} />
    default:
      return <FallbackViewer file={file} reason={t('viewer.noInlinePreview')} />
  }
}
