// TypeScript mirrors of the Go DTOs in server/dto.go. Keep field names in sync
// with the json tags there.

export interface Meta {
  version: string
  region: string
  port: number
  portConfigurable: boolean
  debug?: boolean // server running with -v: reveal developer-only UI affordances
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
  comment?: string
  rootComponentVersionId?: string // per-version cvId for the thumbnail
  isMilestone?: boolean // marks the "release" lane + a dev→release merge edge
  revision?: string // reserved: the "main"/release lane; no API source yet
  publicShare?: boolean // marks the rust-orange "share" lane; no API source yet
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

// WikiPageContent is the markdown body of a single published page.
export interface WikiPageContent {
  itemId: string
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

// Pin mirrors pins.Pin (snake_case json tags, unlike the camelCase DTOs).
export interface Pin {
  id: string
  name: string
  kind: string
  hub_id: string
  project_id?: string
  project_alt_id?: string
  folder_path?: FolderRef[]
  pinned_at?: string
}
