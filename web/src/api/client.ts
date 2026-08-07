// Thin typed fetch wrapper over the Go JSON API. All endpoints live under
// /api and are same-origin (the SPA is served by the Go server; in dev, Vite
// proxies /api to it). IDs are GraphQL URNs containing ':' and '/', so they
// always travel as query params, never path segments.

import type {
  ActivityReport,
  AdminStatus,
  AuthMe,
  BackupConfig,
  BackupList,
  BackupRestoreResponse,
  BackupSummary,
  BackupVerifyReport,
  BOMRow,
  Classify,
  CleanupResult,
  ComponentRef,
  Contents,
  Details,
  DiskUsage,
  DrawingRef,
  FsDirs,
  GroupMember,
  HubOverview,
  Item,
  LocalRefKind,
  LocalRefs,
  Location,
  Logo,
  Meta,
  NamedProperty,
  PhysicalProperties,
  PermLayer,
  Pin,
  ProjectDataDeleteResult,
  ProjectGroup,
  ResolvedProject,
  SetPortResponse,
  Thumbnail,
  UploadJob,
  WikiImageResult,
  WikiPage,
  WikiPageContent,
} from './types'
import type {
  ChatChannel,
  ChatChannelList,
  ChatImage,
  ChatMember,
  ChatMessage,
  ChatMessageList,
  ChatUnread,
  ChatUnreadList,
} from '../chat/types'
import type { MyTasks, Task, TaskDraft, TaskList, TaskPatch, TaskShiftResult } from '../tasks/types'
import type { NotifCount, NotificationList } from '../notifications/types'
import type {
  BatchDraft,
  BatchPatch,
  DocPin,
  FulfillInput,
  Job,
  JobDraft,
  JobList,
  JobPatch,
  MyProduction,
  PlaceholderDraft,
  PlaceholderPatch,
  ProdBatch,
  StepDraft,
  StepPatch,
} from '../production/types'
import type {
  Whiteboard,
  WhiteboardDraft,
  WhiteboardList,
  WhiteboardPatch,
} from '../whiteboards/types'
import { QUERY_CACHE_KEY } from '../queryPersist'

