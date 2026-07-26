import { Box, Stack, Typography } from '@mui/material'
import type { ReactNode } from 'react'

// Field is the labelled setting row shared by the Settings tools (carried
// over from the pre-console single-page dialog).
export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Box>
      <Typography variant="subtitle2" gutterBottom>
        {label}
      </Typography>
      <Stack spacing={0.5}>{children}</Stack>
    </Box>
  )
}
