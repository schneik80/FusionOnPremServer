import { Alert, Box, Button, CircularProgress, Paper, Stack, Typography } from '@mui/material'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../api/client'
import { useAuthMe, useSetSessionHub } from '../api/queries'
import { resolvedPermalink, type DmDeepLink } from '../state/dmDeepLink'
import { saveLastHub } from '../state/hubKeys'
import { teardownAndReload } from '../state/teardown'

// DmDeepLinkGate stands between login and the app when the URL carries the
// Fusion add-in's Data Management ids (see state/dmDeepLink.ts). It does what
// the palette's EmbedGate does — resolve the ids, lock the session hub, ask
// first when the session is on a *different* hub (a switch is session-wide) —
// and then rewrites the URL to the equivalent permalink and steps aside. The
// normal Gate chain runs afterwards: NavProvider reads that permalink at
// mount like any other.
//
// The copy is the embed namespace's: same situation, same words, already
// translated. Both entry points are "Fusion sent you here".
export function DmDeepLinkGate({ link, onDone }: { link: DmDeepLink; onDone: () => void }) {
  const { t } = useTranslation('embed')
  const qc = useQueryClient()
  const meQ = useAuthMe()
  const setHub = useSetSessionHub()

  // Where to come back to after a hub switch reloads the client: this URL,
  // deep link intact, so the second pass resolves against the new lock.
  const here = useMemo(
    () => (typeof window === 'undefined' ? '/' : window.location.pathname + window.location.search),
    [],
  )

  // Always fresh: the answer carries the session's current hub lock, which is
  // exactly what the branches below turn on. One call, once, at launch.
  const resolveQ = useQuery({
    queryKey: ['resolveProject', link.dmHubId, link.dmProjectId],
    queryFn: () => api.resolveProject(link.dmHubId, link.dmProjectId),
    staleTime: 0,
    gcTime: 0,
    retry: false,
  })
  const resolved = resolveQ.data

  // The hub this gate locked, if it did. A successful POST /api/session/hub is
  // authoritative: the resolve answer reported the lock as it was before, and
  // re-reading it to learn what we already know only adds a round trip to get
  // stuck in.
  const [lockedHub, setLockedHub] = useState<string | null>(null)
  const onRightHub =
    !!resolved && (resolved.sessionHubId === resolved.hubId || lockedHub === resolved.hubId)

  // A session with no hub lock at all locks silently — there is nothing
  // cached to tear down. A session on ANOTHER hub falls through to consent.
  const needsLock = !!resolved && resolved.sessionHubId === '' && lockedHub === null
  const autoLocked = useRef(false)
  useEffect(() => {
    if (!needsLock || !resolved || autoLocked.current) return
    autoLocked.current = true
    setHub.mutate(resolved.hubId, {
      onSuccess: () => {
        saveLastHub(resolved.hubId, resolved.hubName)
        setLockedHub(resolved.hubId)
      },
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [needsLock, resolved?.hubId])

  // Resolved, and the session is on the right hub: become an ordinary
  // permalink. replaceState (not push) — the deep link is a launcher, not a
  // place worth going back to.
  useEffect(() => {
    if (!onRightHub || !resolved) return
    window.history.replaceState(null, '', resolvedPermalink(resolved))
    onDone()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onRightHub])

  // Give up on the deep link but not on the app: drop the ids from the URL so
  // a refresh does not re-ask, and let the normal gates take over.
  const skip = () => {
    window.history.replaceState(null, '', '/')
    onDone()
  }

  if (resolveQ.isError) {
    const err = resolveQ.error
    const code = err instanceof ApiError ? err.code : undefined
    const msg =
      code === 'hub_not_found'
        ? t('error.hubNotFound')
        : code === 'project_not_found'
          ? t('error.projectNotFound', { name: link.projectName || link.dmProjectId })
          : t('error.generic')
    return (
      <Centered>
        <Alert severity="warning" sx={{ textAlign: 'left' }}>
          {msg}
        </Alert>
        <Stack direction="row" spacing={1} justifyContent="center">
          <Button onClick={() => void resolveQ.refetch()}>{t('error.retry')}</Button>
          <Button variant="contained" onClick={skip}>
            {t('deepLink.continue')}
          </Button>
        </Stack>
      </Centered>
    )
  }

  if (resolved && !onRightHub && !needsLock) {
    const sessionHubName = meQ.data?.selectedHubName || resolved.sessionHubId
    const confirm = () => {
      if (setHub.isPending) return
      setHub.mutate(resolved.hubId, {
        onSuccess: () => {
          // Same order as Settings → Connection: fls.lastHub before the
          // teardown, because the per-hub client-setting keys are resolved
          // from it at mount. The reload returns HERE, deep link and all.
          saveLastHub(resolved.hubId, resolved.hubName)
          teardownAndReload(qc, { navigate: () => window.location.assign(here) })
        },
      })
    }
    return (
      <Centered>
        <Typography variant="h6">{t('hubConsent.title')}</Typography>
        <Typography variant="body2" color="text.secondary">
          {t('hubConsent.body', { hub: resolved.hubName, sessionHub: sessionHubName })}
        </Typography>
        <Stack direction="row" spacing={1} justifyContent="center">
          <Button onClick={skip}>{t('deepLink.continue')}</Button>
          <Button variant="contained" disabled={setHub.isPending} onClick={confirm}>
            {t('hubConsent.confirm', { hub: resolved.hubName })}
          </Button>
        </Stack>
        {setHub.isError && (
          <Alert severity="warning" sx={{ textAlign: 'left' }}>
            {t('error.lockFailed')}
          </Alert>
        )}
      </Centered>
    )
  }

  // A failed auto-lock is the one dead end here: the hub cannot be taken, and
  // retrying it silently is what the ref above rules out. Offer the way on.
  if (setHub.isError) {
    return (
      <Centered>
        <Alert severity="warning" sx={{ textAlign: 'left' }}>
          {t('error.lockFailed')}
        </Alert>
        <Button variant="contained" onClick={skip}>
          {t('deepLink.continue')}
        </Button>
      </Centered>
    )
  }

  // Resolving, locking, or the tick between ready and the parent unmounting us.
  return (
    <Centered>
      <CircularProgress size={28} />
      <Typography variant="body2" color="text.secondary">
        {t('connecting', { name: link.projectName || '…' })}
      </Typography>
    </Centered>
  )
}

function Centered({ children }: { children: ReactNode }) {
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
        <Stack spacing={2} sx={{ textAlign: 'center' }}>
          {children}
        </Stack>
      </Paper>
    </Box>
  )
}
