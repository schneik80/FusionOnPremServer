import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import { api, ApiError } from './client'
import type {
  ActivityReport,
  AdminStatus,
  AuthMe,
  BackupConfig,
  BackupList,
  BOMRow,
  Classify,
  ComponentRef,
  Contents,
  Details,
  DiskUsage,
  DrawingRef,
  GroupMember,
  HubOverview,
  Item,
  LocalRefKind,
  LocalRefs,
  Location,
  Meta,
  NamedProperty,
  PermLayer,
  PhysicalProperties,
  Pin,
  ProjectGroup,
  Thumbnail,
  WikiPage,
  WikiPageContent,
} from './types'
import type {
  ChatChannelList,
  ChatMember,
  ChatMessageList,
  ChatUnreadList,
} from '../chat/types'
import type { MyTasks, Task, TaskDraft, TaskList, TaskPatch } from '../tasks/types'
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
  ResultDraft,
  ResultPatch,
  StepDraft,
  StepPatch,
} from '../production/types'
import type {
  Whiteboard,
  WhiteboardDraft,
  WhiteboardList,
  WhiteboardPatch,
} from '../whiteboards/types'
import type { NotificationList } from '../notifications/types'
import {
  appendPendingMessage,
  applyUnread,
  removePendingMessage,
  upsertMessage,
} from '../chat/cache'

// Data fetched here is effectively static for a browsing session, so cache
// generously and don't refetch on window focus. enabled flags gate queries on
// the required ids being present.
const STALE = 5 * 60 * 1000

// Meta used to be immutable for a process (version, region, port) and was held
// forever. It now also carries the sign-in logo, which an operator can change
// at any time — and since this query is persisted to localStorage, staleTime:
// Infinity meant a browser that had ever loaded the app would keep showing the
// previous logo indefinitely. STALE bounds that; the client that made the
// change invalidates outright (useLogoMutations).
export const useMeta = (): UseQueryResult<Meta> =>
  useQuery({ queryKey: ['meta'], queryFn: api.meta, staleTime: STALE })

// useAuthMe drives the login gate. It is volatile (logout / session expiry),
// so unlike the browsing data it isn't cached long and doesn't retry — a clean
// "not authenticated" answer should render the login screen immediately.
export const useAuthMe = (): UseQueryResult<AuthMe> =>
  useQuery({ queryKey: ['authMe'], queryFn: api.authMe, staleTime: 0, retry: false })

// useSetSessionHub locks the session to one hub (POST /api/session/hub). The
// response is the refreshed AuthMe, dropped straight into the authMe cache so
// the hub gate re-renders locked without a refetch. Re-posting a different hub
// IS the hub switch — that caller (Settings → Connection) follows success with
// saveLastHub + teardownAndReload, so the cache write is moot there.
export const useSetSessionHub = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (hubId: string) => api.sessionHub(hubId),
    onSuccess: (me) => qc.setQueryData(['authMe'], me),
  })
}

// Admin console queries are live server state: they only run while their tool
// is actually visible (active = dialog open && tool selected) and never
// persist (the 'admin' prefix is excluded from dehydration in main.tsx).
export const useAdminStatus = (active: boolean): UseQueryResult<AdminStatus> =>
  useQuery({
    queryKey: ['adminStatus'],
    queryFn: api.adminStatus,
    enabled: active,
    staleTime: 0,
    refetchInterval: active ? 30_000 : false,
  })

export const useAdminLogTail = (active: boolean): UseQueryResult<string> =>
  useQuery({
    queryKey: ['adminLogTail'],
    queryFn: () => api.adminLogTail(),
    enabled: active,
    staleTime: 0,
  })

// Backups tool: snapshot list + config in one query (the table and the form
// share a fetch), plus the folder picker's directory listing.
export const useAdminBackups = (active: boolean): UseQueryResult<BackupList> =>
  useQuery({
    queryKey: ['adminBackups'],
    queryFn: api.adminBackups,
    enabled: active,
    staleTime: 0,
  })

export const useAdminBackupConfig = (active: boolean): UseQueryResult<BackupConfig> =>
  useQuery({
    queryKey: ['adminBackupConfig'],
    queryFn: api.adminBackupConfig,
    enabled: active,
    staleTime: 0,
  })

export const useAdminFsDirs = (path: string | undefined, active: boolean) =>
  useQuery({
    queryKey: ['adminFsDirs', path ?? ''],
    queryFn: () => api.adminFsDirs(path),
    enabled: active,
    staleTime: 0,
  })

export const useAdminBackupRun = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.adminBackupRun,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['adminBackups'] })
    },
  })
}

// Verify is a pure read (re-hashing happens server-side); nothing to
// invalidate. Restore is followed by a full page reload once the server
// restarts, so it too invalidates nothing.
export const useAdminBackupVerify = () =>
  useMutation({ mutationFn: (path: string) => api.adminBackupVerify(path) })

export const useAdminBackupRestore = () =>
  useMutation({
    mutationFn: (v: { path: string; confirm: string }) =>
      api.adminBackupRestore(v.path, v.confirm),
  })

export const useAdminBackupConfigSet = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (cfg: BackupConfig) => api.adminBackupConfigSet(cfg),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['adminBackups'] })
      void qc.invalidateQueries({ queryKey: ['adminBackupConfig'] })
    },
  })
}

// Data tool: disk usage (refetched after every destructive action below),
// per-project app-data deletion, stale-artifact cleanup.
export const useAdminDisk = (active: boolean): UseQueryResult<DiskUsage> =>
  useQuery({
    queryKey: ['adminDisk'],
    queryFn: api.adminDisk,
    enabled: active,
    staleTime: 0,
  })

export const useAdminProjectDataDelete = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { projectId: string; apps: string[] }) =>
      api.adminProjectDataDelete(v.projectId, v.apps),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['adminDisk'] })
    },
  })
}

