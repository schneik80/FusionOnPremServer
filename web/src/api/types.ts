// TypeScript mirrors of the Go DTOs in server/dto.go. Keep field names in sync
// with the json tags there.

export interface Meta {
  version: string
  region: string
  port: number
  portConfigurable: boolean
  debug?: boolean // server running with -v: reveal developer-only UI affordances
  // The configured sign-in logo, absent when none is set. It rides on /api/meta
  // because that is the one endpoint the SPA can call before it has a session —
  // which is exactly when the sign-in screen needs it.
  logo?: Logo
}

// Logo mirrors server.LogoDTO: the stored sign-in logo, without its bytes.
export interface Logo {
  /** Short content hash. Changing the logo changes this, and so changes the
   *  image URL — which is what retires every cached copy. */
  version: string
  contentType: string
  size: number
  /** Intrinsic pixel size, absent when the server could not determine it (a
   *  WebP, or an SVG with neither an explicit size nor a viewBox). */
  width?: number
  height?: number
}

// AuthMe mirrors server.AuthMeDTO (GET /api/auth/me): the login-state probe.
export interface AuthUser {
  // id is the OIDC subject (stable Autodesk user id); chat uses it for
  // own-message affordances. Empty when the profile fetch failed at login.
  id: string
  name: string
  email: string
}

export interface AuthMe {
  authenticated: boolean
  user?: AuthUser
  // The hub this session is locked to (POST /api/session/hub), absent until a
  // hub is selected. Every data route 409s (code=hub_not_selected) until then.
  selectedHubId?: string
  selectedHubName?: string
}

export interface SetPortResponse {
  port: number
  restarting: boolean
}

// AdminStatus mirrors server.AdminStatusDTO (GET /api/admin/status): process
// uptime, counters and log-file facts for the Settings console.
export interface AdminStatus {
  startedAt: string
  uptimeSeconds: number
  requestsServed: number
  version: string
  goVersion: string
  logPath: string
  logSizeBytes: number
}

// BackupConfig mirrors server.BackupConfigDTO (GET/POST /api/admin/backups/
// config): where and when the daily backup runs.
export interface BackupConfig {
  backupDir: string
  backupTime: string // "HH:MM", 24-hour
  backupEnabled: boolean
}

// BackupSummary mirrors server.BackupSummaryDTO: one snapshot row.
export interface BackupSummary {
  path: string
  kind: string // daily | weekly | monthly | manual | pre-restore
  createdAt: string
  appVersion: string
  fileCount: number
  totalBytes: number
  warning?: string
}

// BackupList mirrors server.BackupListDTO (GET /api/admin/backups).
export interface BackupList {
  config: BackupConfig
  backups: BackupSummary[]
}

// BackupFileResult mirrors server.BackupFileResultDTO: one file's verify
// outcome. A file passed when none of the failure flags trip and detail is
// empty (detail marks structural findings like a stray file).
export interface BackupFileResult {
  path: string
  hashOK: boolean
  parseOK: boolean
  versionOK: boolean
  missing: boolean
  detail?: string
}

// BackupVerifyReport mirrors server.BackupVerifyReportDTO
// (POST /api/admin/backups/verify). warning is a report-level finding — a
// snapshot whose files are clean but that restore would refuse (it predates
// hub isolation, or belongs to another hub).
export interface BackupVerifyReport {
  path: string
  kind: string
  createdAt: string
  ok: boolean
  warning?: string
  files: BackupFileResult[]
}

// BackupRestoreResponse mirrors server.BackupRestoreResponse: the server
// acks, then restarts its listener (same reconnect contract as a port
// change).
export interface BackupRestoreResponse {
  restarting: boolean
}

// FsDirs mirrors server.FsDirsDTO (GET /api/admin/fs/dirs): one directory
// level of the backup-folder picker — directories only, never files.
export interface FsDirs {
  path: string
  parent: string
  dirs: string[]
}

// DiskProject mirrors server.DiskProjectDTO: one project's (or, for the pins
// pseudo-store, one hub file's) slice of a store's disk usage. The identity
// fields come from the self-describing envelope file; when it is unreadable
// only the dir slug remains.
export interface DiskProject {
  dir: string
  projectId?: string
  projectName?: string
  hubId?: string
  bytes: number
}

