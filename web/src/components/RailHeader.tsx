import { faPlus } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, Button, Stack, TextField, Tooltip, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

// RailHeader is the top of a project app's left rail — the strip above the
// list in Production, Whiteboards, Wiki and Chat.
//
// It exists because those four had each grown their own: a bordered row here, a
// borderless one there; a `subtitle2` title, an `overline` title, no title;
// a contained "New", a full-width outlined "New page", a bare `+` icon button.
// Switching tabs meant re-finding the same control in a different place with a
// different name. One component, one answer: title left, primary action right,
// optional search directly below.
//
// Tasks deliberately does NOT use this. Its create button lives in the app-level
// bar beside the List/Board toggle, because the Board view has no rail at all
// and TaskKanban carries no create affordance of its own — moving it into the
// rail would strand it in Board view. It matches the labelling and styling here
// instead.

interface RailHeaderProps {
  /** Plural noun for what the list holds — "Jobs", "Channels", "Pages". */
  title: string
  /** Omit to render a header with no primary action. */
  onNew?: () => void
  newDisabled?: boolean
  /**
   * Why the action is unavailable, shown on hover. Prefer a disabled button with
   * a reason over hiding it: a control that vanishes reads as a missing feature,
   * not as a permission the user doesn't have.
   */
  newDisabledReason?: string
  /** Renders a search field below the title row. */
  search?: {
    value: string
    onChange: (v: string) => void
    placeholder?: string
  }
}

export function RailHeader({
  title,
  onNew,
  newDisabled,
  newDisabledReason,
  search,
}: RailHeaderProps) {
  const { t } = useTranslation('browse')
  return (
    <>
      <Stack
        direction="row"
        alignItems="center"
        sx={{ px: 1, py: 0.75, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
      >
        <Typography variant="subtitle2" sx={{ flex: 1, pl: 0.5 }}>
          {title}
        </Typography>
        {onNew && (
          // The span keeps the tooltip working over a disabled button, which
          // fires no pointer events of its own.
          <Tooltip title={newDisabled ? (newDisabledReason ?? '') : ''}>
            <span>
              <Button
                size="small"
                variant="contained"
                disabled={newDisabled}
                startIcon={<FontAwesomeIcon icon={faPlus} style={{ fontSize: 11 }} />}
                onClick={onNew}
                sx={{ py: 0.25, textTransform: 'none' }}
              >
                {t('railHeader.new')}
              </Button>
            </span>
          </Tooltip>
        )}
      </Stack>
      {search && (
        <Box sx={{ p: 1, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}>
          <TextField
            size="small"
            fullWidth
            value={search.value}
            onChange={(e) => search.onChange(e.target.value)}
            placeholder={search.placeholder}
          />
        </Box>
      )}
    </>
  )
}