export const useAdminCleanup = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.adminCleanup,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['adminDisk'] })
    },
  })
}

// useSetPort persists a new listen port and triggers a server restart. There's
// nothing to invalidate — the server rebinds and the caller reconnects on the
// new port.
export const useSetPort = () =>
  useMutation({ mutationFn: (port: number) => api.setPort(port) })

// useLogoMutations sets and clears the sign-in logo. Both invalidate ['meta']:
// the logo's identity travels in the meta payload (which the sign-in screen
// reads before it has a session), and useMeta holds it with staleTime:Infinity,
// so nothing else would ever refetch it.
export const useLogoMutations = () => {
  const qc = useQueryClient()
  const done = () => {
    void qc.invalidateQueries({ queryKey: ['meta'] })
  }
  return {
    upload: useMutation({ mutationFn: (file: File) => api.uploadLogo(file), onSuccess: done }),
    remove: useMutation({ mutationFn: () => api.deleteLogo(), onSuccess: done }),
  }
}

export const useHubs = (): UseQueryResult<Item[]> =>
  useQuery({ queryKey: ['hubs'], queryFn: api.hubs, staleTime: STALE })

export const useProjects = (hubId: string | null): UseQueryResult<Item[]> =>
  useQuery({
    queryKey: ['projects', hubId],
    queryFn: () => api.projects(hubId!),
    enabled: !!hubId,
    staleTime: STALE,
  })

export const useProjectContents = (
  projectId: string | null,
): UseQueryResult<Contents> =>
  useQuery({
    queryKey: ['projectContents', projectId],
    queryFn: () => api.projectContents(projectId!),
    enabled: !!projectId,
    staleTime: STALE,
  })

export const useFolderContents = (
  hubId: string | null,
  folderId: string | null,
): UseQueryResult<Item[]> =>
  useQuery({
    queryKey: ['folderContents', hubId, folderId],
    queryFn: () => api.folderContents(hubId!, folderId!),
    enabled: !!hubId && !!folderId,
    staleTime: STALE,
  })

// useBrowseContents is the hub browser's DM-space listing: one folder of a
// project, or the project root when folderId is '' — complete, unlike the
// GraphQL folderContents (which misses DM-created files/folders). Pass a null
// dmProjectId to disable (e.g. a collapsed tree node).
export const useBrowseContents = (
  hubId: string | null,
  dmProjectId: string | null | undefined,
  folderId: string,
): UseQueryResult<Item[]> =>
  useQuery({
    queryKey: ['browseContents', dmProjectId, folderId],
    queryFn: () => api.browseContents(hubId!, dmProjectId!, folderId || undefined),
    enabled: !!hubId && !!dmProjectId,
    staleTime: STALE,
  })

export const useItemDetails = (
  hubId: string | null,
  itemId: string | null,
): UseQueryResult<Details> =>
  useQuery({
    queryKey: ['details', hubId, itemId],
    queryFn: () => api.itemDetails(hubId!, itemId!),
    enabled: !!hubId && !!itemId,
    staleTime: STALE,
  })

// useClassify drives the per-row async refinement: each design row issues one
// query to upgrade its icon to assembly/part. Concurrency is bounded by the
// browser's per-host connection cap and the server's classify semaphore.
export const useClassify = (cvId: string | undefined): UseQueryResult<Classify> =>
  useQuery({
    queryKey: ['classify', cvId],
    queryFn: () => api.classify(cvId!),
    enabled: !!cvId,
    staleTime: Infinity,
  })

// useThumbnail fetches a component version's thumbnail. APS generates it
// asynchronously, so the first response may be PENDING with no URL; poll every
// 2s until the status settles on SUCCESS or FAILED.
export const useThumbnail = (
  cvId: string | undefined,
  enabled: boolean,
): UseQueryResult<Thumbnail> =>
  useQuery({
    queryKey: ['thumbnail', cvId],
    queryFn: () => api.thumbnail(cvId!),
    enabled: enabled && !!cvId,
    staleTime: Infinity,
    refetchInterval: (q) => {
      const s = q.state.data?.status
      return s === 'SUCCESS' || s === 'FAILED' ? false : 2000
    },
  })

// useProperties fetches a component version's physical (mass) properties.
// Like thumbnails, generation is async, so poll every 2s until the status is
// COMPLETED/FAILED — capped at ~15 polls so a stuck computation doesn't poll
// forever.
export const useProperties = (
  cvId: string | undefined,
  enabled: boolean,
): UseQueryResult<PhysicalProperties> =>
  useQuery({
    queryKey: ['properties', cvId],
    queryFn: () => api.properties(cvId!),
    enabled: enabled && !!cvId,
    staleTime: Infinity,
    refetchInterval: (q) => {
      const s = q.state.data?.status
      if (s === 'COMPLETED' || s === 'FAILED') return false
      if (q.state.dataUpdateCount >= 15) return false
      return 2000
    },
  })

export const useUses = (args: {
  cvId?: string
  hubId?: string
  drawingItemId?: string
  enabled: boolean
}): UseQueryResult<ComponentRef[]> =>
  useQuery({
    queryKey: ['uses', args.cvId, args.hubId, args.drawingItemId],
    queryFn: () =>
      api.uses({ cvId: args.cvId, hubId: args.hubId, drawingItemId: args.drawingItemId }),
    enabled: args.enabled,
    staleTime: STALE,
  })

// useDescendants fetches the recursive occurrence tree (all child documents).
// Heavier than useUses (one level) — backs the Activity tab's child roll-up.
export const useDescendants = (
  cvId: string | undefined,
  enabled: boolean,
): UseQueryResult<ComponentRef[]> =>
  useQuery({
    queryKey: ['descendants', cvId],
    queryFn: () => api.descendants(cvId!),
    enabled: enabled && !!cvId,
    staleTime: STALE,
  })

