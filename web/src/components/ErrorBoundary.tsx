import { Box, Button, Typography } from '@mui/material'
import { Component, type ErrorInfo, type ReactNode } from 'react'
import { withTranslation, type WithTranslation } from 'react-i18next'

// ErrorBoundary contains a render crash to one region instead of letting it
// unmount the whole app — React tears the entire tree down on an uncaught
// render error, which reads to the user as the app "going white".
//
// Worth wrapping around anything large and third-party (the tldraw canvas) or
// anything rendering stored data whose shape we don't fully control.
interface Props {
  children: ReactNode
  /** what failed, for the message — e.g. "whiteboard" */
  label: string
  /** changing this resets the boundary (e.g. selecting a different board) */
  resetKey?: string
  /**
   * Render as one inline row rather than a centred full-region panel. Use when
   * the boundary wraps an ITEM in a list — a chat message, a card — where the
   * rest of the list should carry on around the failure.
   */
  compact?: boolean
}

interface State {
  error: Error | null
}

// Class components can't use hooks, so translations arrive via the
// withTranslation HOC (t injected as a prop, bound to the browse namespace).
class ErrorBoundaryInner extends Component<Props & WithTranslation, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidUpdate(prev: Props) {
    // A new target gets a fresh attempt: one bad board shouldn't poison the next.
    if (this.state.error && prev.resetKey !== this.props.resetKey) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(`${this.props.label} crashed:`, error, info.componentStack)
  }

  render() {
    const { t } = this.props
    if (!this.state.error) return this.props.children

    // Inline form: one muted row that names what failed and shows the error,
    // so a single bad item in a list is legible in place instead of taking the
    // whole list down with it.
    if (this.props.compact) {
      return (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'baseline',
            gap: 1,
            px: 1,
            py: 0.75,
            my: 0.25,
            borderRadius: 1,
            border: 1,
            borderColor: 'divider',
            bgcolor: 'action.hover',
          }}
        >
          <Typography variant="caption" sx={{ fontWeight: 600, flexShrink: 0 }}>
            {t('errorBoundary.failed', { label: this.props.label })}
          </Typography>
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ minWidth: 0, overflowWrap: 'anywhere' }}
          >
            {this.state.error.message || t('errorBoundary.unexpected')}
          </Typography>
        </Box>
      )
    }

    return (
      <Box
        sx={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 1,
          p: 3,
          textAlign: 'center',
        }}
      >
        <Typography variant="subtitle2">
          {t('errorBoundary.failed', { label: this.props.label })}
        </Typography>
        <Typography variant="caption" color="text.secondary" sx={{ maxWidth: 420 }}>
          {this.state.error.message || t('errorBoundary.unexpected')}
        </Typography>
        <Button size="small" variant="outlined" onClick={() => this.setState({ error: null })} sx={{ mt: 1, textTransform: 'none' }}>
          {t('errorBoundary.tryAgain')}
        </Button>
      </Box>
    )
  }
}

export const ErrorBoundary = withTranslation('browse')(ErrorBoundaryInner)
