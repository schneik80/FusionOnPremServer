import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  TextField,
  Typography,
} from '@mui/material'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

// ImageUrlDialog collects a public image URL (+ optional alt text) for the
// editor's "insert image from URL" toolbar action, with a live preview so the
// user can confirm the link actually resolves to an image before inserting.
export function ImageUrlDialog({
  open,
  onClose,
  onInsert,
}: {
  open: boolean
  onClose: () => void
  onInsert: (url: string, alt: string) => void
}) {
  const { t } = useTranslation('wiki')
  const [url, setUrl] = useState('')
  const [alt, setAlt] = useState('')
  const [previewFailed, setPreviewFailed] = useState(false)

  useEffect(() => {
    if (!open) return
    setUrl('')
    setAlt('')
    setPreviewFailed(false)
  }, [open])

  const trimmed = url.trim()
  const valid = /^https?:\/\/\S+$/i.test(trimmed)

  function insert() {
    if (!valid) return
    onInsert(trimmed, alt.trim() || 'image')
  }

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle>{t('imageUrlDialog.title')}</DialogTitle>
      <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        <TextField
          autoFocus
          fullWidth
          label={t('imageUrlDialog.urlLabel')}
          placeholder="https://example.com/image.png"
          value={url}
          onChange={(e) => {
            setUrl(e.target.value)
            setPreviewFailed(false)
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') insert()
          }}
          variant="standard"
          sx={{ mt: 1 }}
          helperText={t('imageUrlDialog.urlHelper')}
        />
        <TextField
          fullWidth
          label={t('imageUrlDialog.altLabel')}
          value={alt}
          onChange={(e) => setAlt(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') insert()
          }}
          variant="standard"
        />
        {valid && (
          <Box sx={{ textAlign: 'center' }}>
            {previewFailed ? (
              <Typography variant="caption" color="text.secondary">
                {t('imageUrlDialog.previewFailed')}
              </Typography>
            ) : (
              <Box
                component="img"
                src={trimmed}
                alt=""
                onError={() => setPreviewFailed(true)}
                sx={{ maxWidth: '100%', maxHeight: 160, borderRadius: 1 }}
              />
            )}
          </Box>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} color="inherit">
          {t('common:cancel')}
        </Button>
        <Button variant="contained" disabled={!valid} onClick={insert}>
          {t('imageUrlDialog.insert')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