// usePermissionsPath fetches per-layer access (project → folders) for a document.
export const usePermissionsPath = (
  hubId: string | null,
  projectId: string | null | undefined,
  projectName: string | undefined,
  folders: { id: string; name: string }[],
  enabled: boolean,
): UseQueryResult<PermLayer[]> =>
  useQuery({
    queryKey: ['permPath', hubId, projectId, folders.map((f) => f.id)],
    queryFn: () => api.permissionsPath({ hubId: hubId!, projectId: projectId!, projectName, folders }),
    enabled: enabled && !!hubId && !!projectId,
    staleTime: STALE,
  })

export const useWhereUsed = (
  cvId: string | undefined,
  enabled: boolean,
): UseQueryResult<ComponentRef[]> =>
  useQuery({
    queryKey: ['whereUsed', cvId],
    queryFn: () => api.whereUsed(cvId!),
    enabled: enabled && !!cvId,
    staleTime: STALE,
  })

export const useDrawings = (
  hubId: string | null,
  designItemId: string | undefined,
  enabled: boolean,
): UseQueryResult<DrawingRef[]> =>
  useQuery({
    queryKey: ['drawings', hubId, designItemId],
    queryFn: () => api.drawings(hubId!, designItemId!),
    enabled: enabled && !!hubId && !!designItemId,
    staleTime: STALE,
  })

// useLocalRefs backs the Where-Used graph's local sources. The key carries the
// selected sources so unchecking one doesn't re-fetch what is already cached,
// and checking a new one fetches exactly the superset asked for.
//
// It is NOT persisted to localStorage: it reads chat, task, production and
// whiteboard data — realtime, per-user records that must not linger on a
// shared machine (see the dehydrate filter in main.tsx).
export const useLocalRefs = (
  itemId: string | undefined,
  sources: LocalRefKind[],
  enabled: boolean,
): UseQueryResult<LocalRefs> =>
  useQuery({
    queryKey: ['localRefs', itemId, [...sources].sort().join(',')],
    queryFn: () => api.localRefs(itemId!, sources),
    enabled: enabled && !!itemId && sources.length > 0,
    staleTime: STALE,
  })

export const useCustomProperties = (
  cvId: string | undefined,
  enabled: boolean,
): UseQueryResult<NamedProperty[]> =>
  useQuery({
    queryKey: ['customProperties', cvId],
    queryFn: () => api.customProperties(cvId!),
    enabled: enabled && !!cvId,
    staleTime: STALE,
  })

export const useBOM = (
  cvId: string | undefined,
  enabled: boolean,
): UseQueryResult<BOMRow[]> =>
  useQuery({
    queryKey: ['bom', cvId],
    queryFn: () => api.bom(cvId!),
    enabled: enabled && !!cvId,
    staleTime: STALE,
  })

export const useProjectGroups = (
  projectId: string | null | undefined,
): UseQueryResult<ProjectGroup[]> =>
  useQuery({
    queryKey: ['projectGroups', projectId],
    queryFn: () => api.projectGroups(projectId!),
    enabled: !!projectId,
    staleTime: STALE,
  })

// useGroupMembers loads a group's users lazily (on expand). Listing members
// needs hub-admin access, so a 403 is expected for ordinary users — don't
// retry it, and let the caller render it as "no permission".
export const useGroupMembers = (
  hubId: string | null,
  groupId: string | null,
  enabled: boolean,
): UseQueryResult<GroupMember[]> =>
  useQuery({
    queryKey: ['groupMembers', hubId, groupId],
    queryFn: () => api.groupMembers(hubId!, groupId!),
    enabled: enabled && !!hubId && !!groupId,
    staleTime: STALE,
    retry: false,
  })

export const useItemLocation = (
  hubId: string | null,
  itemId: string | undefined,
  enabled: boolean,
): UseQueryResult<Location> =>
  useQuery({
    queryKey: ['location', hubId, itemId],
    queryFn: () => api.itemLocation(hubId!, itemId!),
    enabled: enabled && !!hubId && !!itemId,
    staleTime: STALE,
  })

// useDesignActivity fetches one design's activity report (GraphQL-sourced).
// hubId is the GraphQL hub id and itemId the lineage urn — the same pair the
// Details endpoints use. Daily buckets; the heatmap re-buckets client-side.
export const useDesignActivity = (
  hubId: string | null | undefined,
  itemId: string | null | undefined,
): UseQueryResult<ActivityReport> =>
  useQuery({
    queryKey: ['designActivity', hubId, itemId],
    queryFn: () => api.designActivity({ hubId: hubId!, itemId: itemId!, bucket: 'day' }),
    enabled: !!hubId && !!itemId,
    staleTime: STALE,
  })

// useRollupActivity computes a design's activity merged with all of its child
// documents, server-side. Enabled only while rolled up; staleTime 0 so each
// enable fetches fresh (child docs may change in real time) and keyed by the
// child-id set so a result is never reused for a different document. While the
// document stays mounted, Day/Week/Month/Year flips (handled inside the heat map)
// don't refetch.
export const useRollupActivity = (
  hubId: string | null | undefined,
  itemId: string | null | undefined,
  childItemIds: string[],
  enabled: boolean,
): UseQueryResult<ActivityReport> =>
  useQuery({
    queryKey: ['rollupActivity', hubId, itemId, [...childItemIds].sort().join(',')],
    queryFn: () => api.rollupActivity({ hubId: hubId!, itemId: itemId!, childItemIds }),
    enabled: enabled && !!hubId && !!itemId,
    staleTime: 0,
  })

export const usePins = (hubId: string | null): UseQueryResult<Pin[]> =>
  useQuery({
    queryKey: ['pins', hubId],
    queryFn: () => api.pins(hubId!),
    enabled: !!hubId,
    staleTime: STALE,
  })