// DiskStore mirrors server.DiskStoreDTO: one store's total + breakdown.
export interface DiskStore {
  name: string // chat | tasks | production | whiteboards | pins
  bytes: number
  projects: DiskProject[]
}

// DiskUsage mirrors server.DiskUsageDTO (GET /api/admin/disk). otherBytes is
// everything under the config dir outside the stores (log, sessions, TLS,
// stale artifacts).
export interface DiskUsage {
  stores: DiskStore[]
  otherBytes: number
  totalBytes: number
}

// ProjectDataDeleteResult mirrors server.ProjectDataDeleteDTO
// (DELETE /api/admin/projects/data): per requested app, whether its data is
// now gone (true also when there was nothing to delete — idempotent).
export interface ProjectDataDeleteResult {
  deleted: Record<string, boolean>
}

// CleanupResult mirrors server.CleanupResultDTO (POST /api/admin/cleanup).
export interface CleanupResult {
  removed: string[]
  bytesFreed: number
}

export type ItemKind =
  | 'hub'
  | 'project'
  | 'folder'
  | 'design'
  | 'configured'
  | 'drawing'
  | 'schematic'
  | 'pcb'
  | 'ecad'
  | 'unknown'

// Fusion-native document kinds — everything Fusion itself authored, as opposed
// to a file someone uploaded (which is 'unknown'). Two things key off this: the
// Extension row is hidden for them (their "extension" is an internal type
// marker, redundant with the Type row), and the Open / Insert / Archive actions
// are offered only for them.
export const FUSION_NATIVE_KINDS: ReadonlySet<string> = new Set([
  'design',
  'configured',
  'drawing',
  'schematic',
  'pcb',
  'ecad',
])

// INSERTABLE_KINDS is narrower: Fusion inserts an occurrence into an open
// design, which only makes sense for a 3D document. Asking it to insert a
// drawing or a schematic would fail inside Fusion with an unhelpful message.
export const INSERTABLE_KINDS: ReadonlySet<string> = new Set(['design', 'configured'])

// kindFromTypename maps the details query's GraphQL typename onto Item.kind.
// Returns null when details haven't loaded, so the caller can fall back to
// whatever hint it holds.
//
// This is the app's source of truth for a document's kind, and every card that
// was handed a kind by something else has to consult it. The hints are not
// reliable: a token captures the kind at insert time, and the DM-backed hub
// browser (api/browse.go) recovers a kind from the file EXTENSION — which a
// Fusion design in Fusion Team does not have, so it lists as 'unknown'. A card
// that trusts that hint offers a design the uploaded-file treatment: a preview
// tab instead of history/BOM/references, and no Open, Insert or Archive.
export function kindFromTypename(typename?: string): string | null {
  switch (typename) {
    case 'DesignItem':
      return 'design'
    case 'DrawingItem':
      return 'drawing'
    case 'ConfiguredDesignItem':
      return 'configured'
    case 'BasicItem':
      return 'unknown'
    default:
      return null
  }
}

export interface Item {
  id: string
  name: string
  kind: ItemKind | string
  altId?: string
  webUrl?: string
  isContainer: boolean
  componentVersionId?: string
  subtype?: string // "assembly" | "part" | "dwg" | "template" | ""
  modifiedOn?: string // last-modified time; set for items, empty for folders
  slug?: string // hub slug (e.g. "imallc"), populated for hubs only
}

export interface Contents {
  folders: Item[]
  items: Item[]
}

export interface VersionSummary {
  number: number
  createdOn?: string
  createdBy?: string
  createdById?: string // APS user id — the History view's per-author track key
  comment?: string
  rootComponentVersionId?: string // per-version cvId for the thumbnail
  isMilestone?: boolean // marks a milestone save (v2 flag, or a v3 history marker)
  milestoneName?: string // the milestone's name, from the v3 history ("Milestone V2", "Item Update", "Rev B")
  revision?: string // the release label, from the v3 history ("1", "A", "Rev B")
  publicShare?: boolean // reserved: a public share on this version; no API source yet
}

// HistoryChange mirrors server.HistoryChangeDTO — one edit that made no new
// version (a property changed, a milestone marked, a part number set). Field
// names match VersionSummary so the History view lays both on one day row and
// one author track. `type` is the raw GraphQL typename; enums.ts labels it.
export interface HistoryChange {
  type: string
  createdOn?: string
  createdBy?: string
  createdById?: string
  comment?: string
}

