import { faXmark } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, IconButton, Stack, TextField, Tooltip, Typography } from '@mui/material'
import { useTheme } from '@mui/material/styles'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { useJobGraphMutations } from '../api/queries'
import { RESULT_COLORS, resultColor } from './resultcolors'
import type { ProdResult, ProdStep } from './types'

type Graph = ReturnType<typeof useJobGraphMutations>

// ResultsEditor is the decision's outcome table — the same control in the List
// view card and in the canvas drawer, so the two can't drift. A result is a
// label plus a palette swatch; each one is a branch of the flow, so deleting
// it takes its outgoing edge with it (the server cascades).
export function ResultsEditor({
  step,
  canWrite,
  graph,
}: {
  step: ProdStep
  canWrite: boolean
  graph: Graph
}) {
  const { t } = useTranslation('production')
  const [draft, setDraft] = useState('')

  const add = () => {
    const label = draft.trim()
    if (!label) return
    // Cycle the palette so consecutive results are distinguishable without
    // anyone having to pick a color.
    const color = RESULT_COLORS[step.results.length % RESULT_COLORS.length]
    graph.addResult.mutate({ stepId: step.id, body: { label, color } }, { onSuccess: () => setDraft('') })
  }

  return (
    <Box>
      <Stack spacing={0.5}>
        {step.results.map((r) => (
          <ResultRow key={r.id} step={step} result={r} canWrite={canWrite} graph={graph} />
        ))}
        {step.results.length === 0 && (
          <Typography variant="caption" color="text.disabled">
            {t('results.empty')}
          </Typography>
        )}
      </Stack>
      {canWrite && (
        <TextField
          size="small"
          variant="standard"
          fullWidth
          placeholder={t('results.addHint')}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && add()}
          disabled={graph.addResult.isPending}
          sx={{ mt: 0.75, '& input': { fontSize: 12 } }}
        />
      )}
    </Box>
  )
}

function ResultRow({
  step,
  result,
  canWrite,
  graph,
}: {
  step: ProdStep
  result: ProdResult
  canWrite: boolean
  graph: Graph
}) {
  const { t } = useTranslation('production')
  const theme = useTheme()
  const [label, setLabel] = useState(result.label)

  const saveLabel = () => {
    const trimmed = label.trim()
    if (trimmed && trimmed !== result.label) {
      graph.updateResult.mutate({ stepId: step.id, resultId: result.id, patch: { label: trimmed } })
    } else if (!trimmed) {
      setLabel(result.label) // labels can't be blank — revert
    }
  }

  return (
    <Stack direction="row" alignItems="center" spacing={0.75}>
      <Swatches
        selected={result.color}
        disabled={!canWrite}
        onPick={(color) =>
          graph.updateResult.mutate({ stepId: step.id, resultId: result.id, patch: { color } })
        }
      />
      {canWrite ? (
        <TextField
          variant="standard"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          onBlur={saveLabel}
          onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
          sx={{
            flex: 1,
            minWidth: 0,
            '& input': {
              fontSize: 12,
              py: 0.15,
              // The same colored underline the flow node draws, so a result
              // reads identically in both views.
              borderBottom: `2px solid ${resultColor(result.color, theme)}`,
            },
            '& .MuiInput-underline:before, & .MuiInput-underline:after': { borderBottom: 'none' },
          }}
        />
      ) : (
        <Typography
          variant="caption"
          sx={{
            flex: 1,
            minWidth: 0,
            fontSize: 12,
            borderBottom: `2px solid ${resultColor(result.color, theme)}`,
          }}
          noWrap
        >
          {result.label}
        </Typography>
      )}
      {canWrite && (
        <Tooltip title={t('results.remove')}>
          <IconButton
            size="small"
            aria-label={t('results.remove')}
            disabled={graph.removeResult.isPending}
            onClick={() => graph.removeResult.mutate({ stepId: step.id, resultId: result.id })}
          >
            <FontAwesomeIcon icon={faXmark} style={{ fontSize: 11 }} />
          </IconButton>
        </Tooltip>
      )}
    </Stack>
  )
}

// Swatches is the palette row, following the markup dialog's pattern — a ring
// on the selected dot — with the radiogroup semantics that one is missing.
function Swatches({
  selected,
  disabled,
  onPick,
}: {
  selected: string
  disabled?: boolean
  onPick: (color: string) => void
}) {
  const { t } = useTranslation('production')
  const theme = useTheme()
  return (
    <Stack
      direction="row"
      spacing={0.25}
      role="radiogroup"
      aria-label={t('results.colorLabel')}
      sx={{ flexShrink: 0 }}
    >
      {RESULT_COLORS.map((c) => (
        <Tooltip key={c} title={t(`enums:resultColor.${c}`)}>
          <Box
            component="button"
            type="button"
            role="radio"
            aria-checked={c === selected}
            aria-label={t(`enums:resultColor.${c}`)}
            disabled={disabled}
            onClick={() => onPick(c)}
            sx={{
              width: 14,
              height: 14,
              p: 0,
              borderRadius: '50%',
              bgcolor: resultColor(c, theme),
              border: 2,
              borderColor: c === selected ? 'text.primary' : 'transparent',
              cursor: disabled ? 'default' : 'pointer',
              opacity: disabled && c !== selected ? 0.4 : 1,
            }}
          />
        </Tooltip>
      ))}
    </Stack>
  )
}