// useHubOverview backs the hub dashboard. One request the server answers with a
// single upstream GetProjects call plus local aggregation — durable enough to
// treat like the other browsing data (STALE), so it persists across reloads.
// The key intentionally avoids the volatile chat/task/prod prefixes so it does
// persist (it is hub-level aggregate browsing state, not per-user realtime).
export const useHubOverview = (hubId: string | null): UseQueryResult<HubOverview> =>
  useQuery({
    queryKey: ['hubOverview', hubId],
    queryFn: () => api.hubOverview(),
    enabled: !!hubId,
    staleTime: STALE,
  })

export function usePinMutations(hubId: string | null) {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: ['pins', hubId] })

  const add = useMutation({
    mutationFn: (pin: Partial<Pin>) => api.addPin(hubId!, pin),
    onSuccess: (pins) => {
      qc.setQueryData(['pins', hubId], pins)
      void invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.removePin(hubId!, id),
    onSuccess: (pins) => {
      qc.setQueryData(['pins', hubId], pins)
      void invalidate()
    },
  })
  return { add, remove }
}

// ---- chat (docs/chat/PLAN.md, phases 1 & 3) ----
// Chat is deliberately volatile: nothing here persists to localStorage (see
// the dehydrate filter in main.tsx). The SSE stream (useChatEvents, mounted
// in ProjectPanel) writes events straight into these caches; `live` demotes
// the phase-1 polling to a FALLBACK that re-arms only while the stream is
// down. `active` further gates everything to the visible Chat tab.

export const useChatChannels = (
  projectId: string | null,
  active: boolean,
  live: boolean,
): UseQueryResult<ChatChannelList> =>
  useQuery({
    queryKey: ['chatChannels', projectId],
    queryFn: () => api.chatChannels(projectId!),
    enabled: active && !!projectId,
    staleTime: 0,
    refetchInterval: active && !live ? 10_000 : false,
  })

export const useChatMessages = (
  projectId: string | null,
  channelId: string | null,
  active: boolean,
  live: boolean,
): UseQueryResult<ChatMessageList> =>
  useQuery({
    queryKey: ['chatMessages', projectId, channelId],
    queryFn: () => api.chatMessages(projectId!, channelId!),
    enabled: active && !!projectId && !!channelId,
    staleTime: 0,
    refetchInterval: active && !live ? 2000 : false,
  })

export const useChatThread = (
  projectId: string | null,
  channelId: string | null,
  rootSeq: number | null,
  active: boolean,
  live: boolean,
): UseQueryResult<ChatMessageList> =>
  useQuery({
    queryKey: ['chatThread', projectId, channelId, rootSeq],
    queryFn: () => api.chatThread(projectId!, channelId!, rootSeq!),
    enabled: active && !!projectId && !!channelId && rootSeq !== null,
    staleTime: 0,
    refetchInterval: active && !live ? 2000 : false,
  })

// useChatMutations bundles the write paths for one channel.
//
// Sends are optimistic (design doc §5): a pending copy keyed on a fresh
// clientMsgId appears immediately with a negative placeholder seq; the
// server's echo — REST response or SSE message.created, whichever lands
// first — replaces it via the clientMsgId match in upsertMessage. A failed
// send removes the pending copy (the composer surfaces the error). The
// clientMsgId also makes retries safe server-side: a replayed POST returns
// the original message instead of double-posting.
export function useChatMutations(projectId: string | null, channelId: string | null) {
  const qc = useQueryClient()
  const me = useAuthMe().data?.user

  const sendMutation = useMutation({
    mutationFn: (args: { body: string; clientMsgId: string; threadRootSeq?: number }) =>
      api.chatSend(projectId!, channelId!, {
        body: args.body,
        clientMsgId: args.clientMsgId,
        threadRootSeq: args.threadRootSeq,
      }),
    onMutate: (args) => {
      appendPendingMessage(qc, projectId!, channelId!, {
        seq: -Date.now(), // unique placeholder; replaced by the echo
        threadRoot: args.threadRootSeq,
        authorId: me?.id ?? '',
        authorName: me?.name || me?.email || 'me',
        clientMsgId: args.clientMsgId,
        body: args.body,
        createdAt: new Date().toISOString(),
        deleted: false,
        replyCount: 0,
        reactions: [],
        pending: true,
      })
    },
    onSuccess: (msg) => upsertMessage(qc, projectId!, channelId!, msg),
    onError: (_err, args) => removePendingMessage(qc, projectId!, channelId!, args.clientMsgId),
  })
  const send = (body: string, threadRootSeq?: number) =>
    sendMutation.mutateAsync({ body, clientMsgId: crypto.randomUUID(), threadRootSeq })

  const edit = useMutation({
    mutationFn: (args: { seq: number; body: string }) =>
      api.chatEditMessage(projectId!, channelId!, args.seq, args.body),
    onSuccess: (msg) => upsertMessage(qc, projectId!, channelId!, msg),
  })
  const remove = useMutation({
    mutationFn: (seq: number) => api.chatDeleteMessage(projectId!, channelId!, seq),
    onSuccess: (msg) => upsertMessage(qc, projectId!, channelId!, msg),
  })
  const react = useMutation({
    mutationFn: (args: { seq: number; emoji: string; on: boolean }) =>
      args.on
        ? api.chatReact(projectId!, channelId!, args.seq, args.emoji)
        : api.chatUnreact(projectId!, channelId!, args.seq, args.emoji),
    onSuccess: (msg) => upsertMessage(qc, projectId!, channelId!, msg),
  })
  return { send, sending: sendMutation.isPending, edit, remove, react }
}

export const useCreateChatChannel = (projectId: string | null) => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string; topic?: string; isPrivate?: boolean; memberIds?: string[] }) =>
      api.chatCreateChannel(projectId!, body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['chatChannels', projectId] }),
  })
}