// HistorySave mirrors server.HistorySaveDTO — one save as the history records
// it, newest first, with the milestone name / release label the history
// attached to it. Joined to `versions` by position (applyHistoryMarkers).
export interface HistorySave {
  createdOn?: string
  milestone?: string
  revision?: string
}

// ItemHistory is GET /api/items/history — the v3 history: the non-save
// changes behind "Show other changes", and the markers on the saves.
export interface ItemHistory {
  changes: HistoryChange[]
  saves: HistorySave[]
}

export interface Details {
  id: string
  name: string
  typename: string
  size?: string
  mimeType?: string
  extensionType?: string
  fusionWebUrl?: string
  createdOn?: string
  createdBy?: string
  modifiedOn?: string
  modifiedBy?: string
  versionNumber: number
  partNumber?: string
  partDesc?: string
  material?: string
  isMilestone: boolean
  revision?: string // formal release revision of the tip ("Released - Rev X"); reserved, no API source yet
  rootComponentVersionId?: string
  versions: VersionSummary[]
}

// Thumbnail mirrors server.ThumbnailDTO. status is the async generation state
// ("PENDING" | "SUCCESS" | "FAILED"); signedUrl is set only once SUCCESS.
export interface Thumbnail {
  status: string
  signedUrl?: string
}

// Measure / PhysicalProperties mirror the v2 physical-properties DTOs.
// status is "COMPLETED" | "FAILED" | (computing).
export interface Measure {
  display?: string
  units?: string
}

export interface PhysicalProperties {
  status: string
  area: Measure
  volume: Measure
  mass: Measure
  density: Measure
  bboxLength: Measure
  bboxWidth: Measure
  bboxHeight: Measure
}

// NamedProperty mirrors server.NamedPropertyDTO — a custom/standard property
// (name + display value) shown in the Details Properties tab.
export interface NamedProperty {
  name: string
  value: string
}

export interface ComponentRef {
  id: string
  name: string
  partNumber?: string
  partDesc?: string
  material?: string
  designItemId?: string
  designItemName?: string
  fusionWebUrl?: string
}

// BOMRow mirrors server.BOMRowDTO — one line of a design's bill of materials.
// quantity is the occurrence count (the v2 API has no explicit quantity field).
export interface BOMRow {
  componentVersionId: string
  name: string
  partNumber?: string
  partDesc?: string
  material?: string
  quantity: number
}

export interface DrawingRef {
  id: string
  name: string
  drawingItemId: string
  modifiedOn?: string
  modifiedBy?: string
  fusionWebUrl?: string
}

// LocalRefKind is a source of local (non-APS) where-used relationships: our
// own records that reference a hub document. Wiki pages are absent by design —
// they live in Fusion Team, not in a local store, so finding references in
// them would cost an APS download per page (see handlers_localrefs.go).
export type LocalRefKind = 'task' | 'chat' | 'whiteboard' | 'job' | 'batch'

export const LOCAL_REF_KINDS: LocalRefKind[] = ['task', 'chat', 'whiteboard', 'job', 'batch']

// LocalRef mirrors server.LocalRefDTO — one container of ours (a task, a chat
// channel, a whiteboard, a job's plan, a batch) that references a document,
// with count carrying how many references it holds.
export interface LocalRef {
  kind: LocalRefKind
  key: string
  name: string
  projectId: string
  projectName: string
  // token is the fls: token for the referencing record when it has one
  // (task/job/batch/whiteboard), so the graph node can open the same dialog its
  // card does. Chat channels have no token scheme.
  token?: string
  count: number
  detail?: string // free text from the user's own data — never translated
  via?: string // production: step | fulfillment | reference
  author?: string
  at?: string
  status?: string // task status or batch kind
}

export interface LocalRefs {
  refs: LocalRef[]
  truncated: boolean
  cap: number
}

// ProjectGroup mirrors server.ProjectGroupDTO — a group with access to the
// item's project, and its role.
export interface ProjectGroup {
  id: string
  name: string
  role: string
}

// GroupMember mirrors server.GroupMemberDTO — a user in a group (listable only
// with hub-admin access; otherwise the members request returns 403).
export interface GroupMember {
  userId: string
  name: string
  email?: string
  status?: string
}

