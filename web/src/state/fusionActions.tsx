import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { ApiError, api } from '../api/client'
import type { ArchiveJob, FusionAction, Item } from '../api/types'
import { useNav } from './nav'

// The three document actions — Open, Insert and Archive — and the state each
// one needs while it is in flight.
//
// Archive is a background job: it is started, then observed through a polled
// job list, and its completion is announced by the notification bell. Nothing
// here has to stay mounted for it to finish.
//
// Open and Insert are the interesting ones, because they have to reach the
// user's own machine. The server answers one of two ways:
//
//   mode 'proxy'  — server, browser and Fusion are the same machine, so the
//                   server already did it. The result is in hand.
//   mode 'launch' — the browser is elsewhere. Navigate to a fusionlocal:// URL
//                   and the helper app takes over.
//
// A URL-scheme navigation tells the page NOTHING: not whether a handler is
// registered, not whether it ran. So after launching we poll the server for the
// helper's report. Three things can happen, and each needs a different answer:
//
//   the helper reports success  -> a brief confirmation
//   the helper reports failure  -> the specific reason (Fusion closed, etc.)
//   nothing arrives at all      -> almost certainly no helper installed
//
// The last case is why the timeout exists, and why it is generous: a cold
// helper launch on Windows can include an "allow this site to open…" prompt.

export const isActiveArchive = (j: ArchiveJob) => j.status === 'queued' || j.status === 'preparing'

// LAUNCH_POLL_MS / LAUNCH_TIMEOUT_MS bound the wait for the helper's report.
// The timeout must comfortably exceed a user reading and accepting the
// browser's "open this application?" prompt, or a legitimately slow launch
// would be reported as a missing helper.
const LAUNCH_POLL_MS = 700
const LAUNCH_TIMEOUT_MS = 25_000

// FusionFeedback is what the UI shows once an action resolves. `code` is a
// stable token from the server (fusion_not_running, …) that the errors catalog
// localizes; `helperMissing` is the distinct "nothing happened at all" case,
// which needs install instructions rather than an error message.
export interface FusionFeedback {
  /** which action this is reporting on — the dialog titles differ */
  kind: 'action' | 'archive'
  action?: FusionAction
  docName: string
  status: 'ok' | 'error' | 'helperMissing'
  code?: string
}

interface FusionActionsCtx {
  /** run Open or Insert against a document; resolves when the outcome is known */
  runAction: (item: Item, dmProjectId: string, action: FusionAction) => Promise<void>
  /** true while an action is in flight for this item */
  pendingFor: (itemId: string) => FusionAction | null
  /** the outcome to show, or null; cleared by dismissFeedback */
  feedback: FusionFeedback | null
  dismissFeedback: () => void

  /**
   * Start archiving a document; the bell announces completion. versionId pins
   * the archive to one version (a production card's) — omit it for the tip.
   */
  startArchive: (item: Item, dmProjectId: string, versionId?: string) => Promise<void>
  archives: ArchiveJob[]
  /** the live job for this exact document version, if one is running */
  archiveFor: (itemId: string, versionId?: string) => ArchiveJob | undefined
  /** is this archive job still on the server and downloadable? */
  archiveReady: (id: string) => boolean
  /** fetch a finished archive and save it; surfaces failures as feedback */
  downloadArchive: (id: string, docName: string) => Promise<void>
  /** true while that fetch is in flight */
  downloadingArchive: (id: string) => boolean
  cancelArchive: (id: string) => void
  dismissArchive: (id?: string) => void
}

const Ctx = createContext<FusionActionsCtx | null>(null)

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

// filenameFromDisposition reads the name the server chose. It is already
// sanitized server-side (sanitizeDownloadName), so this only has to unquote it.
function filenameFromDisposition(header: string | null): string {
  if (!header) return ''
  const m = /filename="([^"]*)"/.exec(header)
  return m?.[1] ?? ''
}