// useChatMembers is the ACTIVE project roster for private-channel member
// pickers (phase 5). Gated on `enabled` so it only fetches while a picker
// is actually open.
export const useChatMembers = (
  projectId: string | null,
  enabled: boolean,
): UseQueryResult<ChatMember[]> =>
  useQuery({
    queryKey: ['chatMembers', projectId],
    queryFn: () => api.chatMembers(projectId!),
    enabled: enabled && !!projectId,
    staleTime: 60_000,
  })

// useChatChannelAdmin bundles the channel management writes (phase 5):
// rename/topic, archive, private-channel ACL changes. Everything settles by
// refetching the channel list — these are rare operations and membership
// changes what the caller may see.
export function useChatChannelAdmin(projectId: string | null) {
  const qc = useQueryClient()
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['chatChannels', projectId] })
    void qc.invalidateQueries({ queryKey: ['chatUnreads', projectId] })
  }
  const update = useMutation({
    mutationFn: (args: { channelId: string; name?: string; topic?: string }) =>
      api.chatUpdateChannel(projectId!, args.channelId, { name: args.name, topic: args.topic }),
    onSuccess: invalidate,
  })
  const archive = useMutation({
    mutationFn: (channelId: string) => api.chatArchiveChannel(projectId!, channelId),
    onSuccess: invalidate,
  })
  const addMember = useMutation({
    mutationFn: (args: { channelId: string; userId: string }) =>
      api.chatAddChannelMember(projectId!, args.channelId, args.userId),
    onSuccess: invalidate,
  })
  const removeMember = useMutation({
    mutationFn: (args: { channelId: string; userId: string }) =>
      api.chatRemoveChannelMember(projectId!, args.channelId, args.userId),
    onSuccess: invalidate,
  })
  return { update, archive, addMember, removeMember }
}

// useChatUnreads is the caller's per-channel unread summary (phase 4). It
// runs whenever the project is open — not just on the Chat tab — because it
// feeds the Chat tab badge. Live updates come from channel.activity /
// read.updated events; the interval only reconciles drift (e.g. deletions
// of unread messages) and serves as the fallback while the stream is down.
export const useChatUnreads = (
  projectId: string | null,
  live: boolean,
): UseQueryResult<ChatUnreadList> =>
  useQuery({
    queryKey: ['chatUnreads', projectId],
    queryFn: () => api.chatUnreads(projectId!),
    enabled: !!projectId,
    staleTime: 0,
    refetchInterval: live ? 60_000 : 10_000,
  })

// useMarkChatRead advances the caller's read cursor; the response (the
// fresh unread summary) lands straight in the unreads cache. Other tabs
// hear about it via the read.updated SSE echo.
export const useMarkChatRead = (projectId: string | null) => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (args: { channelId: string; lastReadSeq: number }) =>
      api.chatMarkRead(projectId!, args.channelId, args.lastReadSeq),
    onSuccess: (u) => applyUnread(qc, projectId!, u),
  })
}

// ---- tasks ----
//
// Tasks are volatile like chat: nothing here persists to localStorage (see
// the dehydrate filter in main.tsx). No SSE in v1 — mutations invalidate,
// and a modest interval keeps other users' edits from going stale while a
// task view is actually visible.

export const useTasks = (
  projectId: string | null,
  active: boolean,
): UseQueryResult<TaskList> =>
  useQuery({
    queryKey: ['tasks', projectId],
    queryFn: () => api.tasks(projectId!),
    enabled: active && !!projectId,
    staleTime: 10_000,
    refetchInterval: active ? 15_000 : false,
  })

// useTask hydrates one task (fls:task cards). Kept light — many cards can
// mount at once, so a longer staleTime shares fetches across them.
export const useTask = (
  projectId: string | null,
  taskId: string | null,
): UseQueryResult<Task> =>
  useQuery({
    queryKey: ['task', projectId, taskId],
    queryFn: () => api.taskGet(projectId!, taskId!),
    enabled: !!projectId && !!taskId,
    staleTime: 30_000,
    retry: (failureCount, err) =>
      // A deleted task's 404 is a designed terminal state, not a blip.
      !(err instanceof ApiError && err.status === 404) && failureCount < 2,
  })

export const useMyTasks = (active: boolean): UseQueryResult<MyTasks> =>
  useQuery({
    queryKey: ['tasksMine'],
    queryFn: () => api.myTasks(),
    enabled: active,
    staleTime: 10_000,
    refetchInterval: active ? 30_000 : false,
  })

// useTaskMutations bundles the write paths for one project's tasks. Every
// settled write refreshes the project list and the caller's cross-project
// list; the returned task also lands in its own card cache immediately.
export function useTaskMutations(projectId: string | null) {
  const qc = useQueryClient()
  const settle = (t?: Task) => {
    if (t) qc.setQueryData(['task', projectId, t.id], t)
    void qc.invalidateQueries({ queryKey: ['tasks', projectId] })
    void qc.invalidateQueries({ queryKey: ['tasksMine'] })
  }
  const create = useMutation({
    mutationFn: (body: TaskDraft) => api.taskCreate(projectId!, body),
    onSuccess: (t) => settle(t),
  })
  const update = useMutation({
    mutationFn: (args: { taskId: string; patch: TaskPatch }) =>
      api.taskUpdate(projectId!, args.taskId, args.patch),
    onSuccess: (t) => settle(t),
  })
  const remove = useMutation({
    mutationFn: (taskId: string) => api.taskDelete(projectId!, taskId),
    onSuccess: (_res, taskId) => {
      qc.removeQueries({ queryKey: ['task', projectId, taskId] })
      settle()
    },
  })
  // shift moves a set of scheduled tasks by whole days in one atomic write
  // (the Gantt stage-bar drag).
  const shift = useMutation({
    mutationFn: (args: { taskIds: string[]; days: number }) =>
      api.taskShift(projectId!, args.taskIds, args.days),
    onSuccess: (res) => {
      for (const t of res.tasks) qc.setQueryData(['task', projectId, t.id], t)
      void qc.invalidateQueries({ queryKey: ['tasks', projectId] })
      void qc.invalidateQueries({ queryKey: ['tasksMine'] })
    },
  })
  return { create, update, remove, shift }
}