// PermMember mirrors server.MemberDTO — an individual user with a role + status
// on a project or folder (a contributor / folder member).
export interface PermMember {
  userId: string
  name: string
  email?: string
  role: string
  status?: string // ACTIVE | INACTIVE | PENDING
}

// PermLayer mirrors server.PermLayerDTO — one layer of a document's access path
// (the project, or a folder) with the groups and individual members granted there.
export interface PermLayer {
  type: string // "project" | "folder"
  id: string
  name?: string
  groups: ProjectGroup[]
  members: PermMember[]
}

export interface FolderRef {
  id: string
  name: string
}

export interface Location {
  hubId: string
  projectId: string
  projectAltId?: string
  projectName: string
  folderPath: FolderRef[]
}

// ResolvedProject mirrors server ResolveProjectDTO (GET /api/resolve/project):
// the GraphQL ids for a hub/project named by Data Management ids, plus the
// session's current hub lock so the embed can decide auto-lock vs consent.
export interface ResolvedProject {
  hubId: string
  hubName: string
  hubAltId: string
  projectId: string
  projectName: string
  projectAltId: string
  sessionHubId: string
}

export interface Classify {
  componentVersionId: string
  isAssembly: boolean
  subtype: string // "assembly" | "part"
}

// --- Activity reports (mirror server/dto_activity.go) ---

export type ActivityScope = 'hub' | 'project' | 'folder' | 'design'
export type ActivityBucket = 'hour' | 'day' | 'month' | 'year'

export interface ActivityActor {
  accountId?: string
  displayName: string
  email?: string
}

export interface ActivityContributor {
  accountId?: string
  displayName: string
  email?: string
  eventCount: number
  firstSeen?: string
  lastSeen?: string
}

export interface ActivityTimeBucket {
  start: string // RFC3339
  count: number
}

export interface ActivityChild {
  type: string // "project" | "folder" | "design"
  id: string
  name: string
  eventCount: number
  lastChange?: string
}

export interface ActivityEvent {
  entityType: string // "design" | "community"
  entityId: string
  entityName: string
  timestamp?: string
  action: string
  actor: ActivityActor
  versionNumber?: number
  projectId?: string
  projectName?: string
  folderUrn?: string
  lineageUrn?: string
  fileType?: string
  webUrl?: string
  views?: number
  comments?: number
  likes?: number
  detail?: string
}

export interface ActivityReport {
  scope: ActivityScope | string
  scopeId?: string
  scopeName?: string
  hubId?: string
  totalEvents: number
  designCount: number
  versionCount: number
  contributorCount: number
  createdOn?: string
  lastChange?: string
  bucket: ActivityBucket | string
  timeline: ActivityTimeBucket[]
  contributors: ActivityContributor[]
  children: ActivityChild[]
  events: ActivityEvent[]
  eventsTruncated: boolean
}

// --- Hub dashboard overview (mirror server/dto_hub.go) ---

export interface HubDayCount {
  day: string // YYYY-MM-DD (UTC)
  count: number
}

export interface HubTaskStats {
  total: number
  todo: number
  inprogress: number
  blocked: number
  done: number
  open: number
  overdue: number
}

export interface HubProdStats {
  jobs: number
  batches: number
  planned: number
  running: number
  complete: number
}

export interface HubChatStats {
  total: number
  days: HubDayCount[]
}

export interface HubContributor {
  id?: string
  name: string
  count: number
}

export interface HubProjectPulse {
  projectId: string
  projectName: string
  total: number
  days: HubDayCount[]
}

// HubOverview is GET /api/hub/overview — aggregate counts plus local-activity
// buckets across the projects the caller can access in the current hub. Every
// figure comes from the local stores (tasks/production/chat), scoped to the
// accessible projects; there is no APS design activity here.
export interface HubOverview {
  hubId: string
  hubName: string
  generatedAt: string
  windowDays: number
  projectCount: number
  tasks: HubTaskStats
  production: HubProdStats
  chat: HubChatStats
  contributors: HubContributor[]
  // Distinct people who authored local content in the hub — the full tally,
  // before `contributors` is capped server-side.
  contributorCount: number
  projects: HubProjectPulse[]
  pulse: HubDayCount[]
}

// --- Wiki (mirror server/dto_wiki.go) ---

