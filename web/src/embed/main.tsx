import '@fontsource/montserrat/400.css'
import '@fontsource/montserrat/500.css'
import '@fontsource/montserrat/600.css'
import '@fontsource/montserrat/700.css'
// i18n must initialize before any component module evaluates useTranslation.
import '../i18n'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { setGateHandlers } from '../api/client'
import { newQueryCaches, queryDefaults } from '../api/queryDefaults'
import { installBridge } from './bridge'
import { HUB_GATE_EVENT } from './context'
import { EmbedApp } from './EmbedApp'

// The Fusion palette entry point (/embed.html). Differences from the SPA's
// main.tsx are deliberate:
// - No persister: everything here is realtime chat + auth state, exactly the
//   query keys the SPA excludes from localStorage anyway.
// - Gate overrides: a 401 must come back to /embed.html with its dm-id params
//   (via the login `next` param), not to the SPA root; a 409 hub_not_selected
//   re-runs the in-page gate instead of tearing down to the SPA's HubGate.
// - No ColorModeProvider/LocaleProvider: the palette follows Fusion's theme
//   (pushed by the add-in), not the per-hub browser preference.

installBridge()

let redirecting = false
setGateHandlers({
  onUnauthorized: () => {
    if (redirecting) return
    redirecting = true
    const next = window.location.pathname + window.location.search
    window.location.assign('/api/auth/login?next=' + encodeURIComponent(next))
  },
  onHubGate: () => {
    window.dispatchEvent(new Event(HUB_GATE_EVENT))
  },
})

// Same posture as the SPA (api/queryDefaults.ts): bounded 429 retry after the
// server's own wait, no reconnect storms.
const queryClient = new QueryClient({ defaultOptions: queryDefaults, ...newQueryCaches() })

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <EmbedApp />
    </QueryClientProvider>
  </StrictMode>,
)