// ---- production (jobs & batches) ----
//
// Like tasks: volatile (no localStorage persistence), no SSE; a modest
// interval keeps other users' edits fresh while a job view is visible. Every
// step/edge/placeholder mutation returns the whole updated job, so writes drop
// straight into the job cache.

export const useJobs = (
  projectId: string | null,
  active: boolean,
): UseQueryResult<JobList> =>
  useQuery({
    queryKey: ['prodJobs', projectId],
    queryFn: () => api.prodJobs(projectId!),
    enabled: active && !!projectId,
    staleTime: 10_000,
    refetchInterval: active ? 15_000 : false,
  })

// useMyProduction backs the cross-project Production screen. Like useMyTasks it
// is volatile (never persisted) and polls only while the screen is visible.
export const useMyProduction = (active: boolean): UseQueryResult<MyProduction> =>
  useQuery({
    queryKey: ['prodMine'],
    queryFn: () => api.myProduction(),
    enabled: active,
    staleTime: 10_000,
    refetchInterval: active ? 30_000 : false,
  })

export const useJob = (
  projectId: string | null,
  jobId: string | null,
  active: boolean,
): UseQueryResult<Job> =>
  useQuery({
    queryKey: ['prodJob', projectId, jobId],
    queryFn: () => api.prodJob(projectId!, jobId!),
    enabled: active && !!projectId && !!jobId,
    staleTime: 10_000,
    refetchInterval: active ? 15_000 : false,
    retry: (failureCount, err) =>
      // A deleted job's 404 is terminal, not a blip.
      !(err instanceof ApiError && err.status === 404) && failureCount < 2,
  })

// useProductionMutations bundles the write paths for one project's jobs. The
// graph mutations (step/edge/placeholder) return the updated job, so they land
// directly in the ['prodJob', …] cache; job create/delete refresh the list.
export function useProductionMutations(projectId: string | null) {
  const qc = useQueryClient()
  const settleJob = (j?: Job) => {
    if (j) qc.setQueryData(['prodJob', projectId, j.id], j)
    void qc.invalidateQueries({ queryKey: ['prodJobs', projectId] })
  }
  const createJob = useMutation({
    mutationFn: (body: JobDraft) => api.prodJobCreate(projectId!, body),
    onSuccess: (j) => settleJob(j),
  })
  const updateJob = useMutation({
    mutationFn: (args: { jobId: string; patch: JobPatch }) =>
      api.prodJobUpdate(projectId!, args.jobId, args.patch),
    onSuccess: (j) => settleJob(j),
  })
  const duplicateJob = useMutation({
    mutationFn: (args: { jobId: string; hubId: string; projectName: string }) =>
      api.prodJobDuplicate(projectId!, args.jobId, { hubId: args.hubId, projectName: args.projectName }),
    // AWAITS the list refetch, unlike the other job mutations. The caller
    // selects the copy on success, and ProductionApp's recovery effect resets
    // the selection to jobs[0] whenever the selected id isn't in the list —
    // so selecting before the list has the new job would snap straight back.
    onSuccess: async (j) => {
      qc.setQueryData(['prodJob', projectId, j.id], j)
      await qc.invalidateQueries({ queryKey: ['prodJobs', projectId] })
    },
  })
  const removeJob = useMutation({
    mutationFn: (jobId: string) => api.prodJobDelete(projectId!, jobId),
    onSuccess: (_res, jobId) => {
      qc.removeQueries({ queryKey: ['prodJob', projectId, jobId] })
      settleJob()
    },
  })
  return { createJob, duplicateJob, updateJob, removeJob }
}

