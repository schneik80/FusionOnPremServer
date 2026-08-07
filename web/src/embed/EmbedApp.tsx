import {
  Alert,
  Box,
  Button,
  CircularProgress,
  CssBaseline,
  Stack,
  ThemeProvider,
  Typography,
} from '@mui/material'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../api/client'
import { useAuthMe } from '../api/queries'
import type { Item } from '../api/types'
import { ChatApp } from '../chat/ChatApp'
import { useChatEvents } from '../chat/useChatEvents'
import { makeTheme } from '../theme'
import {
  contextFromPayload,
  contextFromSearch,
  HUB_GATE_EVENT,
  SET_CONTEXT_EVENT,
  type EmbedContext,
} from './context'
import { EmbedNavProvider } from './EmbedNavProvider'

// EmbedApp is the Fusion palette's page: the gate chain the SPA spreads over
// App/HubGate/AppLayout, collapsed to one project's chat. States: loading →
// sign-in → resolving (DM ids → GraphQL ids) → hub consent (only when the
// session is locked to a DIFFERENT hub — switching is session-wide, so it
// must be explicit) → chat.
export function EmbedApp() {
  const [ctx, setCtx] = useState<EmbedContext>(() => contextFromSearch(window.location.search))
  const qc = useQueryClient()

  // Context pushes from Fusion (document switched, theme flipped).
  useEffect(() => {
    const onSet = (e: Event) => {
      setCtx((cur) => contextFromPayload(cur, (e as CustomEvent).detail))
    }
    window.addEventListener(SET_CONTEXT_EVENT, onSet)
    return () => window.removeEventListener(SET_CONTEXT_EVENT, onSet)
  }, [])

  // A mid-session 409 hub_not_selected (server restarted with a pre-hub
  // session) re-runs the resolve → auto-lock chain instead of reloading.
  useEffect(() => {
    const onGate = () => {
      void qc.invalidateQueries({ queryKey: ['authMe'] })
      void qc.invalidateQueries({ queryKey: ['resolveProject'] })
    }
    window.addEventListener(HUB_GATE_EVENT, onGate)
    return () => window.removeEventListener(HUB_GATE_EVENT, onGate)
  }, [qc])

  const theme = useMemo(() => makeTheme(ctx.theme), [ctx.theme])

  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <Box
        sx={{
          height: '100vh',
          display: 'flex',
          flexDirection: 'column',
          bgcolor: 'background.default',
        }}
      >
        <EmbedGate ctx={ctx} />
      </Box>
    </ThemeProvider>
  )
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <Stack
      spacing={2}
      alignItems="center"
      justifyContent="center"
      sx={{ flex: 1, p: 3, textAlign: 'center' }}
    >
      {children}
    </Stack>
  )
}

function signInWithReturn() {
  const next = window.location.pathname + window.location.search
  window.location.assign('/api/auth/login?next=' + encodeURIComponent(next))
}

