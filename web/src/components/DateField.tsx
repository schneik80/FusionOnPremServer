import { faCalendarDay, faChevronLeft, faChevronRight } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, Button, IconButton, Popover, Stack, TextField, Tooltip, Typography } from '@mui/material'
import { alpha } from '@mui/material/styles'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getDateTimeFmt } from '../fmt'
import { addDays, addMonths, fmtDay, isDay, mondayOf, monthGrid, parseDay, sameDay, today } from '../fmt/dates'

// DateField is the app's date picker: a read-only text field plus a month-grid
// popover, hand-drawn the way every other visualization here is. There is no
// date library in this project and adding @mui/x-date-pickers would drag in a
// peer date library too, so the ~40 lines of grid math live in fmt/dates.ts.
//
// The value is a `YYYY-MM-DD` string, which is what the Gantt schedule and the
// production run date already store, so this drops straight into an existing
// `<TextField type="date">` call site. Weeks start Monday in every locale, to
// agree with the Gantt's calendar bands (see mondayOf).

const CELL = 32

export function DateField({
  value,
  onChange,
  label,
  size = 'small',
  disabled,
  error,
  helperText,
  clearable,
  fullWidth,
  sx,
}: {
  /** YYYY-MM-DD, or '' for no date */
  value: string
  onChange: (next: string) => void
  label?: string
  size?: 'small' | 'medium'
  disabled?: boolean
  error?: boolean
  helperText?: string
  /** show a Clear action — only for fields where no date is valid */
  clearable?: boolean
  fullWidth?: boolean
  sx?: object
}) {
  const { t } = useTranslation('common')
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)

  const selected = isDay(value) ? parseDay(value) : null
  const display = selected ? getDateTimeFmt({ dateStyle: 'medium' }).format(selected) : ''

  return (
    <>
      <TextField
        label={label}
        value={display}
        size={size}
        disabled={disabled}
        error={error}
        helperText={helperText}
        fullWidth={fullWidth}
        sx={sx}
        // Read-only rather than parsing free text: the display string is
        // locale-formatted, so round-tripping it back to YYYY-MM-DD would need
        // a locale-aware parser this app has no reason to own.
        onClick={(e) => !disabled && setAnchor(e.currentTarget)}
        InputLabelProps={{ shrink: true }}
        InputProps={{
          readOnly: true,
          sx: { cursor: disabled ? undefined : 'pointer', '& input': { cursor: 'inherit' } },
          endAdornment: (
            <Tooltip title={t('date.open')}>
              <span>
                <IconButton
                  size="small"
                  disabled={disabled}
                  aria-label={t('date.open')}
                  onClick={(e) => {
                    e.stopPropagation()
                    setAnchor(e.currentTarget)
                  }}
                >
                  <FontAwesomeIcon icon={faCalendarDay} style={{ fontSize: 12 }} />
                </IconButton>
              </span>
            </Tooltip>
          ),
        }}
      />
      <Popover
        open={!!anchor}
        anchorEl={anchor}
        onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        transformOrigin={{ vertical: 'top', horizontal: 'left' }}
      >
        {/* Remounted per open (the Popover unmounts its children), so the
            month cursor always reopens on the selected day. */}
        <MonthGrid
          selected={selected}
          clearable={clearable}
          onPick={(d) => {
            onChange(d ? fmtDay(d) : '')
            setAnchor(null)
          }}
        />
      </Popover>
    </>
  )
}