// useJobGraphMutations bundles the in-place graph edits for one job (steps,
// edges, placeholders). Each returns the whole updated job, which drops
// straight into the ['prodJob', …] cache and refreshes the list.
export function useJobGraphMutations(projectId: string | null, jobId: string | null) {
  const qc = useQueryClient()
  const settle = (j: Job) => {
    qc.setQueryData(['prodJob', projectId, j.id], j)
    void qc.invalidateQueries({ queryKey: ['prodJobs', projectId] })
  }
  const addStep = useMutation({
    mutationFn: (body: StepDraft) => api.prodStepCreate(projectId!, jobId!, body),
    onSuccess: settle,
  })
  const updateStep = useMutation({
    mutationFn: (args: { stepId: string; patch: StepPatch }) =>
      api.prodStepUpdate(projectId!, jobId!, args.stepId, args.patch),
    onSuccess: settle,
  })
  const removeStep = useMutation({
    mutationFn: (stepId: string) => api.prodStepDelete(projectId!, jobId!, stepId),
    onSuccess: settle,
  })
  const addEdge = useMutation({
    mutationFn: (args: { from: string; to: string; fromResultId?: string }) =>
      api.prodEdgeCreate(projectId!, jobId!, args.from, args.to, args.fromResultId),
    onSuccess: settle,
  })
  const removeEdge = useMutation({
    mutationFn: (edgeId: string) => api.prodEdgeDelete(projectId!, jobId!, edgeId),
    onSuccess: settle,
  })
  const addResult = useMutation({
    mutationFn: (args: { stepId: string; body: ResultDraft }) =>
      api.prodResultCreate(projectId!, jobId!, args.stepId, args.body),
    onSuccess: settle,
  })
  const updateResult = useMutation({
    mutationFn: (args: { stepId: string; resultId: string; patch: ResultPatch }) =>
      api.prodResultUpdate(projectId!, jobId!, args.stepId, args.resultId, args.patch),
    onSuccess: settle,
  })
  const removeResult = useMutation({
    mutationFn: (args: { stepId: string; resultId: string }) =>
      api.prodResultDelete(projectId!, jobId!, args.stepId, args.resultId),
    onSuccess: settle,
  })
  const addPlaceholder = useMutation({
    mutationFn: (args: { stepId: string; body: PlaceholderDraft }) =>
      api.prodPlaceholderCreate(projectId!, jobId!, args.stepId, args.body),
    onSuccess: settle,
  })
  const removePlaceholder = useMutation({
    mutationFn: (args: { stepId: string; placeholderId: string }) =>
      api.prodPlaceholderDelete(projectId!, jobId!, args.stepId, args.placeholderId),
    onSuccess: settle,
  })
  const addPlanDoc = useMutation({
    mutationFn: (args: { stepId: string; body: DocPin }) =>
      api.prodPlanDocCreate(projectId!, jobId!, args.stepId, args.body),
    onSuccess: settle,
  })
  const removePlanDoc = useMutation({
    mutationFn: (args: { stepId: string; planDocId: string }) =>
      api.prodPlanDocDelete(projectId!, jobId!, args.stepId, args.planDocId),
    onSuccess: settle,
  })
  return {
    addStep,
    updateStep,
    removeStep,
    addEdge,
    removeEdge,
    addResult,
    updateResult,
    removeResult,
    addPlaceholder,
    removePlaceholder,
    addPlanDoc,
    removePlanDoc,
  }
}

// useBatchMutations bundles a job's batch and fulfillment writes. Each returns
// the affected batch; since GET /api/production/job already hydrates full
// batches, we just invalidate the job query to refresh its batches array.
export function useBatchMutations(projectId: string | null, jobId: string | null) {
  const qc = useQueryClient()
  const settle = () => void qc.invalidateQueries({ queryKey: ['prodJob', projectId, jobId] })
  const createBatch = useMutation({
    mutationFn: (body: BatchDraft) => api.prodBatchCreate(projectId!, jobId!, body),
    onSuccess: settle,
  })
  const updateBatch = useMutation({
    mutationFn: (args: { batchId: string; patch: BatchPatch }) =>
      api.prodBatchUpdate(projectId!, jobId!, args.batchId, args.patch),
    onSuccess: settle,
  })
  const removeBatch = useMutation({
    mutationFn: (batchId: string) => api.prodBatchDelete(projectId!, jobId!, batchId),
    onSuccess: settle,
  })
  const addFulfillment = useMutation({
    mutationFn: (args: { batchId: string; body: FulfillInput }) =>
      api.prodFulfillmentCreate(projectId!, jobId!, args.batchId, args.body),
    onSuccess: settle,
  })
  const removeFulfillment = useMutation({
    mutationFn: (args: { batchId: string; fulfillmentId: string }) =>
      api.prodFulfillmentDelete(projectId!, jobId!, args.batchId, args.fulfillmentId),
    onSuccess: settle,
  })
  const setStepHidden = useMutation({
    mutationFn: (args: { batchId: string; stepId: string; hidden: boolean }) =>
      args.hidden
        ? api.prodBatchStepHide(projectId!, jobId!, args.batchId, args.stepId)
        : api.prodBatchStepShow(projectId!, jobId!, args.batchId, args.stepId),
    onSuccess: settle,
  })
  const addRef = useMutation({
    mutationFn: (args: { batchId: string; token: string }) =>
      api.prodBatchRefAdd(projectId!, jobId!, args.batchId, args.token),
    onSuccess: settle,
  })
  const removeRef = useMutation({
    mutationFn: (args: { batchId: string; token: string }) =>
      api.prodBatchRefDelete(projectId!, jobId!, args.batchId, args.token),
    onSuccess: settle,
  })
  return {
    createBatch,
    updateBatch,
    removeBatch,
    addFulfillment,
    removeFulfillment,
    setStepHidden,
    addRef,
    removeRef,
  }
}

// ---- whiteboards ----
//
// Volatile like tasks/production (never persisted — see the dehydrate filter in
// main.tsx). The list polls while the tab is visible; a board's document is NOT
// a query — the canvas owns it, loading once on open and autosaving.

export const useWhiteboards = (
  projectId: string | null,
  active: boolean,
): UseQueryResult<WhiteboardList> =>
  useQuery({
    queryKey: ['whiteboards', projectId],
    queryFn: () => api.whiteboards(projectId!),
    enabled: active && !!projectId,
    staleTime: 10_000,
    refetchInterval: active ? 15_000 : false,
  })

// useWhiteboard hydrates ONE board — what an fls:whiteboard card needs. It
// deliberately reuses the project list's query key rather than calling a
// per-board route (there isn't one, and adding one would mean a request per
// card): a channel full of cards pointing at the same project costs a single
// fetch, and a card in the project you're already viewing is warm from the
// tab's own list. No refetchInterval — polling belongs to the open tab, not to
// a link preview. A board missing from the list is `null`, which the card
// renders as its deleted state.
export const useWhiteboard = (
  projectId: string | null,
  boardId: string,
  enabled = true,
): UseQueryResult<{ board: Whiteboard | null; canWrite: boolean }> =>
  useQuery({
    queryKey: ['whiteboards', projectId],
    queryFn: () => api.whiteboards(projectId!),
    enabled: enabled && !!projectId,
    staleTime: 30_000,
    select: (l: WhiteboardList) => ({
      board: l.whiteboards.find((b) => b.id === boardId) ?? null,
      canWrite: l.capabilities.write,
    }),
  })