export function FusionActionsProvider({ children }: { children: ReactNode }) {
  const nav = useNav()
  const qc = useQueryClient()
  // The browsed project, captured at call time. Both are only context for the
  // notification the server may emit — the action itself is addressed by the
  // document's own ids.
  const hubId = nav.hubId
  const projectId = nav.project?.id
  const projectName = nav.project?.name
  const [feedback, setFeedback] = useState<FusionFeedback | null>(null)
  // Which item each in-flight action belongs to, so only that document's
  // buttons show a spinner. A ref rather than state for the guard, plus state
  // for the render — the guard must be correct synchronously.
  const [pending, setPending] = useState<Record<string, FusionAction>>({})
  const pendingRef = useRef<Record<string, FusionAction>>({})

  // Poll the archive job list while anything is generating; go quiet
  // otherwise. Slower than the upload poll (2s vs 750ms) because generation is
  // minutes long and there is no byte progress to animate.
  const archivesQ = useQuery({
    queryKey: ['archives'],
    queryFn: api.archives,
    staleTime: 0,
    refetchInterval: (q) => (q.state.data?.some(isActiveArchive) ? 2000 : false),
  })
  const archives = useMemo(() => archivesQ.data ?? [], [archivesQ.data])

  // A finished job means the server just wrote a notification. The bell polls
  // on a 45-second backstop, so without this the "archive ready" row shows up
  // to three quarters of a minute after the archive itself did — long enough
  // that a page refresh looks like the thing that fixed it.
  //
  // The job list is already polled every 2s while work is in flight, so the
  // transition is known almost immediately; this just forwards it. Runs off
  // status CHANGES, but a job first seen terminal (finished while the tab was
  // backgrounded) also counts, since the bell's cache predates it too.
  const seenStatus = useRef(new Map<string, string>())
  useEffect(() => {
    const seen = seenStatus.current
    let settled = false
    for (const j of archives) {
      if (!isActiveArchive(j) && seen.get(j.id) !== j.status) settled = true
    }
    seenStatus.current = new Map(archives.map((j) => [j.id, j.status]))
    if (settled) void qc.invalidateQueries({ queryKey: ['notifs'] })
  }, [archives, qc])

  const startArchive = useCallback(
    async (item: Item, dmProjectId: string, versionId?: string) => {
      if (!hubId) return
      try {
        const list = await api.createArchive({
          hubId,
          dmProjectId,
          projectId,
          projectName,
          itemId: item.id,
          versionId,
          name: item.name,
        })
        qc.setQueryData(['archives'], list)
      } catch (e) {
        // Only the request to our own server can fail here — generation itself
        // is a background job that reports through the bell. Callers fire this
        // and forget (there is nothing to await), so a rejection left to escape
        // would be an unhandled one and the click would look like it did
        // nothing. The code carries the reason (a hub gate, a duplicate job).
        setFeedback({
          kind: 'archive',
          docName: item.name,
          status: 'error',
          code: e instanceof ApiError ? e.code : undefined,
        })
      }
    },
    [hubId, projectId, projectName, qc],
  )

  const cancelArchive = useCallback(
    (id: string) => {
      void api.cancelArchive(id).then((list) => qc.setQueryData(['archives'], list))
    },
    [qc],
  )

  const dismissArchive = useCallback(
    (id?: string) => {
      void api.dismissArchives(id).then((list) => qc.setQueryData(['archives'], list))
    },
    [qc],
  )

  // Matched on the version as well as the document: the details header archives
  // the tip and a production card archives the version it pinned, so a card
  // must not show the other one's spinner — nor be disabled by it.
  const archiveFor = useCallback(
    (itemId: string, versionId?: string) =>
      archives.find(
        (j) => j.itemId === itemId && (j.versionId ?? '') === (versionId ?? '') && isActiveArchive(j),
      ),
    [archives],
  )

  // Whether a job id is still downloadable. Notifications are persisted per
  // user, but jobs live only in the server's memory — so a "ready" entry in the
  // bell can outlive the job it points at (a restart, or the two-hour retention
  // window). Checking the polled list is what lets the row say so, instead of
  // letting the browser save the server's error envelope as a .json file.
  const archiveReady = useCallback(
    (id: string) => archives.some((j) => j.id === id && j.status === 'ready'),
    [archives],
  )

  // Downloading is a fetch, NOT an <a download>.
  //
  // A download link hands whatever comes back to the browser's download
  // manager, so any error response is silently saved as a file — which is
  // exactly what happened: a failed archive arrived as "file.json". Fetching
  // first means a failure can be read, localized and shown, and only real
  // bytes ever reach the download manager.
  //
  // The cost is holding the archive in memory as a Blob. That is acceptable
  // here: the bytes already stream through our own server, and a Fusion
  // archive is a desktop-scale file, not a video library.
  const [downloading, setDownloading] = useState<Record<string, true>>({})
  const downloadArchive = useCallback(async (id: string, docName: string) => {
    setDownloading((cur) => ({ ...cur, [id]: true }))
    try {
      const res = await fetch(api.archiveDownloadUrl(id), { credentials: 'same-origin' })
      if (!res.ok) {
        // The server's error envelope carries a stable code the errors
        // catalog localizes; a non-JSON body just leaves it undefined.
        let code: string | undefined
        try {
          code = (await res.json())?.code
        } catch {
          code = undefined
        }
        setFeedback({ kind: 'archive', docName, status: 'error', code })
        return
      }
      const blob = await res.blob()
      const name = filenameFromDisposition(res.headers.get('Content-Disposition')) || docName
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = name
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch {
      setFeedback({ kind: 'archive', docName, status: 'error' })
    } finally {
      setDownloading((cur) => {
        const { [id]: _done, ...rest } = cur
        return rest
      })
    }
  }, [])

  const downloadingArchive = useCallback((id: string) => !!downloading[id], [downloading])

  const runAction = useCallback(
    async (item: Item, dmProjectId: string, action: FusionAction) => {
      if (!hubId) return
      // One action per document at a time: a second click while Fusion is
      // still deciding would mint a second ticket and launch a second helper.
      if (pendingRef.current[item.id]) return
      pendingRef.current = { ...pendingRef.current, [item.id]: action }
      setPending(pendingRef.current)

      const done = (fb: FusionFeedback) => {
        const { [item.id]: _removed, ...rest } = pendingRef.current
        pendingRef.current = rest
        setPending(rest)
        setFeedback(fb)
      }

      try {
        const res = await api.fusionAction({
          hubId,
          dmProjectId,
          projectId,
          projectName,
          itemId: item.id,
          name: item.name,
          action,
        })

        if (res.mode === 'proxy') {
          done({
            kind: 'action',
            action,
            docName: item.name,
            status: res.ok ? 'ok' : 'error',
            code: res.errorCode,
          })
          return
        }

        if (!res.url || !res.ticket) {
          done({ kind: 'action', action, docName: item.name, status: 'error' })
          return
        }

        // Hand the URL to the OS. location.href rather than window.open: a
        // popup blocker will silently swallow an opened window, whereas a
        // same-tab scheme navigation is not treated as a popup and leaves the
        // page itself untouched (the browser does not navigate away from a
        // scheme it hands to an external handler).
        window.location.href = res.url

        const deadline = Date.now() + LAUNCH_TIMEOUT_MS
        for (;;) {
          await sleep(LAUNCH_POLL_MS)
          let outcome
          try {
            outcome = await api.fusionOutcome(res.ticket)
          } catch {
            // The ticket ages out of the server's memory eventually; treat a
            // lookup failure the same as never hearing back.
            break
          }
          if (outcome.status === 'ok') {
            done({ kind: 'action', action, docName: item.name, status: 'ok' })
            return
          }
          if (outcome.status === 'error') {
            // The server also files this failure in the inbox; show it there
            // now rather than on the next backstop poll.
            void qc.invalidateQueries({ queryKey: ['notifs'] })
            done({
              kind: 'action',
              action,
              docName: item.name,
              status: 'error',
              code: outcome.errorCode,
            })
            return
          }
          if (Date.now() > deadline) break
        }
        // Nothing ever reported. The overwhelmingly likely cause is that no
        // application is registered for the scheme, so say that rather than
        // "something went wrong".
        done({ kind: 'action', action, docName: item.name, status: 'helperMissing' })
      } catch (e) {
        // The request to our own server failed (offline, 409 hub gate, …).
        // Carry its error code through so the dialog explains that rather than
        // blaming Fusion for something that never reached it.
        done({
          kind: 'action',
          action,
          docName: item.name,
          status: 'error',
          code: e instanceof ApiError ? e.code : undefined,
        })
      }
    },
    [hubId, projectId, projectName, qc],
  )

  const pendingFor = useCallback((itemId: string) => pending[itemId] ?? null, [pending])

  const value = useMemo<FusionActionsCtx>(
    () => ({
      runAction,
      pendingFor,
      feedback,
      dismissFeedback: () => setFeedback(null),
      startArchive,
      archives,
      archiveFor,
      archiveReady,
      downloadArchive,
      downloadingArchive,
      cancelArchive,
      dismissArchive,
    }),
    [
      runAction,
      pendingFor,
      feedback,
      startArchive,
      archives,
      archiveFor,
      archiveReady,
      downloadArchive,
      downloadingArchive,
      cancelArchive,
      dismissArchive,
    ],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useFusionActions(): FusionActionsCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useFusionActions must be used within FusionActionsProvider')
  return ctx
}
