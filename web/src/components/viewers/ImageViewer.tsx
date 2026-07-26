import { Box } from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ViewerFile } from './kind'
import { FallbackViewer } from './ui'

// ImageViewer shows a raster/vector image inline, scaled to fit the pane. On a
// load error (e.g. the server capped an oversized image) it drops to the
// download fallback. Click toggles fit-to-width vs. actual size for a closer look.
export function ImageViewer({ file }: { file: ViewerFile }) {
  const { t } = useTranslation('browse')
  const [failed, setFailed] = useState(false)
  const [zoom, setZoom] = useState(false)

  if (failed) return <FallbackViewer file={file} reason={t('viewer.imageFailed')} />

  return (
    <Box sx={{ display: 'flex', justifyContent: 'center' }}>
      <Box
        component="img"
        src={file.url}
        alt={file.name}
        onError={() => setFailed(true)}
        onClick={() => setZoom((z) => !z)}
        sx={{
          maxWidth: '100%',
          height: 'auto',
          objectFit: 'contain',
          borderRadius: 1,
          boxShadow: 2,
          cursor: zoom ? 'zoom-out' : 'zoom-in',
          ...(zoom ? { width: 'auto', maxWidth: 'none' } : {}),
        }}
      />
    </Box>
  )
}
