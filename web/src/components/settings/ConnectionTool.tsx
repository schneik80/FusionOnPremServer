import { Alert, Box, Button, Stack, TextField, Typography } from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useAuthMe, useMeta, useSetPort, useSetSessionHub } from '../../api/queries'
import type { Item } from '../../api/types'
import { saveLastHub } from '../../state/hubKeys'
import { teardownAndReload } from '../../state/teardown'
import { HubList } from '../HubList'
import { Field } from './Field'

// ConnectionTool: the session's hub lock (the ONLY place hubs are switched),
// the listen port (with the restart/reconnect flow), the read-only APS
// region, and the build note.

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
      <Field label={t('hub.label')}>
        <HubSetting />
      </Field>

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

// HubSetting lists the user's hubs with the session's locked hub marked.
// Picking a different one shows the port-change-style warning; Apply re-locks
// the session (POST /api/session/hub — no re-OAuth), saves fls.lastHub so the
// next mount's per-hub settings and auto-relock target the new hub, then runs
// the shared teardown (clear caches + reload at the root).
function HubSetting() {
  const { t } = useTranslation('settings')
  const qc = useQueryClient()
  const authQ = useAuthMe()
  const currentId = authQ.data?.selectedHubId ?? null
  const [selected, setSelected] = useState<Item | null>(null)
  const setHub = useSetSessionHub()

  // The pending switch target: a selection that differs from the lock.
  const target = selected && selected.id !== currentId ? selected : null

  const apply = () => {
    if (!target || setHub.isPending) return
    const hub = target
    setHub.mutate(hub.id, {
      onSuccess: () => {
        // Order matters: fls.lastHub must be saved BEFORE the reload — the
        // per-hub client-setting keys are resolved from it at mount.
        saveLastHub(hub.id, hub.name)
        teardownAndReload(qc)
      },
    })
  }

  return (
    <Stack spacing={1}>
      <Box
        sx={{
          border: 1,
          borderColor: 'divider',
          borderRadius: 1,
          maxHeight: 240,
          overflowY: 'auto',
        }}
      >
        <HubList
          selectedId={selected?.id ?? currentId}
          currentId={currentId}
          onSelect={setSelected}
          disabled={setHub.isPending}
        />
      </Box>
      {target && (
        <>
          <Alert severity="warning" sx={{ py: 0.5 }}>
            {t('hub.switchWarning')}
          </Alert>
          <Box>
            <Button size="small" variant="outlined" disabled={setHub.isPending} onClick={apply}>
              {setHub.isPending ? t('hub.applying') : t('hub.apply')}
            </Button>
          </Box>
        </>
      )}
      {setHub.error && (
        <Typography variant="caption" color="error">
          {(setHub.error as Error).message}
        </Typography>
      )}
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