// WikiPage is one published markdown page in a project's Wiki folder. title is
// the file name without its .md extension; tipVersion is the current version urn
// (also the base-version token a draft records for stale-publish detection).
export interface WikiPage {
  itemId: string
  name: string
  title: string
  tipVersion?: string
  modifiedOn?: string
  modifiedBy?: string
}

// WikiPageContent is the markdown body of a single published page — its tip, or
// (versionId set) one specific version from its history.
export interface WikiPageContent {
  itemId: string
  versionId?: string
  markdown: string
}

// WikiVersion is one entry of a page's history (every publish is a DM version),
// listed newest first.
export interface WikiVersion {
  versionId: string
  number: number
  createdOn?: string
  createdBy?: string
}

// WikiRestoreResult answers a restore: the page with its new tip, plus the
// restored markdown so a linked draft can adopt it without a second download.
export interface WikiRestoreResult {
  page: WikiPage
  markdown: string
}

// WikiImageResult is returned after uploading an image into a page's images
// folder — the stored item's lineage urn and file name.
export interface WikiImageResult {
  itemId: string
  name: string
}

// --- Uploads (mirror server/dto_upload.go) ---

export type UploadStatus = 'queued' | 'uploading' | 'done' | 'error' | 'canceled'

// UploadJob is one background file-upload job. bytesSent tracks the server→APS
// transfer (the browser→server spool finished before the job existed).
// hubId/projectId/folderId are the GraphQL ids echoed back from submission so
// the client can invalidate the matching contents queries when the job lands.
export interface UploadJob {
  id: string
  fileName: string
  size: number
  bytesSent: number
  status: UploadStatus | string
  error?: string
  hubId?: string
  projectId?: string
  folderId?: string
  dmProjectId?: string
  folderPath: string[]
  itemId?: string
  versionId?: string
  createdOn?: string
}

// ArchiveStatus mirrors server/archives.go. 'preparing' covers the whole APS
// side — pick a format, create the download, poll it — because there is no
// progress to report: APS tells us "queued", "processing" or "done", never a
// percentage.
export type ArchiveStatus = 'queued' | 'preparing' | 'ready' | 'error' | 'canceled'

// ArchiveJob is one background archive generation (a Fusion design turned into
// an F3Z/F3D by APS). fileType is empty until APS has said which native format
// this version can actually produce; errorCode is a stable token the errors
// catalog localizes.
export interface ArchiveJob {
  id: string
  docName: string
  fileName?: string
  fileType?: string
  status: ArchiveStatus | string
  error?: string
  errorCode?: string
  hubId?: string
  projectId?: string
  dmProjectId?: string
  itemId?: string
  /** set only when the job was pinned to one version (a production card's) */
  versionId?: string
  createdOn?: string
}

// FusionAction is what the SPA can ask the user's Fusion desktop client to do.
export type FusionAction = 'open' | 'insert'

// FusionActionResult mirrors server/handlers_fusion.go's FusionActionDTO.
//
// mode 'proxy' means the server and the browser are the same machine, so the
// server already did it and ok/errorCode are final. mode 'launch' means the
// action has to go through the helper app: navigate to url, then poll the
// outcome by ticket.
export interface FusionActionResult {
  mode: 'proxy' | 'launch'
  ticket?: string
  url?: string
  ok?: boolean
  errorCode?: string
}

// FusionOutcome is the result of a launched action. 'pending' means the helper
// hasn't reported yet — which is also what "the helper isn't installed" looks
// like, since a URL-scheme navigation gives the page no feedback either way.
export interface FusionOutcome {
  status: 'pending' | 'ok' | 'error'
  errorCode?: string
}

// Pin mirrors pins.Pin (snake_case json tags, unlike the camelCase DTOs).
// Two families share the record: APS items (project/folder/document), which
// carry project + folder ancestry, and local records (whiteboard, task, job,
// batch, channel), which instead carry the fls: token that addresses them —
// see pins.Validate for the split the server enforces.
export interface Pin {
  id: string
  name: string
  kind: string
  hub_id: string
  project_id?: string
  project_alt_id?: string
  folder_path?: FolderRef[]
  /** fls: token for a local record; also that pin's id. Absent for APS items. */
  ref?: string
  /** the local record's project name, captured at pin time */
  project_name?: string
  pinned_at?: string
}
