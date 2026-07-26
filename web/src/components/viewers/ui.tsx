// Shared bits for the file viewers: a centered spinner and the download/open
// fallback card (also used as the error state for the media viewers).

import { faDownload, faFileArrowDown } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, Button, CircularProgress, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { ViewerFile } from './kind'

export function ViewerSpinner() {
  return (
    <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
      <CircularProgress size={22} />
    </Box>
  )
}

// FallbackViewer is shown for file types we can't render inline, and as the
// error state when a viewer fails to load. It always offers a plain download
// (the bytes URL doubles as the href) and, when known, an "Open in Fusion" link.
export function FallbackViewer({ file, reason }: { file: ViewerFile; reason?: string }) {
  const { t } = useTranslation('browse')
  return (
    <Stack spacing={1.25} alignItems="center" sx={{ py: 5, px: 2, textAlign: 'center' }}>
      <FontAwesomeIcon icon={faFileArrowDown} style={{ fontSize: 40, opacity: 0.4 }} />
      <Typography variant="subtitle2" sx={{ maxWidth: '100%', wordBreak: 'break-word' }}>
        {file.name}
      </Typography>
      {reason && (
        <Typography variant="body2" color="text.secondary">
          {reason}
        </Typography>
      )}
      {file.size && (
        <Typography variant="caption" color="text.secondary">
          {file.size}
        </Typography>
      )}
      <Stack direction="row" spacing={1} sx={{ pt: 0.5 }}>
        <Button
          component="a"
          href={file.url}
          download={file.name}
          size="small"
          variant="outlined"
          startIcon={<FontAwesomeIcon icon={faDownload} style={{ fontSize: 12 }} />}
        >
          {t('viewer.download')}
        </Button>
        {file.webUrl && (
          <Button component="a" href={file.webUrl} target="_blank" rel="noopener" size="small">
            {t('viewer.openInFusion')}
          </Button>
        )}
      </Stack>
    </Stack>
  )
}
