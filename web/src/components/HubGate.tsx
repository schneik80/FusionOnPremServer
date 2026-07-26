import { Alert, Box, Button, CircularProgress, Paper, Stack, Typography } from '@mui/material'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useHubs, useSetSessionHub } from '../api/queries'
import type { Item } from '../api/types'
import { loadLastHub, saveLastHub } from '../state/hubKeys'
import { HubList } from './HubList'

// HubGate stands between login and the app while the session has no hub lock
// (every data route 409s code=hub_not_selected until POST /api/session/hub).
// It replaces the old AppLayout auto-restore effect with a three-way flow:
//
//   1. authMe.selectedHubId set (persisted session) — the Gate in App.tsx
//      never renders this component; the app enters directly.
//   2. fls.lastHub remembered AND still in the user's hub list — auto-POST
//      the lock and enter; only a spinner is shown (no picker flash).
//   3. Otherwise — first run, or the remembered hub was revoked (notice) —
//      the full-screen picker.
//
// A successful lock lands the refreshed AuthMe in the authMe cache
// (useSetSessionHub), which re-renders the Gate and unmounts this component.
export function HubGate() {
  const { t } = useTranslation('browse')
  const hubsQ = useHubs()
  const setHub = useSetSessionHub()
  const [selected, setSelected] = useState<Item | null>(null)
  // Read once: nav/gate flows may rewrite fls.lastHub while we're mounted.
  const remembered = useMemo(() => loadLastHub(), [])
  const autoTried = useRef(false)

  const hubs = hubsQ.data
  const rememberedHub =
    remembered && hubs ? hubs.find((h) => h.id === remembered.id) : undefined
  const revoked = !!remembered && !!hubs && !rememberedHub

  // Auto-relock: the remembered hub is still in the list → lock without
  // showing the picker. The ref guards StrictMode's double effect run (a
  // duplicate POST would be a harmless re-lock, but one is enough).
  useEffect(() => {
    if (autoTried.current || !rememberedHub) return
    autoTried.current = true
    setHub.mutate(rememberedHub.id, {
      onSuccess: () => saveLastHub(rememberedHub.id, rememberedHub.name),
    })
  }, [rememberedHub, setHub])

  // Spinner while the hub list loads, and across the whole auto-relock path
  // (about to fire / in flight / succeeded-and-unmounting). A failed
  // auto-relock falls through to the picker with the error shown.
  if (hubsQ.isLoading || (rememberedHub && !setHub.isError)) {
    return (
      <Box sx={{ height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <CircularProgress />
      </Box>
    )
  }

  const enter = () => {
    if (!selected || setHub.isPending) return
    const hub = selected
    setHub.mutate(hub.id, {
      // Saved for the NEXT load (auto-relock + per-hub setting keys); this
      // mount's theme providers already resolved their keys.
      onSuccess: () => saveLastHub(hub.id, hub.name),
    })
  }

  return (
    <Box
      sx={{
        height: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        p: 2,
      }}
    >
      <Paper elevation={3} sx={{ p: 4, maxWidth: 440, width: '100%' }}>
        <Stack spacing={2}>
          <Typography variant="h5" sx={{ fontWeight: 600 }}>
            {t('hubGate.title')}
          </Typography>
          <Typography variant="body2" color="text.secondary">
            {t('hubGate.subtitle')}
          </Typography>
          {revoked && (
            <Alert severity="warning">
              {t('hubGate.revoked', { name: remembered?.name || remembered?.id })}
            </Alert>
          )}
          {setHub.isError && (
            <Alert severity="error">{(setHub.error as Error).message}</Alert>
          )}
          <Box
            sx={{
              border: 1,
              borderColor: 'divider',
              borderRadius: 1,
              maxHeight: 300,
              overflowY: 'auto',
            }}
          >
            <HubList
              selectedId={selected?.id}
              onSelect={setSelected}
              disabled={setHub.isPending}
            />
          </Box>
          <Button
            variant="contained"
            fullWidth
            disabled={!selected || setHub.isPending}
            onClick={enter}
          >
            {setHub.isPending ? t('hubGate.entering') : t('hubGate.enter')}
          </Button>
        </Stack>
      </Paper>
    </Box>
  )
}