export class ApiError extends Error {
  status: number
  // Stable machine token from the server's error envelope; the SPA maps it
  // to localized text (i18n/apiError.ts) and falls back to `message`.
  code?: string
  constructor(status: number, message: string, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

// redirectToLogin sends the browser into the OAuth flow when a data call comes
// back 401 (session lost or expired). Guarded so a burst of parallel 401s only
// navigates once. A full navigation (not fetch) is required so the browser
// follows the server's 302 to Autodesk and accepts the Set-Cookie.
let redirecting = false
function redirectToLogin() {
  if (redirecting) return
  redirecting = true
  window.location.assign('/api/auth/login')
}

// resetForHubGate handles 409 code=hub_not_selected: the session lost its hub
// lock (server restarted with a pre-hub persisted session, or a stale tab
// outlived a switch). Drop the persisted query cache and reload at the root so
// the HubGate can re-lock — the in-memory cache dies with the reload. Guarded
// like redirectToLogin so a burst of parallel 409s navigates once. Never loops:
// after the reload the gate blocks all data calls until a hub is locked again.
let resettingHub = false
function resetForHubGate() {
  if (resettingHub) return
  resettingHub = true
  try {
    localStorage.removeItem(QUERY_CACHE_KEY)
  } catch {
    /* storage unavailable — nothing to clear */
  }
  window.location.assign('/')
}

// The 401/409 reactions above are full-page navigations into the SPA shell —
// right for the app, wrong for any other entry point (the Fusion palette's
// /embed.html must return to itself after login, not to '/'). Entry points
// override them once at bootstrap; the defaults keep the SPA's behavior
// without main.tsx doing anything.
export interface GateHandlers {
  onUnauthorized: () => void
  onHubGate: () => void
}
let gate: GateHandlers = { onUnauthorized: redirectToLogin, onHubGate: resetForHubGate }
export function setGateHandlers(h: GateHandlers) {
  gate = h
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  // Let the browser set the multipart boundary for FormData bodies (image
  // uploads); JSON calls get the explicit content-type.
  const isForm = init?.body instanceof FormData
  const res = await fetch(path, {
    headers: isForm ? undefined : { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    ...init,
  })
  if (!res.ok) {
    let msg = `request failed (HTTP ${res.status})`
    let code: string | undefined
    try {
      const body = (await res.json()) as { error?: string; code?: string }
      if (body?.error) msg = body.error
      if (body?.code) code = body.code
    } catch {
      /* non-JSON error body — keep the generic message */
    }
    // A 401 on a data call means the session is gone; bounce to login. The
    // /api/auth/me probe never 401s (it returns 200 with authenticated:false),
    // so this can't loop on the login gate.
    if (res.status === 401) gate.onUnauthorized()
    // 409 hub_not_selected means the session has no hub lock — tear down and
    // reload so the gate re-locks (mirrors the 401 posture). The code check
    // keeps ordinary 409s (wiki stale-overwrite) untouched.
    if (res.status === 409 && code === 'hub_not_selected') gate.onHubGate()
    throw new ApiError(res.status, msg, code)
  }
  // 204/empty bodies shouldn't happen on our GET endpoints, but guard anyway.
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

// requestText is the raw-text sibling of request<T> for endpoints that answer
// text/plain (the admin log tail). Same error envelope + 401 handling.
async function requestText(path: string, init?: RequestInit): Promise<string> {
  const res = await fetch(path, { credentials: 'same-origin', ...init })
  if (!res.ok) {
    let msg = `request failed (HTTP ${res.status})`
    let code: string | undefined
    try {
      const body = (await res.json()) as { error?: string; code?: string }
      if (body?.error) msg = body.error
      if (body?.code) code = body.code
    } catch {
      /* non-JSON error body — keep the generic message */
    }
    if (res.status === 401) gate.onUnauthorized()
    if (res.status === 409 && code === 'hub_not_selected') gate.onHubGate()
    throw new ApiError(res.status, msg, code)
  }
  return res.text()
}

// requestWithEtag is the sibling of request<T> for endpoints whose response
// carries a revision in an ETag header. Used by the whiteboard document, whose
// body must stay a verbatim tldraw snapshot — wrapping it in an envelope just
// to carry a number would mean the client had to unwrap it before handing it
// to loadSnapshot, and the server had to build it.
async function requestWithEtag<T>(path: string, init?: RequestInit): Promise<{ data: T; etag: string | null }> {
  const res = await fetch(path, { credentials: 'same-origin', ...init })
  if (!res.ok) {
    let msg = `request failed (HTTP ${res.status})`
    let code: string | undefined
    try {
      const body = (await res.json()) as { error?: string; code?: string }
      if (body?.error) msg = body.error
      if (body?.code) code = body.code
    } catch {
      /* non-JSON error body — keep the generic message */
    }
    if (res.status === 401) gate.onUnauthorized()
    if (res.status === 409 && code === 'hub_not_selected') gate.onHubGate()
    throw new ApiError(res.status, msg, code)
  }
  return { data: (await res.json()) as T, etag: res.headers.get('ETag') }
}

// parseRev reads the revision out of a weak ETag (`W/"7"`). An absent or
// unreadable tag means revision 0 — the value a never-saved board carries, and
// the only safe default: the first save then either succeeds or is refused,
// never silently overwrites.
function parseRev(etag: string | null): number {
  const n = Number(etag?.replace(/^W\//, '').replace(/"/g, ''))
  return Number.isFinite(n) && n >= 0 ? n : 0
}

const qs = (params: Record<string, string | undefined>): string => {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v) sp.set(k, v)
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

export const api = {
  meta: () => request<Meta>('/api/meta'),

  authMe: () => request<AuthMe>('/api/auth/me'),

  logout: () => request<void>('/api/auth/logout', { method: 'POST' }),

  // sessionHub locks the session to one hub (validating APS membership
  // server-side) and returns the refreshed AuthMe. Re-posting a different hub
  // IS the hub switch — the caller then tears the client down (full reload).
  sessionHub: (hubId: string) =>
    request<AuthMe>('/api/session/hub', {
      method: 'POST',
      body: JSON.stringify({ hubId }),
    }),

  // resolveProject maps the Data Management ids a Fusion add-in reads from its
  // Python API to the GraphQL ids every data route is keyed by. Deliberately
  // pre-hub (works before /api/session/hub) — the embed page uses
  // sessionHubId to decide auto-lock vs hub-switch consent.
  resolveProject: (dmHubId: string, dmProjectId: string) =>
    request<ResolvedProject>(`/api/resolve/project${qs({ dmHubId, dmProjectId })}`),

  // Admin console (Settings): process status + server log tail. The full log
  // downloads via /api/admin/log?download=1 (a navigation, not a fetch).
  adminStatus: () => request<AdminStatus>('/api/admin/status'),

  adminLogTail: (tailBytes = 65536) =>
    requestText(`/api/admin/log${qs({ tailBytes: String(tailBytes) })}`),

  // Backups (Settings console): snapshot list + config, manual run, config
  // read/write, and the dirs-only filesystem browse for the folder picker.
  adminBackups: () => request<BackupList>('/api/admin/backups'),

  adminBackupRun: () =>
    request<BackupSummary>('/api/admin/backups/run', { method: 'POST' }),

  adminBackupConfig: () => request<BackupConfig>('/api/admin/backups/config'),

  adminBackupConfigSet: (cfg: BackupConfig) =>
    request<BackupConfig>('/api/admin/backups/config', {
      method: 'POST',
      body: JSON.stringify(cfg),
    }),

  // Verify re-checks one snapshot against its manifest; restore replaces the
  // live data (server-side typed confirmation, then a listener restart).
  adminBackupVerify: (path: string) =>
    request<BackupVerifyReport>('/api/admin/backups/verify', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  adminBackupRestore: (path: string, confirm: string) =>
    request<BackupRestoreResponse>('/api/admin/backups/restore', {
      method: 'POST',
      body: JSON.stringify({ path, confirm }),
    }),

  adminFsDirs: (path?: string) => request<FsDirs>(`/api/admin/fs/dirs${qs({ path })}`),

  // Data tool: disk usage across the local stores, per-project app-data
  // deletion (the UI carries the typed confirmation), and allow-listed
  // stale-artifact cleanup.
  adminDisk: () => request<DiskUsage>('/api/admin/disk'),

  adminProjectDataDelete: (projectId: string, apps: string[]) =>
    request<ProjectDataDeleteResult>(
      `/api/admin/projects/data${qs({ projectId, apps: apps.join(',') })}`,
      { method: 'DELETE' },
    ),

  adminCleanup: () => request<CleanupResult>('/api/admin/cleanup', { method: 'POST' }),

  setPort: (port: number) =>
    request<SetPortResponse>('/api/settings/port', {
      method: 'POST',
      body: JSON.stringify({ port }),
    }),

  // logoUrl is a same-origin <img> src for the sign-in logo. The version is the
  // server's content hash: a new logo is a new URL, which is what lets the
  // response be cached immutably and still never go stale. Callers pass the
  // version from meta.logo — without it the server falls back to revalidating.
  logoUrl: (version: string) => `/api/branding/logo${qs({ v: version })}`,

  uploadLogo: (file: File) => {
    const fd = new FormData()
    fd.set('file', file)
    return request<Logo>('/api/branding/logo', { method: 'POST', body: fd })
  },

  deleteLogo: () => request<void>('/api/branding/logo', { method: 'DELETE' }),

  hubs: () => request<Item[]>('/api/hubs'),

  projects: (hubId: string) => request<Item[]>(`/api/projects${qs({ hubId })}`),

  projectContents: (projectId: string) =>
    request<Contents>(`/api/projects/contents${qs({ projectId })}`),

  folderContents: (hubId: string, folderId: string) =>
    request<Item[]>(`/api/folders/contents${qs({ hubId, folderId })}`),

  // browseContents lists one folder (or, with folderId omitted, the project
  // root) through the Data Management API for the in-place hub browser. Unlike
  // folderContents it sees everything in the folder — DM-created files and
  // folders (e.g. wiki images) are invisible to the GraphQL listing. Ids are
  // DM folder urns / item lineage urns; dmProjectId is the project's altId.
  browseContents: (hubId: string, dmProjectId: string, folderId?: string) =>
    request<Item[]>(`/api/browse/contents${qs({ hubId, dmProjectId, folderId })}`),

  itemDetails: (hubId: string, itemId: string) =>
    request<Details>(`/api/items/details${qs({ hubId, itemId })}`),

  itemLocation: (hubId: string, itemId: string) =>
    request<Location>(`/api/items/location${qs({ hubId, itemId })}`),

  // fileUrl is the same-origin URL streaming an uploaded (non-native) file's tip
  // bytes, used directly as the src for <img>/<video> and by the PDF viewer. It
  // carries the session cookie as a subresource; the server forwards Range so
  // video/PDF can seek. dmProjectId is the project's altId, itemId its lineage
  // urn. name is the file name *with extension* — the server derives the
  // Content-Type from it, which the response's X-Content-Type-Options: nosniff
  // makes mandatory for <img>/<video> to render (the stored version name can
  // lack the extension).
  fileUrl: (dmProjectId: string, itemId: string, name?: string) =>
    `/api/items/file${qs({ dmProjectId, itemId, name })}`,

  // downloadUrl is fileUrl as an attachment: same stream, but the server sends
  // Content-Disposition: attachment and skips the inline preview cap, so the
  // data card's download button saves the file instead of navigating to it.
  // Used as an <a href download>, not fetched — the browser owns the transfer.
  downloadUrl: (dmProjectId: string, itemId: string, name?: string) =>
    `/api/items/file${qs({ dmProjectId, itemId, name, download: '1' })}`,

  // fileText fetches an uploaded file's bytes as text for the source/code viewer.
  // It returns tooLarge=true — without downloading the body — when the server
  // caps the file (413) or it exceeds maxBytes, so the UI can offer a plain
  // download instead of choking the editor on a huge blob.
  fileText: async (
    dmProjectId: string,
    itemId: string,
    maxBytes = 8 << 20,
  ): Promise<{ text: string; tooLarge: boolean }> => {
    const res = await fetch(api.fileUrl(dmProjectId, itemId), { credentials: 'same-origin' })
    if (res.status === 401) {
      gate.onUnauthorized()
      throw new ApiError(401, 'not authenticated')
    }
    if (res.status === 413) return { text: '', tooLarge: true }
    if (!res.ok) throw new ApiError(res.status, `request failed (HTTP ${res.status})`)
    const len = Number(res.headers.get('Content-Length') || '0')
    if (len && len > maxBytes) return { text: '', tooLarge: true }
    return { text: await res.text(), tooLarge: false }
  },

  uses: (args: { cvId?: string; hubId?: string; drawingItemId?: string }) =>
    request<ComponentRef[]>(`/api/items/uses${qs(args)}`),

  // descendants is the recursive occurrence tree (all child documents), used by
  // the Activity tab's child roll-up.
  descendants: (cvId: string) =>
    request<ComponentRef[]>(`/api/items/descendants${qs({ cvId })}`),

  whereUsed: (cvId: string) =>
    request<ComponentRef[]>(`/api/items/where-used${qs({ cvId })}`),

  drawings: (hubId: string, designItemId: string) =>
    request<DrawingRef[]>(`/api/items/drawings${qs({ hubId, designItemId })}`),

  // localRefs is the local-store half of Where Used: our own records (tasks,
  // chat, whiteboards, jobs, batches) that reference a document. sources is
  // the checkbox selection — an unscanned source costs nothing, so the caller
  // sends only what is switched on.
  localRefs: (itemId: string, sources: LocalRefKind[]) =>
    request<LocalRefs>(`/api/items/local-refs${qs({ itemId, sources: sources.join(',') })}`),

  bom: (cvId: string) => request<BOMRow[]>(`/api/items/bom${qs({ cvId })}`),

  projectGroups: (projectId: string) =>
    request<ProjectGroup[]>(`/api/projects/groups${qs({ projectId })}`),

  groupMembers: (hubId: string, groupId: string) =>
    request<GroupMember[]>(`/api/groups/members${qs({ hubId, groupId })}`),

  classify: (cvId: string) =>
    request<Classify>(`/api/items/classify${qs({ cvId })}`),

  thumbnail: (cvId: string) =>
    request<Thumbnail>(`/api/items/thumbnail${qs({ cvId })}`),

  properties: (cvId: string) =>
    request<PhysicalProperties>(`/api/items/properties${qs({ cvId })}`),

  customProperties: (cvId: string) =>
    request<NamedProperty[]>(`/api/items/custom-properties${qs({ cvId })}`),

  // designActivity reports a single design's activity, sourced from the
  // Manufacturing Data Model GraphQL (the notifications feed rejects this app's
  // token). hubId is the GraphQL hub id and itemId the lineage urn — the same
  // pair the Details endpoints take.
  designActivity: (args: { hubId: string; itemId: string; bucket?: string }) =>
    request<ActivityReport>(
      `/api/activity/report${qs({ scope: 'design', hubId: args.hubId, id: args.itemId, bucket: args.bucket })}`,
    ),

  // rollupActivity merges a design's activity with all of its child documents'
  // activity, computed server-side (bounded concurrency, generous timeout). The
  // caller passes the descendant lineage ids it enumerated.
  rollupActivity: (args: { hubId: string; itemId: string; childItemIds: string[] }) =>
    request<ActivityReport>('/api/activity/rollup', {
      method: 'POST',
      body: JSON.stringify(args),
    }),

  // hubOverview aggregates the hub dashboard snapshot: counts and local-activity
  // buckets across the projects the caller can access. The server makes one
  // upstream GetProjects call (the accessible-project scope) then reads the
  // local stores — no per-project fan-out. No params: the hub is the session's.
  hubOverview: () => request<HubOverview>('/api/hub/overview'),

  // permissionsPath returns the access at each layer of a document's path
  // (project → folders, root→leaf): groups + individual members with roles.
  permissionsPath: (args: {
    hubId: string
    projectId: string
    projectName?: string
    folders: { id: string; name: string }[]
  }) => {
    const p = new URLSearchParams()
    p.set('hubId', args.hubId)
    p.set('projectId', args.projectId)
    if (args.projectName) p.set('projectName', args.projectName)
    for (const f of args.folders) {
      p.append('folderId', f.id)
      p.append('folderName', f.name)
    }
    return request<PermLayer[]>(`/api/permissions/path?${p.toString()}`)
  },

  // Chat (docs/chat/PLAN.md, phase 1): per-project channels + threaded
  // messages. Reads poll until the SSE stream lands in phase 2.
  chatChannels: (projectId: string) =>
    request<ChatChannelList>(`/api/chat/channels${qs({ projectId })}`),

  chatMembers: (projectId: string) =>
    request<ChatMember[]>(`/api/chat/members${qs({ projectId })}`),

  chatUpdateChannel: (
    projectId: string,
    channelId: string,
    body: { name?: string; topic?: string },
  ) =>
    request<ChatChannel>(`/api/chat/channels${qs({ projectId, channelId })}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),

  chatArchiveChannel: (projectId: string, channelId: string) =>
    request<ChatChannel>(`/api/chat/channels${qs({ projectId, channelId })}`, {
      method: 'DELETE',
    }),

  chatAddChannelMember: (projectId: string, channelId: string, userId: string) =>
    request<ChatChannel>(`/api/chat/channels/members${qs({ projectId, channelId })}`, {
      method: 'POST',
      body: JSON.stringify({ userId }),
    }),

  chatRemoveChannelMember: (projectId: string, channelId: string, userId: string) =>
    request<ChatChannel>(`/api/chat/channels/members${qs({ projectId, channelId, userId })}`, {
      method: 'DELETE',
    }),

  chatCreateChannel: (
    projectId: string,
    body: { name: string; topic?: string; isPrivate?: boolean; memberIds?: string[] },
  ) =>
    request<ChatChannel>(`/api/chat/channels${qs({ projectId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  chatMessages: (
    projectId: string,
    channelId: string,
    opts?: { beforeSeq?: number; afterSeq?: number; limit?: number },
  ) =>
    request<ChatMessageList>(
      `/api/chat/messages${qs({
        projectId,
        channelId,
        beforeSeq: opts?.beforeSeq ? String(opts.beforeSeq) : undefined,
        afterSeq: opts?.afterSeq !== undefined ? String(opts.afterSeq) : undefined,
        limit: opts?.limit ? String(opts.limit) : undefined,
      })}`,
    ),

  chatSend: (
    projectId: string,
    channelId: string,
    body: { body: string; clientMsgId: string; threadRootSeq?: number },
  ) =>
    request<ChatMessage>(`/api/chat/messages${qs({ projectId, channelId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  chatEditMessage: (projectId: string, channelId: string, seq: number, body: string) =>
    request<ChatMessage>(
      `/api/chat/messages${qs({ projectId, channelId, seq: String(seq) })}`,
      { method: 'PATCH', body: JSON.stringify({ body }) },
    ),

  chatDeleteMessage: (projectId: string, channelId: string, seq: number) =>
    request<ChatMessage>(
      `/api/chat/messages${qs({ projectId, channelId, seq: String(seq) })}`,
      { method: 'DELETE' },
    ),

  chatThread: (projectId: string, channelId: string, rootSeq: number) =>
    request<ChatMessageList>(
      `/api/chat/thread${qs({ projectId, channelId, rootSeq: String(rootSeq) })}`,
    ),

  chatReact: (projectId: string, channelId: string, seq: number, emoji: string) =>
    request<ChatMessage>(
      `/api/chat/reactions${qs({ projectId, channelId, seq: String(seq), emoji })}`,
      { method: 'POST' },
    ),

  chatUnreact: (projectId: string, channelId: string, seq: number, emoji: string) =>
    request<ChatMessage>(
      `/api/chat/reactions${qs({ projectId, channelId, seq: String(seq), emoji })}`,
      { method: 'DELETE' },
    ),

  chatUnreads: (projectId: string) =>
    request<ChatUnreadList>(`/api/chat/unreads${qs({ projectId })}`),

  chatMarkRead: (projectId: string, channelId: string, lastReadSeq: number) =>
    request<ChatUnread>(`/api/chat/read${qs({ projectId, channelId })}`, {
      method: 'PATCH',
      body: JSON.stringify({ lastReadSeq }),
    }),

  // chatTyping is a fire-and-forget ephemeral ping (204, no body).
  chatTyping: (projectId: string, channelId: string) =>
    request<void>(`/api/chat/typing${qs({ projectId, channelId })}`, { method: 'POST' }),

  // chatUploadImage stores an attachment image under the project's
  // Chat/images/ folder (an ordinary Fusion Team item) and returns its item
  // id; the composer references it with an fls:img token and wikiImageUrl
  // serves the bytes.
  chatUploadImage: (
    ids: { projectId: string; hubId: string; dmProjectId: string },
    file: File,
  ) => {
    const fd = new FormData()
    fd.set('file', file)
    return request<ChatImage>(`/api/chat/image${qs(ids)}`, { method: 'POST', body: fd })
  },

  // Tasks: per-project task lists on the local store, chat-authz roles.
  // /api/tasks/get is the single-task fetch (fls:task card hydration);
  // /api/tasks/mine is the caller's cross-project task list.
  tasks: (projectId: string) => request<TaskList>(`/api/tasks${qs({ projectId })}`),

  taskGet: (projectId: string, taskId: string) =>
    request<Task>(`/api/tasks/get${qs({ projectId, taskId })}`),

  taskCreate: (projectId: string, body: TaskDraft) =>
    request<Task>(`/api/tasks${qs({ projectId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  taskUpdate: (projectId: string, taskId: string, body: TaskPatch) =>
    request<Task>(`/api/tasks${qs({ projectId, taskId })}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),

  taskDelete: (projectId: string, taskId: string) =>
    request<{ deleted: boolean }>(`/api/tasks${qs({ projectId, taskId })}`, {
      method: 'DELETE',
    }),

  myTasks: () => request<MyTasks>('/api/tasks/mine'),

  // taskShift moves a set of scheduled tasks by whole days in one atomic
  // write (the Gantt stage-bar drag; per-task PATCHes would burst the
  // server's op limiter).
  taskShift: (projectId: string, taskIds: string[], days: number) =>
    request<TaskShiftResult>(`/api/tasks/shift${qs({ projectId })}`, {
      method: 'POST',
      body: JSON.stringify({ taskIds, days }),
    }),

  // Production: per-project job & batch tracker on the local store, chat-authz
  // roles. GET /api/production/job (singular) hydrates one job's full graph;
  // steps/edges/placeholders mutate a job in place and return the updated job.
  prodJobs: (projectId: string) => request<JobList>(`/api/production/jobs${qs({ projectId })}`),

  prodJob: (projectId: string, jobId: string) =>
    request<Job>(`/api/production/job${qs({ projectId, jobId })}`),

  // myProduction is the caller's jobs across every project on this server —
  // ones they created, or that carry a run they created.
  myProduction: () => request<MyProduction>('/api/production/mine'),

  prodJobCreate: (projectId: string, body: JobDraft) =>
    request<Job>(`/api/production/jobs${qs({ projectId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  prodJobUpdate: (projectId: string, jobId: string, body: JobPatch) =>
    request<Job>(`/api/production/jobs${qs({ projectId, jobId })}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),

  prodJobDelete: (projectId: string, jobId: string) =>
    request<{ deleted: boolean }>(`/api/production/jobs${qs({ projectId, jobId })}`, {
      method: 'DELETE',
    }),

  prodStepCreate: (projectId: string, jobId: string, body: StepDraft) =>
    request<Job>(`/api/production/steps${qs({ projectId, jobId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  prodStepUpdate: (projectId: string, jobId: string, stepId: string, body: StepPatch) =>
    request<Job>(`/api/production/steps${qs({ projectId, jobId, stepId })}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),

  prodStepDelete: (projectId: string, jobId: string, stepId: string) =>
    request<Job>(`/api/production/steps${qs({ projectId, jobId, stepId })}`, {
      method: 'DELETE',
    }),

  prodEdgeCreate: (projectId: string, jobId: string, from: string, to: string) =>
    request<Job>(`/api/production/edges${qs({ projectId, jobId })}`, {
      method: 'POST',
      body: JSON.stringify({ from, to }),
    }),

  prodEdgeDelete: (projectId: string, jobId: string, edgeId: string) =>
    request<Job>(`/api/production/edges${qs({ projectId, jobId, edgeId })}`, {
      method: 'DELETE',
    }),

  prodPlaceholderCreate: (projectId: string, jobId: string, stepId: string, body: PlaceholderDraft) =>
    request<Job>(`/api/production/placeholders${qs({ projectId, jobId, stepId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  prodPlaceholderUpdate: (
    projectId: string,
    jobId: string,
    stepId: string,
    placeholderId: string,
    body: PlaceholderPatch,
  ) =>
    request<Job>(`/api/production/placeholders${qs({ projectId, jobId, stepId, placeholderId })}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),

  prodPlaceholderDelete: (projectId: string, jobId: string, stepId: string, placeholderId: string) =>
    request<Job>(`/api/production/placeholders${qs({ projectId, jobId, stepId, placeholderId })}`, {
      method: 'DELETE',
    }),

  // Plan documents (version-pinned server-side) return the updated job.
  prodPlanDocCreate: (projectId: string, jobId: string, stepId: string, body: DocPin) =>
    request<Job>(`/api/production/plandocs${qs({ projectId, jobId, stepId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  prodPlanDocDelete: (projectId: string, jobId: string, stepId: string, planDocId: string) =>
    request<Job>(`/api/production/plandocs${qs({ projectId, jobId, stepId, planDocId })}`, {
      method: 'DELETE',
    }),

  // Batches (freeze plan-doc versions on create) and fulfillments (version-
  // pinned supplied documents) return the affected batch.
  prodBatchCreate: (projectId: string, jobId: string, body: BatchDraft) =>
    request<ProdBatch>(`/api/production/batches${qs({ projectId, jobId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  prodBatchUpdate: (projectId: string, jobId: string, batchId: string, body: BatchPatch) =>
    request<ProdBatch>(`/api/production/batches${qs({ projectId, jobId, batchId })}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),

  prodBatchDelete: (projectId: string, jobId: string, batchId: string) =>
    request<{ deleted: boolean }>(`/api/production/batches${qs({ projectId, jobId, batchId })}`, {
      method: 'DELETE',
    }),

  prodFulfillmentCreate: (projectId: string, jobId: string, batchId: string, body: FulfillInput) =>
    request<ProdBatch>(`/api/production/fulfillments${qs({ projectId, jobId, batchId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  prodFulfillmentDelete: (projectId: string, jobId: string, batchId: string, fulfillmentId: string) =>
    request<ProdBatch>(`/api/production/fulfillments${qs({ projectId, jobId, batchId, fulfillmentId })}`, {
      method: 'DELETE',
    }),

  // Batch references — related task / document tokens (not version-pinned).
  prodBatchRefAdd: (projectId: string, jobId: string, batchId: string, token: string) =>
    request<ProdBatch>(`/api/production/batchrefs${qs({ projectId, jobId, batchId })}`, {
      method: 'POST',
      body: JSON.stringify({ token }),
    }),

  prodBatchRefDelete: (projectId: string, jobId: string, batchId: string, token: string) =>
    request<ProdBatch>(`/api/production/batchrefs${qs({ projectId, jobId, batchId, token })}`, {
      method: 'DELETE',
    }),

  // Whiteboards: tldraw boards on the local store, chat-authz roles. The
  // document lives on its own endpoint so listing boards never ships shapes.
  whiteboards: (projectId: string) =>
    request<WhiteboardList>(`/api/whiteboards${qs({ projectId })}`),

  whiteboardCreate: (projectId: string, body: WhiteboardDraft) =>
    request<Whiteboard>(`/api/whiteboards${qs({ projectId })}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  whiteboardUpdate: (projectId: string, boardId: string, body: WhiteboardPatch) =>
    request<Whiteboard>(`/api/whiteboards${qs({ projectId, boardId })}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),

  whiteboardDelete: (projectId: string, boardId: string) =>
    request<{ deleted: boolean }>(`/api/whiteboards${qs({ projectId, boardId })}`, {
      method: 'DELETE',
    }),

  // The document is opaque tldraw JSON: fetched whole when a board opens,
  // replaced whole by the debounced autosave. doc null = never saved (empty
  // canvas). rev is the document's save counter, which every save must carry
  // back so a board two people have open can't be silently overwritten.
  whiteboardDoc: async (projectId: string, boardId: string) => {
    const { data, etag } = await requestWithEtag<unknown | null>(
      `/api/whiteboards/doc${qs({ projectId, boardId })}`,
    )
    return { doc: data, rev: parseRev(etag) }
  },

  // baseRev is the revision this client loaded (or last wrote). force is the
  // user's acknowledged overwrite after being shown a conflict — never a retry:
  // retrying a refused save is precisely what would discard the other person's
  // work. A stale save throws ApiError with code 'whiteboard_stale'.
  whiteboardDocSave: (
    projectId: string,
    boardId: string,
    doc: unknown,
    baseRev: number,
    force?: boolean,
  ) =>
    request<Whiteboard>(
      `/api/whiteboards/doc${qs({
        projectId,
        boardId,
        baseRev: String(baseRev),
        force: force ? '1' : undefined,
      })}`,
      { method: 'PUT', body: JSON.stringify(doc) },
    ),

  // One client's edit, as a folded tldraw RecordsDiff. The response carries the
  // board's new revision and any records the server refused (a shape someone
  // else had already deleted), which the caller drops locally.
  whiteboardPatch: (
    projectId: string,
    boardId: string,
    body: { clientId: string; seq: number; baseRev: number; put: Record<string, unknown>; remove: string[] },
  ) =>
    request<{ rev: number; rejected: string[] }>(
      `/api/whiteboards/patch${qs({ projectId, boardId })}`,
      { method: 'POST', body: JSON.stringify(body) },
    ),

  // Wiki: published markdown pages in a project's root "Wiki" folder. hubId is
  // the GraphQL hub id (the server resolves it to the DM hub id); dmProjectId is
  // the project's altId. itemId is a page's lineage urn.
  wikiPages: (hubId: string, dmProjectId: string) =>
    request<WikiPage[]>(`/api/wiki/pages${qs({ hubId, dmProjectId })}`),

  wikiPage: (dmProjectId: string, itemId: string) =>
    request<WikiPageContent>(`/api/wiki/page${qs({ dmProjectId, itemId })}`),

  // wikiPublish uploads a page's markdown to the project's Wiki folder. itemId
  // links to an already-published page (empty for a new one); baseVersion + force
  // drive stale-overwrite detection. Returns the resulting page (new tip version).
  wikiPublish: (body: {
    hubId: string
    dmProjectId: string
    itemId?: string
    slug: string
    markdown: string
    baseVersion?: string
    force?: boolean
  }) => request<WikiPage>('/api/wiki/publish', { method: 'POST', body: JSON.stringify(body) }),

  // wikiUploadImage stores an image under Wiki/<slug>/images/ and returns its
  // item id, which wikiImageUrl turns into a same-origin <img> src.
  wikiUploadImage: (fields: { hubId: string; dmProjectId: string; slug: string }, file: File) => {
    const fd = new FormData()
    fd.set('hubId', fields.hubId)
    fd.set('dmProjectId', fields.dmProjectId)
    fd.set('slug', fields.slug)
    fd.set('file', file)
    return request<WikiImageResult>('/api/wiki/image', { method: 'POST', body: fd })
  },

  // wikiRename renames a published page's file (and its images subfolder) to
  // "<newSlug>.md". oldSlug locates the images subfolder; the lineage id is
  // unchanged, so a linked draft keeps its base version.
  wikiRename: (body: {
    hubId: string
    dmProjectId: string
    itemId: string
    oldSlug: string
    newSlug: string
  }) => request<WikiPage>('/api/wiki/rename', { method: 'POST', body: JSON.stringify(body) }),

  wikiImageUrl: (dmProjectId: string, itemId: string) =>
    `/api/wiki/image${qs({ dmProjectId, itemId })}`,

  // Uploads: background file-upload jobs into a project folder. uploadFile
  // spools the bytes to the local server and resolves as soon as the job is
  // accepted; the APS-side transfer continues asynchronously and is observed by
  // polling uploads(). The target fields go into the FormData BEFORE the file —
  // the server reads them streaming and must know the target first. folderPath
  // is the folder-name trail from the project root (empty = project root);
  // projectId/folderId are the GraphQL ids echoed back for cache invalidation.
  uploads: () => request<UploadJob[]>('/api/uploads'),

  uploadFile: (
    fields: {
      hubId: string
      dmProjectId: string
      projectId?: string
      folderId?: string
      folderPath: string[]
      /** create folderPath if missing (reusing existing folders) instead of requiring it */
      ensureFolders?: boolean
    },
    file: File,
  ) => {
    const fd = new FormData()
    fd.set('hubId', fields.hubId)
    fd.set('dmProjectId', fields.dmProjectId)
    if (fields.projectId) fd.set('projectId', fields.projectId)
    if (fields.folderId) fd.set('folderId', fields.folderId)
    fd.set('folderPath', JSON.stringify(fields.folderPath))
    if (fields.ensureFolders) fd.set('ensureFolders', 'true')
    fd.set('file', file) // last: the server stops reading parts after the file
    return request<UploadJob>('/api/uploads', { method: 'POST', body: fd })
  },

  cancelUpload: (id: string) =>
    request<UploadJob[]>(`/api/uploads/cancel${qs({ id })}`, { method: 'POST' }),

  // dismissUploads clears finished jobs (one by id, or all when omitted) and
  // returns the refreshed list.
  dismissUploads: (id?: string) =>
    request<UploadJob[]>(`/api/uploads/dismiss${qs({ id })}`, { method: 'POST' }),

  // Notifications: the app-chrome bell's per-user, per-hub inbox. The server
  // is the sole author; these read, mark read, and dismiss.
  notifications: () => request<NotificationList>('/api/notifications'),

  notifMarkRead: (ids: string[]) =>
    request<NotifCount>('/api/notifications/read', {
      method: 'PATCH',
      body: JSON.stringify({ ids }),
    }),

  notifMarkAllRead: () =>
    request<NotifCount>('/api/notifications/readall', { method: 'POST' }),

  notifDismiss: (id: string) =>
    request<NotifCount>(`/api/notifications${qs({ id })}`, { method: 'DELETE' }),

  pins: (hubId: string) => request<Pin[]>(`/api/pins${qs({ hubId })}`),

  addPin: (hubId: string, pin: Partial<Pin>) =>
    request<Pin[]>(`/api/pins${qs({ hubId })}`, {
      method: 'POST',
      body: JSON.stringify(pin),
    }),

  removePin: (hubId: string, id: string) =>
    request<Pin[]>(`/api/pins${qs({ hubId, id })}`, { method: 'DELETE' }),
}