export function useWhiteboardMutations(projectId: string | null) {
  const qc = useQueryClient()
  const settle = () => void qc.invalidateQueries({ queryKey: ['whiteboards', projectId] })
  const create = useMutation({
    mutationFn: (body: WhiteboardDraft) => api.whiteboardCreate(projectId!, body),
    onSuccess: settle,
  })
  const rename = useMutation({
    mutationFn: (args: { boardId: string; patch: WhiteboardPatch }) =>
      api.whiteboardUpdate(projectId!, args.boardId, args.patch),
    onSuccess: settle,
  })
  const remove = useMutation({
    mutationFn: (boardId: string) => api.whiteboardDelete(projectId!, boardId),
    onSuccess: settle,
  })
  return { create, rename, remove }
}

// useWikiPages lists a project's published wiki pages. Gated on the hub id and
// the project's data-management id (altId) both being present; the Wiki tab only
// mounts at the project level, but a project may have no Wiki folder yet (→ []).
export const useWikiPages = (
  hubId: string | null,
  dmProjectId: string | null | undefined,
  enabled = true,
): UseQueryResult<WikiPage[]> =>
  useQuery({
    queryKey: ['wikiPages', hubId, dmProjectId],
    queryFn: () => api.wikiPages(hubId!, dmProjectId!),
    enabled: enabled && !!hubId && !!dmProjectId,
    // Shorter stale window + refetch on focus so returning to the app surfaces
    // pages another device published; explicit refetches (tab open, page select)
    // cover the rest. There's no push channel (a localhost BFF can't take APS
    // webhooks), so freshness is on-demand.
    staleTime: 30 * 1000,
    refetchOnWindowFocus: true,
  })

// useWikiPage fetches one published page's markdown body. Used when opening a
// published page for reading or pulling it down as a draft.
export const useWikiPage = (
  dmProjectId: string | null | undefined,
  itemId: string | null,
  enabled = true,
): UseQueryResult<WikiPageContent> =>
  useQuery({
    queryKey: ['wikiPage', dmProjectId, itemId],
    queryFn: () => api.wikiPage(dmProjectId!, itemId!),
    enabled: enabled && !!dmProjectId && !!itemId,
    staleTime: STALE,
  })

// useWikiPublish uploads the working copy of a page to the project's Wiki folder
// and refreshes the published-pages list on success. A 409 ApiError means the
// page changed upstream — the caller can retry with force to overwrite.
export function useWikiPublish(hubId: string | null, dmProjectId: string | null | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: {
      itemId?: string
      slug: string
      markdown: string
      baseVersion?: string
      force?: boolean
    }) => api.wikiPublish({ hubId: hubId!, dmProjectId: dmProjectId!, ...body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['wikiPages', hubId, dmProjectId] })
    },
  })
}

// useWikiRename renames a published page's file (and images subfolder) and
// refreshes the published-pages list.
export function useWikiRename(hubId: string | null, dmProjectId: string | null | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { itemId: string; oldSlug: string; newSlug: string }) =>
      api.wikiRename({ hubId: hubId!, dmProjectId: dmProjectId!, ...body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['wikiPages', hubId, dmProjectId] })
    },
  })
}

// ---- notifications (app-chrome bell) ----

// useNotifications polls the caller's per-user inbox. Keyed 'notifs' so it is
// excluded from localStorage persistence (per-user realtime; see the
// dehydrate filter in main.tsx). The poll is gated on `enabled` — only while
// the app chrome is mounted and the tab is active — and refreshes on a slow
// cadence since the inbox is not latency-critical.
export const useNotifications = (enabled: boolean): UseQueryResult<NotificationList> =>
  useQuery({
    queryKey: ['notifs'],
    queryFn: () => api.notifications(),
    enabled,
    // The interval is the FLOOR on how stale the bell can get, not how it
    // normally learns things: writes this app makes itself invalidate ['notifs']
    // as soon as they land (see FusionActionsProvider), and opening the bell
    // refetches. 45s is the backstop for events that happen elsewhere — someone
    // else's @mention — where nothing local can announce them.
    staleTime: 15_000,
    refetchInterval: enabled ? 45_000 : false,
    // Opted in against the global default (main.tsx turns this off for the
    // browsing queries, which are APS-quota'd). The inbox is one small local
    // file read, and returning to the tab is exactly when a stale bell is most
    // obvious.
    refetchOnWindowFocus: enabled,
  })

// useNotificationActions bundles the bell's writes: mark-read (a set of ids),
// mark-all-read, and dismiss. Each patches the ['notifs'] cache from the
// server's fresh unread count so the badge updates without a refetch.
export function useNotificationActions() {
  const qc = useQueryClient()
  const patch = (fn: (list: NotificationList) => NotificationList) => {
    qc.setQueryData<NotificationList>(['notifs'], (prev) => (prev ? fn(prev) : prev))
  }
  const markRead = useMutation({
    mutationFn: (ids: string[]) => api.notifMarkRead(ids),
    onSuccess: (res, ids) => {
      const set = new Set(ids)
      patch((list) => ({
        unread: res.unread,
        notifications: list.notifications.map((n) => (set.has(n.id) ? { ...n, read: true } : n)),
      }))
    },
  })
  const markAllRead = useMutation({
    mutationFn: () => api.notifMarkAllRead(),
    onSuccess: (res) => {
      patch((list) => ({
        unread: res.unread,
        notifications: list.notifications.map((n) => ({ ...n, read: true })),
      }))
    },
  })
  const dismiss = useMutation({
    mutationFn: (id: string) => api.notifDismiss(id),
    onSuccess: (res, id) => {
      patch((list) => ({
        unread: res.unread,
        notifications: list.notifications.filter((n) => n.id !== id),
      }))
    },
  })
  return { markRead, markAllRead, dismiss }
}