function MonthGrid({
  selected,
  clearable,
  onPick,
}: {
  selected: Date | null
  clearable?: boolean
  onPick: (d: Date | null) => void
}) {
  const { t } = useTranslation('common')
  const now = today()
  const [focus, setFocus] = useState<Date>(selected ?? now)
  const cursor = focus // the grid always shows the month the focused day is in
  const gridRef = useRef<HTMLDivElement>(null)

  const days = useMemo(() => monthGrid(cursor), [cursor.getFullYear(), cursor.getMonth()]) // eslint-disable-line react-hooks/exhaustive-deps

  const monthFmt = getDateTimeFmt({ month: 'long', year: 'numeric' })
  const weekdayFmt = getDateTimeFmt({ weekday: 'short' })
  const weekdays = useMemo(() => {
    // Any known Monday seeds the header; the labels follow the app language.
    const monday = mondayOf(new Date(2024, 0, 3))
    return Array.from({ length: 7 }, (_, i) => weekdayFmt.format(addDays(monday, i)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Roving focus: only the focused cell is tabbable, and it takes DOM focus so
  // arrow keys keep working after a month change re-renders the grid.
  useEffect(() => {
    gridRef.current?.querySelector<HTMLButtonElement>('[data-focused="true"]')?.focus()
  }, [focus])

  const onKeyDown = (e: React.KeyboardEvent) => {
    const step: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }
    if (e.key in step) {
      e.preventDefault()
      setFocus((d) => addDays(d, step[e.key]))
    } else if (e.key === 'PageUp' || e.key === 'PageDown') {
      e.preventDefault()
      setFocus((d) => addMonths(d, e.key === 'PageUp' ? -1 : 1))
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault()
      setFocus((d) => (e.key === 'Home' ? mondayOf(d) : addDays(mondayOf(d), 6)))
    }
  }

  return (
    <Box sx={{ p: 1, width: CELL * 7 + 16 }}>
      <Stack direction="row" alignItems="center" sx={{ mb: 0.5 }}>
        <IconButton size="small" aria-label={t('date.prevMonth')} onClick={() => setFocus((d) => addMonths(d, -1))}>
          <FontAwesomeIcon icon={faChevronLeft} style={{ fontSize: 11 }} />
        </IconButton>
        <Typography variant="body2" fontWeight={600} sx={{ flex: 1, textAlign: 'center' }}>
          {monthFmt.format(cursor)}
        </Typography>
        <IconButton size="small" aria-label={t('date.nextMonth')} onClick={() => setFocus((d) => addMonths(d, 1))}>
          <FontAwesomeIcon icon={faChevronRight} style={{ fontSize: 11 }} />
        </IconButton>
      </Stack>

      <Box sx={{ display: 'grid', gridTemplateColumns: `repeat(7, ${CELL}px)` }}>
        {weekdays.map((w, i) => (
          <Typography
            key={i}
            variant="caption"
            sx={{ textAlign: 'center', color: 'text.disabled', fontSize: 10, lineHeight: '20px' }}
          >
            {w}
          </Typography>
        ))}
      </Box>

      <Box
        ref={gridRef}
        role="grid"
        onKeyDown={onKeyDown}
        sx={{ display: 'grid', gridTemplateColumns: `repeat(7, ${CELL}px)` }}
      >
        {days.map((d) => {
          const isSel = !!selected && sameDay(d, selected)
          const isToday = sameDay(d, now)
          const outside = d.getMonth() !== cursor.getMonth()
          const focused = sameDay(d, focus)
          return (
            <Box
              key={d.getTime()}
              component="button"
              type="button"
              data-focused={focused}
              tabIndex={focused ? 0 : -1}
              onClick={() => onPick(d)}
              onFocus={() => setFocus(d)}
              sx={{
                height: CELL,
                border: 0,
                borderRadius: '50%',
                p: 0,
                font: 'inherit',
                fontSize: 12,
                cursor: 'pointer',
                bgcolor: isSel ? 'primary.main' : 'transparent',
                color: isSel ? 'primary.contrastText' : outside ? 'text.disabled' : 'text.primary',
                fontWeight: isSel || isToday ? 600 : 400,
                // Today reads as a ring rather than a fill, so it can't be
                // mistaken for the selection.
                outline: isToday && !isSel ? '1px solid' : 'none',
                outlineColor: 'primary.main',
                outlineOffset: -1,
                transition: 'background-color .1s',
                '&:hover': { bgcolor: isSel ? 'primary.dark' : (th) => alpha(th.palette.primary.main, 0.12) },
                '&:focus-visible': { boxShadow: (th) => `0 0 0 2px ${alpha(th.palette.primary.main, 0.5)}` },
              }}
            >
              {d.getDate()}
            </Box>
          )
        })}
      </Box>

      <Stack direction="row" spacing={1} sx={{ mt: 0.5 }}>
        <Button size="small" onClick={() => onPick(now)} sx={{ textTransform: 'none', flex: 1 }}>
          {t('date.today')}
        </Button>
        {clearable && (
          <Button size="small" color="inherit" onClick={() => onPick(null)} sx={{ textTransform: 'none', flex: 1 }}>
            {t('date.clear')}
          </Button>
        )}
      </Stack>
    </Box>
  )
}
