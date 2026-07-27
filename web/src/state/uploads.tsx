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
import { api } from '../api/client'
import type { UploadJob } from '../api/types'
import { useNav } from './nav'

// Uploads state: the pending-file list the lightbox builds, the background job
// list polled from the server, and app-wide drag-and-drop. Jobs live on the Go
// server — once a file's bytes are spooled there, the APS transfer continues
// even if the user navigates elsewhere in the SPA (or closes the tab); this
// provider only observes them and refreshes the affected contents queries when
// one lands.

// UploadTarget is where files would go right now: the browsed project folder.
// folderPath is the folder-name trail from the project root (empty = root),
// which is how the server addresses Data-Management folders (GraphQL folder ids
// don't translate to DM ids). projectId/folderId are the GraphQL ids the
// completion handler needs to invalidate the right contents query.
export interface UploadTarget {
  hubId: string
  projectId: string
  dmProjectId: string
  folderId: string | null
  folderPath: string[]
  label: string
}

export interface PendingFile {
  key: string
  file: File
}

export const isActiveUpload = (j: UploadJob) => j.status === 'queued' || j.status === 'uploading'

interface UploadsCtx {
  /** current drop/browse target, or null when not looking at a project root or folder */
  target: UploadTarget | null
  dialogOpen: boolean
  openDialog: () => void
  closeDialog: () => void
  pending: PendingFile[]
  addFiles: (files: File[]) => void
  removeFile: (key: string) => void
  clearPending: () => void
  /** true while pending files are being spooled to the local server */
  submitting: boolean
  startUpload: () => void
  jobs: UploadJob[]
  activeCount: number
  cancelJob: (id: string) => void
  dismissFinished: (id?: string) => void
  /** files are being dragged over the window and a target is available */
  dragActive: boolean
}

const Ctx = createContext<UploadsCtx | null>(null)

const fileKey = (f: File) => `${f.name}:${f.size}:${f.lastModified}`

// collectFiles pulls droppable files out of a drop event, skipping directory
// entries (folder upload isn't supported — DM has no folder-drop primitive and
// recursing client-side is out of scope).
function collectFiles(dt: DataTransfer | null): File[] {
  if (!dt) return []
  if (dt.items?.length) {
    const out: File[] = []
    for (const it of Array.from(dt.items)) {
      if (it.kind !== 'file') continue
      const entry = (
        it as DataTransferItem & { webkitGetAsEntry?: () => { isDirectory: boolean } | null }
      ).webkitGetAsEntry?.()
      if (entry?.isDirectory) continue
      const f = it.getAsFile()
      if (f) out.push(f)
    }
    return out
  }
  return Array.from(dt.files)
}