function EmbedGate({ ctx }: { ctx: EmbedContext }) {
  const { t } = useTranslation('embed')
  const qc = useQueryClient()
  const meQ = useAuthMe()
  const authenticated = meQ.data?.authenticated === true

  const resolveQ = useQuery({
    queryKey: ['resolveProject', ctx.dmHubId, ctx.dmProjectId],
    queryFn: () => api.resolveProject(ctx.dmHubId, ctx.dmProjectId),
    enabled: authenticated && !!ctx.dmHubId && !!ctx.dmProjectId,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
  const resolved = resolveQ.data

  const lock = useMutation({
    mutationFn: (hubId: string) => api.sessionHub(hubId),
    onSuccess: (me) => {
      qc.setQueryData(['authMe'], me)
      void qc.invalidateQueries({ queryKey: ['resolveProject'] })
    },
  })

  // A fresh session (no hub lock at all) locks silently — there is nothing to
  // tear down. A session on ANOTHER hub falls through to the consent card.
  const needsLock = !!resolved && resolved.sessionHubId === ''
  useEffect(() => {
    if (needsLock && resolved && !lock.isPending) lock.mutate(resolved.hubId)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [needsLock, resolved?.hubId])

  if (!ctx.dmHubId || !ctx.dmProjectId) {
    return (
      <Centered>
        <Typography variant="h6">{t('noContext.title')}</Typography>
        <Typography variant="body2" color="text.secondary">
          {t('noContext.body')}
        </Typography>
      </Centered>
    )
  }

  if (meQ.isLoading) {
    return (
      <Centered>
        <CircularProgress size={28} />
      </Centered>
    )
  }

  if (!authenticated) {
    return (
      <Centered>
        <Typography variant="h6">{t('signIn.title')}</Typography>
        <Typography variant="body2" color="text.secondary">
          {t('signIn.body')}
        </Typography>
        <Button variant="contained" onClick={signInWithReturn}>
          {t('signIn.button')}
        </Button>
      </Centered>
    )
  }

  if (resolveQ.isError) {
    const err = resolveQ.error
    const code = err instanceof ApiError ? err.code : undefined
    const msg =
      code === 'hub_not_found'
        ? t('error.hubNotFound')
        : code === 'project_not_found'
          ? t('error.projectNotFound', { name: ctx.projectName || ctx.dmProjectId })
          : t('error.generic')
    return (
      <Centered>
        <Alert severity="warning" sx={{ textAlign: 'left' }}>
          {msg}
        </Alert>
        <Button onClick={() => void resolveQ.refetch()}>{t('error.retry')}</Button>
      </Centered>
    )
  }

  if (!resolved || needsLock || lock.isPending) {
    return (
      <Centered>
        <CircularProgress size={28} />
        <Typography variant="body2" color="text.secondary">
          {t('connecting', { name: ctx.projectName || '…' })}
        </Typography>
        {lock.isError && (
          <Alert severity="warning" sx={{ textAlign: 'left' }}>
            {t('error.lockFailed')}
          </Alert>
        )}
      </Centered>
    )
  }

  if (resolved.sessionHubId !== resolved.hubId) {
    const sessionHubName = meQ.data?.selectedHubName || resolved.sessionHubId
    return (
      <Centered>
        <Typography variant="h6">{t('hubConsent.title')}</Typography>
        <Typography variant="body2" color="text.secondary">
          {t('hubConsent.body', { hub: resolved.hubName, sessionHub: sessionHubName })}
        </Typography>
        <Button
          variant="contained"
          disabled={lock.isPending}
          onClick={() => lock.mutate(resolved.hubId)}
        >
          {t('hubConsent.confirm', { hub: resolved.hubName })}
        </Button>
        {lock.isError && (
          <Alert severity="warning" sx={{ textAlign: 'left' }}>
            {t('error.lockFailed')}
          </Alert>
        )}
      </Centered>
    )
  }

  return <EmbedChat resolved={resolved} channelId={ctx.channelId} />
}

function EmbedChat({
  resolved,
  channelId,
}: {
  resolved: { hubId: string; hubName: string; projectId: string; projectName: string; projectAltId: string }
  channelId: string | null
}) {
  const project: Item = useMemo(
    () => ({
      id: resolved.projectId,
      name: resolved.projectName,
      kind: 'project',
      altId: resolved.projectAltId,
      isContainer: true,
    }),
    [resolved.projectId, resolved.projectName, resolved.projectAltId],
  )
  const { live } = useChatEvents(resolved.projectId)

  return (
    <EmbedNavProvider
      key={resolved.projectId}
      hubId={resolved.hubId}
      hubName={resolved.hubName}
      project={project}
      initialChannelId={channelId}
    >
      <Box sx={{ flex: 1, display: 'flex', minHeight: 0 }}>
        <ChatApp active live={live} collapsibleChannels />
      </Box>
    </EmbedNavProvider>
  )
}
