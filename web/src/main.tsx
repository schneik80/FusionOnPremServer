import '@fontsource/montserrat/400.css'
import '@fontsource/montserrat/500.css'
import '@fontsource/montserrat/600.css'
import '@fontsource/montserrat/700.css'
// i18n must initialize before any component module evaluates useTranslation.
import './i18n'

import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './api/client'
import { createSyncStoragePersister } from '@tanstack/query-sync-storage-persister'
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { QUERY_CACHE_KEY } from './queryPersist'
import { ColorModeProvider } from './state/colorMode'
import { LocaleProvider } from './state/locale'

const DAY = 24 * 60 * 60 * 1000

// gcTime must outlive maxAge so inactive queries survive long enough to persist.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      gcTime: DAY,
      // One retry for transient failures, but NEVER retry a 429: the server is
      // rate-limited (APS's per-minute query-point quota) and a retry just
      // spends more of the quota. The backend already fails these fast.
      retry: (failureCount, error) =>
        error instanceof ApiError && error.status === 429 ? false : failureCount < 1,
    },
  },
})

// Persist the browsing cache so a reload paints hubs / projects / contents /
// details instantly, then revalidates in the background.
const persister = createSyncStoragePersister({
  storage: window.localStorage,
  key: QUERY_CACHE_KEY,
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <PersistQueryClientProvider
      client={queryClient}
      persistOptions={{
        persister,
        maxAge: DAY,
        // Bump when query shapes change to invalidate stale persisted caches.
        buster: 'fls-12',
        dehydrateOptions: {
          // Persist only successful, non-volatile queries. Auth state must stay
          // fresh (and persisting it could briefly show a prior user's state);
          // upload jobs are live server state and are re-polled on load; and
          // chat, tasks, and production never persist — they're realtime,
          // per-user data that must not linger in shared-machine localStorage.
          // ('task' covers task/tasks/tasksMine; 'prod' covers
          // prodJob/prodJobs; 'whiteboard' covers the board list.)
          shouldDehydrateQuery: (q) =>
            q.state.status === 'success' &&
            q.queryKey[0] !== 'authMe' &&
            q.queryKey[0] !== 'uploads' &&
            !String(q.queryKey[0]).startsWith('chat') &&
            !String(q.queryKey[0]).startsWith('task') &&
            !String(q.queryKey[0]).startsWith('prod') &&
            !String(q.queryKey[0]).startsWith('whiteboard'),
        },
      }}
    >
      <ColorModeProvider>
        <LocaleProvider>
          <App />
        </LocaleProvider>
      </ColorModeProvider>
    </PersistQueryClientProvider>
  </StrictMode>,
)