export function UploadsProvider({ children }: { children: ReactNode }) {
  const nav = useNav()
  const qc = useQueryClient()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [pending, setPending] = useState<PendingFile[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [dragActive, setDragActive] = useState(false)
  // Spool failures (the POST itself failed) never become server jobs, so they
  // are kept locally and merged into the job list for display.
  const [localErrors, setLocalErrors] = useState<UploadJob[]>([])

  // The target tracks the browsed location live. While the (modal) dialog is
  // open the user can't navigate, so what they see is where files go.
  const target = useMemo<UploadTarget | null>(() => {
    if (!nav.hubId || !nav.project?.altId) return null
    const folderPath = nav.folderStack.map((f) => f.name)
    return {
      hubId: nav.hubId,
      projectId: nav.project.id,
      dmProjectId: nav.project.altId,
      folderId: nav.currentFolderId,
      folderPath,
      label: [nav.project.name, ...folderPath].join(' / '),
    }
  }, [nav.hubId, nav.project, nav.folderStack, nav.currentFolderId])

  // Poll the server job list while anything is in flight; go quiet otherwise.
  // Volatile by design — never persisted (see main.tsx dehydrate filter).
  const jobsQ = useQuery({
    queryKey: ['uploads'],
    queryFn: api.uploads,
    staleTime: 0,
    refetchInterval: (q) => (q.state.data?.some(isActiveUpload) ? 750 : false),
  })
  const serverJobs = useMemo(() => jobsQ.data ?? [], [jobsQ.data])
  const jobs = useMemo(() => [...serverJobs, ...localErrors], [serverJobs, localErrors])

  // When a job completes, the target folder has a new item (or version):
  // refresh the matching contents query. Runs off status *transitions* — but a
  // job first seen as done (finished while the app was closed) also refreshes,
  // since the persisted contents cache predates it.
  const seenStatus = useRef(new Map<string, string>())
  useEffect(() => {
    const seen = seenStatus.current
    for (const j of serverJobs) {
      if (j.status === 'done' && seen.get(j.id) !== 'done') {
        if (j.folderId) {
          void qc.invalidateQueries({ queryKey: ['folderContents', j.hubId, j.folderId] })
        } else if (j.projectId) {
          void qc.invalidateQueries({ queryKey: ['projectContents', j.projectId] })
        }
      }
    }
    seenStatus.current = new Map(serverJobs.map((j) => [j.id, j.status]))
  }, [serverJobs, qc])

  const addFiles = useCallback((files: File[]) => {
    if (files.length === 0) return
    setPending((cur) => {
      const have = new Set(cur.map((p) => p.key))
      const next = [...cur]
      for (const f of files) {
        const key = fileKey(f)
        if (!have.has(key)) {
          have.add(key)
          next.push({ key, file: f })
        }
      }
      return next
    })
  }, [])

  const removeFile = useCallback(
    (key: string) => setPending((cur) => cur.filter((p) => p.key !== key)),
    [],
  )
  const clearPending = useCallback(() => setPending([]), [])

  // startUpload spools the pending files to the local server one at a time
  // (each POST resolves as soon as the server accepted the job — the APS
  // transfer is the server's business). Accepted jobs are appended to the
  // cached list optimistically so the UI reacts before the next poll.
  const startUpload = useCallback(() => {
    if (!target || pending.length === 0 || submitting) return
    const t = target
    const files = pending
    setPending([])
    setSubmitting(true)
    void (async () => {
      for (const p of files) {
        try {
          const job = await api.uploadFile(
            {
              hubId: t.hubId,
              dmProjectId: t.dmProjectId,
              projectId: t.projectId,
              folderId: t.folderId ?? undefined,
              folderPath: t.folderPath,
            },
            p.file,
          )
          qc.setQueryData<UploadJob[]>(['uploads'], (old) => [...(old ?? []), job])
        } catch (e) {
          setLocalErrors((cur) => [
            ...cur,
            {
              id: `local-${p.key}-${Date.now()}`,
              fileName: p.file.name,
              size: p.file.size,
              bytesSent: 0,
              status: 'error',
              error: e instanceof Error ? e.message : 'upload failed',
              folderPath: t.folderPath,
            },
          ])
        }
      }
      setSubmitting(false)
      void qc.invalidateQueries({ queryKey: ['uploads'] })
    })()
  }, [target, pending, submitting, qc])

  const cancelJob = useCallback(
    (id: string) => {
      if (id.startsWith('local-')) {
        setLocalErrors((cur) => cur.filter((j) => j.id !== id))
        return
      }
      void api.cancelUpload(id).then((list) => qc.setQueryData(['uploads'], list))
    },
    [qc],
  )

  const dismissFinished = useCallback(
    (id?: string) => {
      setLocalErrors((cur) => (id ? cur.filter((j) => j.id !== id) : []))
      if (id?.startsWith('local-')) return
      void api.dismissUploads(id).then((list) => qc.setQueryData(['uploads'], list))
    },
    [qc],
  )

  // App-wide drag-and-drop. Listeners live on window so dropping anywhere works
  // while a target folder is being browsed; a depth counter tracks enter/leave
  // pairs across child elements. The dialog's own drop zone stops propagation,
  // so files dropped there aren't double-added.
  const targetRef = useRef(target)
  targetRef.current = target
  useEffect(() => {
    let depth = 0
    // A drag that started inside the SPA is never a file upload. Dragging an
    // element that the browser drags natively — a card's <img> thumbnail — puts
    // 'Files' in dataTransfer.types (Chrome offers the image as a file promise),
    // so moving a document card used to read as an incoming file drag and raise
    // the drop overlay out of nowhere.
    let internal = false
    let idle: ReturnType<typeof setTimeout> | undefined

    const stop = () => {
      depth = 0
      if (idle !== undefined) {
        clearTimeout(idle)
        idle = undefined
      }
      setDragActive(false)
    }
    // Recovery watchdog. dragover fires continuously while a drag is in flight,
    // so a gap means the drag ended without us hearing the end of it: the drop
    // landed in a subtree that stops propagation (tldraw's canvas does), or the
    // drag was cancelled, or it left the window. The overlay is a full-window
    // scrim with no controls, so a missed end would strand the UI.
    const keepAlive = () => {
      if (idle !== undefined) clearTimeout(idle)
      idle = setTimeout(stop, 1200)
    }
    // Subtrees that handle their own drops (the whiteboard canvas) opt out of
    // the app-wide upload path — see data-fls-drop-owner.
    const ownsDrop = (e: DragEvent) =>
      e.target instanceof Element && !!e.target.closest('[data-fls-drop-owner]')
    const isFileDrag = (e: DragEvent) =>
      !internal && Array.from(e.dataTransfer?.types ?? []).includes('Files')

    const onDragStart = () => {
      internal = true
      stop()
    }
    const onDragEnd = () => {
      internal = false
      stop()
    }
    // Self-heal: dragend is fired at the source node, so a source that unmounts
    // mid-drag takes the end of the drag with it and would leave every later
    // file drop classified as internal. No pointer event is delivered while a
    // drag is in flight, so the next pointerdown proves there isn't one.
    const onPointerDown = () => {
      internal = false
    }
    const onDragEnter = (e: DragEvent) => {
      if (!isFileDrag(e) || !targetRef.current || ownsDrop(e)) return
      depth += 1
      keepAlive()
      setDragActive(true)
    }
    const onDragOver = (e: DragEvent) => {
      if (!isFileDrag(e) || !targetRef.current || ownsDrop(e)) return
      if (depth > 0) keepAlive()
      e.preventDefault() // required for the drop to be allowed at all
    }
    const onDragLeave = (e: DragEvent) => {
      if (!isFileDrag(e)) return
      depth = Math.max(0, depth - 1)
      if (depth === 0) stop()
    }
    const onDrop = (e: DragEvent) => {
      stop()
      if (!isFileDrag(e) || !targetRef.current || ownsDrop(e)) return
      e.preventDefault()
      const files = collectFiles(e.dataTransfer)
      if (files.length) {
        addFiles(files)
        setDialogOpen(true)
      }
    }
    // dragstart/dragend only fire for drags that begin in this document, and
    // they are watched in the capture phase so a subtree that stops propagation
    // can't hide the start (or, worse, the end) of an in-app drag from us.
    window.addEventListener('dragstart', onDragStart, true)
    window.addEventListener('dragend', onDragEnd, true)
    window.addEventListener('pointerdown', onPointerDown, true)
    window.addEventListener('dragenter', onDragEnter)
    window.addEventListener('dragover', onDragOver)
    window.addEventListener('dragleave', onDragLeave)
    window.addEventListener('drop', onDrop)
    return () => {
      if (idle !== undefined) clearTimeout(idle)
      window.removeEventListener('dragstart', onDragStart, true)
      window.removeEventListener('dragend', onDragEnd, true)
      window.removeEventListener('pointerdown', onPointerDown, true)
      window.removeEventListener('dragenter', onDragEnter)
      window.removeEventListener('dragover', onDragOver)
      window.removeEventListener('dragleave', onDragLeave)
      window.removeEventListener('drop', onDrop)
    }
  }, [addFiles])

  const value = useMemo<UploadsCtx>(
    () => ({
      target,
      dialogOpen,
      openDialog: () => setDialogOpen(true),
      closeDialog: () => setDialogOpen(false),
      pending,
      addFiles,
      removeFile,
      clearPending,
      submitting,
      startUpload,
      jobs,
      activeCount: jobs.filter(isActiveUpload).length,
      cancelJob,
      dismissFinished,
      dragActive,
    }),
    [
      target,
      dialogOpen,
      pending,
      addFiles,
      removeFile,
      clearPending,
      submitting,
      startUpload,
      jobs,
      cancelJob,
      dismissFinished,
      dragActive,
    ],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useUploads(): UploadsCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useUploads must be used within UploadsProvider')
  return ctx
}
