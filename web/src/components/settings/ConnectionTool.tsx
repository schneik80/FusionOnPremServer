import { Alert, Box, Button, Stack, TextField, Typography } from '@mui/material'
import { useEffect, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useMeta, useSetPort } from '../../api/queries'
import { Field } from './Field'

// ConnectionTool: the server's network-facing settings — listen port (with
// the restart/reconnect flow), the read-only APS region, and the build note.

const MIN_PORT = 1024
const MAX_PORT = 65535

// Reconnect delay after a port change. The server rebinds within ~0.5s of
// acking, so a short pause lets the new listener come up before we navigate.
const RECONNECT_DELAY_MS = 2500

export function ConnectionTool({ active }: { active: boolean }) {
  const { t } = useTranslation('settings')
  const metaQ = useMeta()
  const meta = metaQ.data

  return (
    <Stack spacing={3}>
      <Field label={t('port.label')}>
        <PortSetting open={active} />
      </Field>

      <Field label={t('region.label')}>
        <Typography variant="body2">{meta?.region ?? '—'}</Typography>
        <Typography variant="caption" color="text.secondary">
          {t('region.help')}
        </Typography>
      </Field>

      <Field label={t('about.label')}>
        <Typography variant="caption" color="text.secondary">
          {t('about.buildNote')}
        </Typography>
      </Field>
    </Stack>
  )
}

// PortSetting shows the current listen port and, when the server owns it,
// lets the user change it. Applying persists the port and restarts the
// listener, so we then redirect the browser to the new port.
function PortSetting({ open }: { open: boolean }) {
  const { t } = useTranslation('settings')
  const metaQ = useMeta()
  const meta = metaQ.data
  const setPort = useSetPort()

  const [value, setValue] = useState('')
  const [reconnectTo, setReconnectTo] = useState<string | null>(null)

  // Reset the field to the live port each time the dialog opens.
  useEffect(() => {
    if (open && meta?.port) setValue(String(meta.port))
  }, [open, meta?.port])

  if (!meta) {
    return <Typography variant="body2">—</Typography>
  }

  if (!meta.portConfigurable) {
    return (
      <>
        <Typography variant="body2">{meta.port}</Typography>
        <Typography variant="caption" color="text.secondary">
          <Trans t={t} i18nKey="port.fixedAtStartup" components={{ cmd: <code /> }} />
        </Typography>
      </>
    )
  }

  if (reconnectTo) {
    return (
      <Alert severity="info" sx={{ py: 0.5 }}>
        <Trans
          t={t}
          i18nKey="port.restarting"
          values={{ url: reconnectTo }}
          components={{ lnk: <Box component="a" href={reconnectTo} sx={{ wordBreak: 'break-all' }} /> }}
        />
      </Alert>
    )
  }

  const parsed = Number(value)
  const valid = Number.isInteger(parsed) && parsed >= MIN_PORT && parsed <= MAX_PORT
  const unchanged = parsed === meta.port
  const canApply = valid && !unchanged && !setPort.isPending

  const apply = () => {
    if (!canApply) return
    setPort.mutate(parsed, {
      onSuccess: (res) => {
        if (!res.restarting) return
        const url = new URL(window.location.href)
        url.port = String(res.port)
        // Navigate to exactly what we display. The origin root is enough — the
        // SPA keeps nav state in memory, so a reload starts fresh regardless.
        const target = url.origin
        setReconnectTo(target)
        window.setTimeout(() => {
          window.location.href = target
        }, RECONNECT_DELAY_MS)
      },
    })
  }

  return (
    <Stack spacing={1}>
      <Stack direction="row" spacing={1} alignItems="flex-start">
        <TextField
          size="small"
          type="number"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && apply()}
          error={value !== '' && !valid}
          inputProps={{ min: MIN_PORT, max: MAX_PORT }}
          sx={{ width: 120 }}
        />
        <Button size="small" variant="outlined" disabled={!canApply} onClick={apply}>
          {setPort.isPending ? t('port.applying') : t('port.applyRestart')}
        </Button>
      </Stack>
      {setPort.error && (
        <Typography variant="caption" color="error">
          {(setPort.error as Error).message}
        </Typography>
      )}
      <Typography variant="caption" color="text.secondary">
        {t('port.rangeHelp', { min: MIN_PORT, max: MAX_PORT })}
      </Typography>
    </Stack>
  )
}
