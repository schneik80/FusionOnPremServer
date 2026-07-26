import { Box } from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ViewerFile } from './kind'
import { FallbackViewer } from './ui'

// VideoViewer plays a video with the native controls. Seeking works because the
// file endpoint forwards Range requests to OSS (206 partial content), so the
// browser fetches only the window it needs rather than the whole file. Codecs
// the browser can't decode fall through to the download fallback.
export function VideoViewer({ file }: { file: ViewerFile }) {
  const { t } = useTranslation('browse')
  const [failed, setFailed] = useState(false)

  if (failed) return <FallbackViewer file={file} reason={t('viewer.videoFailed')} />

  return (
    <Box sx={{ display: 'flex', justifyContent: 'center' }}>
      <Box
        component="video"
        src={file.url}
        controls
        preload="metadata"
        onError={() => setFailed(true)}
        sx={{ width: '100%', maxHeight: '72vh', bgcolor: '#000', borderRadius: 1 }}
      />
    </Box>
  )
}
