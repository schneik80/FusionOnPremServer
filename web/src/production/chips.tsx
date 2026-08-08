import { Box, Chip } from '@mui/material'
import { alpha } from '@mui/material/styles'
import type { ProdPlaceholder } from './types'

// Shared production chips, following the tasks/chips.tsx precedent: the small
// repeated visual vocabulary of this feature lives in one place, so a change to
// how a step or an unfilled slot reads lands everywhere at once. Both of these
// were previously copy-pasted across the canvas, the list, the step editor and
// the batch record — and had already drifted (20px vs 22px badges).

// StepNumBadge is the round step-number marker.
export function StepNumBadge({ num, size = 22 }: { num: number; size?: number }) {
  return (
    <Box
      sx={{
        width: size,
        height: size,
        borderRadius: '50%',
        flexShrink: 0,
        display: 'grid',
        placeItems: 'center',
        fontSize: size <= 20 ? 10 : 11,
        fontWeight: 700,
        color: 'primary.contrastText',
        bgcolor: 'primary.main',
      }}
    >
      {num}
    </Box>
  )
}

// DecisionGlyph marks a branch node wherever there is no room for the flow
// canvas's actual diamond — the list card and the frozen run record. A rotated
// square rather than an icon font, so it matches the canvas shape exactly.
export function DecisionGlyph({ color, size = 11 }: { color: string; size?: number }) {
  return (
    <Box
      component="span"
      sx={{
        width: size,
        height: size,
        flexShrink: 0,
        borderRadius: '2px',
        transform: 'rotate(45deg)',
        bgcolor: color,
      }}
    />
  )
}

// PlaceholderChip is an unfilled document slot. The dashed outline is the
// feature's visual language for "expected, not yet supplied"; a required slot
// is marked so a run's gaps are obvious at a glance.
export function PlaceholderChip({
  placeholder,
  onDelete,
}: {
  placeholder: ProdPlaceholder
  onDelete?: () => void
}) {
  return (
    <Chip
      size="small"
      variant="outlined"
      onDelete={onDelete}
      label={
        placeholder.required ? (
          <>
            {placeholder.label}
            <Box component="span" sx={{ color: 'error.main', ml: 0.25 }}>
              *
            </Box>
          </>
        ) : (
          placeholder.label
        )
      }
      sx={{
        borderStyle: 'dashed',
        fontSize: 11,
        borderColor: (t) => alpha(t.palette.text.primary, 0.3),
      }}
    />
  )
}
