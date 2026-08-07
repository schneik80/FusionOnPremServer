import { Box, Dialog, DialogContent, Typography } from '@mui/material'
import { useState } from 'react'
import { api } from '../../api/client'
import type { ImgRef } from './imgref'

// ImageCard renders an fls:img token as an inline thumbnail with a
// click-to-zoom lightbox. The bytes come same-origin (session cookie as a
// subresource, CSP img-src 'self'), and errors degrade to the filename so a
// deleted APS item never breaks the message row.
export function ImageCard({ imgRef }: { imgRef: ImgRef }) {
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState(false)
  const src = api.wikiImageUrl(imgRef.dmProjectId, imgRef.itemId)

  if (failed) {
    return (
      <Typography component="span" variant="caption" color="text.disabled">
        {imgRef.name}
      </Typography>
    )
  }
  return (
    <>
      <Box
        component="img"
        src={src}
        alt={imgRef.name}
        onError={() => setFailed(true)}
        onClick={() => setOpen(true)}
        sx={{
          display: 'block',
          maxWidth: '100%',
          maxHeight: 260,
          my: 0.5,
          borderRadius: 1,
          border: 1,
          borderColor: 'divider',
          cursor: 'zoom-in',
        }}
      />
      <Dialog open={open} onClose={() => setOpen(false)} maxWidth="lg">
        <DialogContent sx={{ p: 1 }}>
          <Box
            component="img"
            src={src}
            alt={imgRef.name}
            onClick={() => setOpen(false)}
            sx={{ display: 'block', maxWidth: '100%', maxHeight: '85vh', cursor: 'zoom-out' }}
          />
          <Typography variant="caption" color="text.secondary" sx={{ px: 0.5 }}>
            {imgRef.name}
          </Typography>
        </DialogContent>
      </Dialog>
    </>
  )
}
