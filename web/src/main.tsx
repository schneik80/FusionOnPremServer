import '@fontsource/montserrat/400.css'
import '@fontsource/montserrat/500.css'
import '@fontsource/montserrat/600.css'
import '@fontsource/montserrat/700.css'
// i18n must initialize before any component module evaluates useTranslation.
import './i18n'

import { QueryClient } from '@tanstack/react-query'
import { DAY, newQueryCaches, queryDefaults } from './api/queryDefaults'
import { createSyncStoragePersister } from '@tanstack/query-sync-storage-persister'
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { QUERY_CACHE_KEY } from './queryPersist'
import { ColorModeProvider } from './state/colorMode'
import { LocaleProvider } from './state/locale'
import { ThemeOverridesProvider } from './state/themeOverrides'

// The query posture (staleness, bounded 429 retry, no reconnect storms) lives
// in api/queryDefaults.ts, shared with the embed entry point. gcTime (DAY)
// must outlive the persister's maxAge so inactive queries survive to persist.
const queryClient = new QueryClient({ defaultOptions: queryDefaults, ...newQueryCaches() })

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
        buster: 'fls-19',
        dehydrateOptions: {
          // Persist only successful, non-volatile queries. Auth state must stay
          // fresh (and persisting it could briefly show a prior user's state);
          // upload and archive jobs are live server state (session-scoped, and
          // dropped on restart) and are re-polled on load; and
          // chat, tasks, and production never persist — they're realtime,
          // per-user data that must not linger in shared-machine localStorage.
          // ('task' covers task/tasks/tasksMine; 'prod' covers
          // prodJob/prodJobs; 'whiteboard' covers the board list; 'admin'
          // covers adminStatus/adminLogTail — live server state, re-fetched
          // whenever the Settings console opens; 'notif' covers the bell's
          // per-user inbox + unread count, which are realtime and per-user;
          // 'localRefs' is the Where-Used graph's local sources, which read
          // all of the above and inherit the same posture; 'resolveProject' is
          // the Fusion deep link's id resolution, whose answer reports the
          // session's CURRENT hub lock — the gate branches on that, so a
          // day-old copy would send it down the wrong path.)
          shouldDehydrateQuery: (q) =>
            q.state.status === 'success' &&
            q.queryKey[0] !== 'authMe' &&
            q.queryKey[0] !== 'quota' &&
            q.queryKey[0] !== 'resolveProject' &&
            q.queryKey[0] !== 'uploads' &&
            q.queryKey[0] !== 'archives' &&
            !String(q.queryKey[0]).startsWith('chat') &&
            !String(q.queryKey[0]).startsWith('task') &&
            !String(q.queryKey[0]).startsWith('prod') &&
            !String(q.queryKey[0]).startsWith('whiteboard') &&
            !String(q.queryKey[0]).startsWith('notif') &&
            !String(q.queryKey[0]).startsWith('localRefs') &&
            !String(q.queryKey[0]).startsWith('admin'),
        },
      }}
    >
      <ColorModeProvider>
        <LocaleProvider>
          <ThemeOverridesProvider>
            <App />
          </ThemeOverridesProvider>
        </LocaleProvider>
      </ColorModeProvider>
    </PersistQueryClientProvider>
  </StrictMode>,
)
