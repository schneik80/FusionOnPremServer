package server

import (
	"net/http"

	"github.com/schneik80/fusionlocalserver/chat"
)

// routes builds the full HTTP handler: the /api/* JSON endpoints plus the
// static SPA catch-all, wrapped in the middleware chain. The Go 1.22 ServeMux
// resolves the most specific pattern, so the method-qualified /api routes win
// over the "/" catch-all, and "/api/" backstops any unmatched API path with a
// JSON 404 rather than letting it fall through to the SPA shell.
func (s *Server) routes() http.Handler {
	// The auth routes are metered below, so their buckets must exist before
	// they are wired. Run builds them with the rest; doing it here too means a
	// hand-built Server (tests) is metered rather than dereferencing nil.
	s.ensureAuthLimiters()
	// Same reasoning for the background-job registries: Run builds them, but a
	// hand-built Server (tests) would otherwise nil-panic on the first job.
	s.ensureJobRegistries()

	mux := http.NewServeMux()

	// Public: server self-description, the auth flow, and the /api 404
	// backstop. These must be reachable before a user has a session.
	//
	// The auth routes are the one place a caller acts without a session, so
	// they are metered per client IP (perIP) rather than per session like the
	// app routes. Everything behind them is already limited that way.
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/auth/login", s.perIP(s.authLim, s.handleAuthLogin))
	mux.HandleFunc("GET /api/auth/callback", s.perIP(s.authLim, s.handleAuthCallback))
	mux.HandleFunc("GET /api/auth/me", s.perIP(s.authMeLim, s.handleAuthMe))
	mux.HandleFunc("POST /api/auth/logout", s.perIP(s.authLim, s.handleAuthLogout))
	// The sign-in logo. Public because the screen that shows it renders before
	// anyone has a session — see branding.go for why this is server-wide
	// branding rather than hub data, and why nothing confidential belongs in
	// it. Writing it still requires a session (below).
	mux.HandleFunc("GET /api/branding/logo", s.handleBrandingLogo)
	// The Fusion helper app's two endpoints. Session-less by necessity — a
	// native app carries no browser cookie — and authorized instead by a
	// single-use, two-minute ticket the SPA minted while it WAS authenticated
	// (see fusiontickets.go). Metered per IP like the other pre-session routes,
	// so guessing at ticket ids costs the same as guessing at logins.
	mux.HandleFunc("GET /api/fusion/ticket", s.perIP(s.fusionLim, s.handleFusionTicket))
	mux.HandleFunc("POST /api/fusion/callback", s.perIP(s.fusionLim, s.handleFusionCallback))
	mux.HandleFunc("/api/", s.handleAPINotFound)

	// Protected: every data route requires a logged-in session. prot wraps a
	// handler with requireAuth, which resolves the session's APS token into the
	// request context (or replies 401). Only the hub list and the hub-selection
	// endpoint use bare prot — they must work before a hub is chosen.
	prot := func(h http.HandlerFunc) http.HandlerFunc {
		return s.requireAuth(h).ServeHTTP
	}
	// protHub additionally requires the session's hub lock (requireHub): 409
	// hub_not_selected until POST /api/session/hub, a central 403 on any
	// query-param hubId that differs from the session hub, and the hub's
	// storeSet resolved once into the request context. EVERY data route below
	// other than /api/hubs and /api/session/hub goes through it.
	protHub := func(h http.HandlerFunc) http.HandlerFunc {
		return s.requireAuth(s.requireHub(h)).ServeHTTP
	}

	// Session hub lock (the only data routes valid before a hub is chosen).
	mux.HandleFunc("POST /api/session/hub", prot(s.handleSessionHub))
	// Deliberately pre-hub (bare prot): the Fusion palette resolves its DM ids
	// to GraphQL ids before the session has (or to decide whether to switch)
	// a hub lock. See handleResolveProject.
	mux.HandleFunc("GET /api/resolve/project", prot(s.handleResolveProject))

	// Navigation.
	mux.HandleFunc("GET /api/hubs", prot(s.handleHubs))
	mux.HandleFunc("GET /api/projects", protHub(s.handleProjects))
	mux.HandleFunc("GET /api/projects/contents", protHub(s.handleProjectContents))
	mux.HandleFunc("GET /api/folders/contents", protHub(s.handleFolderContents))
	// DM-space folder listing for the in-place hub browser (sees content the
	// GraphQL listing misses, e.g. wiki image folders).
	mux.HandleFunc("GET /api/browse/contents", protHub(s.handleBrowseContents))
	mux.HandleFunc("GET /api/items/details", protHub(s.handleItemDetails))
	mux.HandleFunc("GET /api/items/history", protHub(s.handleItemHistory))
	mux.HandleFunc("GET /api/items/location", protHub(s.handleItemLocation))
	// Raw bytes of an uploaded (non-native) file's tip, for the preview viewers.
	mux.HandleFunc("GET /api/items/file", protHub(s.handleFile))

	// References.
	mux.HandleFunc("GET /api/items/uses", protHub(s.handleUses))
	mux.HandleFunc("GET /api/items/descendants", protHub(s.handleDescendants))
	mux.HandleFunc("GET /api/items/where-used", protHub(s.handleWhereUsed))
	mux.HandleFunc("GET /api/items/drawings", protHub(s.handleDrawings))
	mux.HandleFunc("GET /api/items/bom", protHub(s.handleBOM))
	// The local-store half of Where Used: our own records (tasks, chat,
	// whiteboards, jobs, batches) that reference a document. One GetProjects
	// call for the accessible-project scope, then local file scans.
	mux.HandleFunc("GET /api/items/local-refs", protHub(s.handleLocalRefs))

	// Permissions (project groups + roles; group members need hub-admin access).
	mux.HandleFunc("GET /api/projects/groups", protHub(s.handleProjectGroups))
	mux.HandleFunc("GET /api/permissions/path", protHub(s.handlePermissionsPath))
	mux.HandleFunc("GET /api/groups/members", protHub(s.handleGroupMembers))
	mux.HandleFunc("GET /api/items/classify", protHub(s.handleClassify))
	mux.HandleFunc("GET /api/items/thumbnail", protHub(s.handleThumbnail))
	mux.HandleFunc("GET /api/items/thumbnail/image", protHub(s.handleThumbnailImage))
	mux.HandleFunc("GET /api/items/drawing/preview", protHub(s.handleDrawingPreview))
	mux.HandleFunc("GET /api/items/properties", protHub(s.handleProperties))
	mux.HandleFunc("GET /api/items/custom-properties", protHub(s.handleCustomProperties))

	// Activity reports (per design; rollup merges in child documents).
	mux.HandleFunc("GET /api/activity/report", protHub(s.handleActivityReport))
	mux.HandleFunc("POST /api/activity/rollup", protHub(s.handleActivityRollup))

	// Hub dashboard overview: one GetProjects call (the accessible-project
	// scope) then a local aggregation of tasks/production/chat. No per-project
	// fan-out — everything after that single upstream call is a local scan.
	mux.HandleFunc("GET /api/hub/overview", protHub(s.handleHubOverview))

	// Settings.
	mux.HandleFunc("POST /api/settings/port", protHub(s.handleSetPort))
	// Sign-in branding. Server-wide, like the port: the GET above is public,
	// but changing it takes a session.
	mux.HandleFunc("POST /api/branding/logo", protHub(s.handleBrandingLogoSet))
	mux.HandleFunc("DELETE /api/branding/logo", protHub(s.handleBrandingLogoDelete))

	// Debug (only live when launched with -v; otherwise 404s). A live, real-doc
	// probe for discovering how a version exposes its root component version.
	mux.HandleFunc("GET /api/debug/version-probe", protHub(s.handleDebugVersionProbe))
	// …and one for where the schema exposes a design's non-save history
	// (HistoryChange rows) — the gate for the History tab's "other changes".
	mux.HandleFunc("GET /api/debug/history-probe", protHub(s.handleDebugHistoryProbe))

	// Chat (docs/chat/PLAN.md, phase 1). REST + client polling; the SSE
	// event stream lands in phase 2. URN-style ids ride query params, per
	// the repo-wide convention.
	mux.HandleFunc("GET /api/chat/events", protHub(s.handleChatEvents))
	mux.HandleFunc("POST /api/chat/image", protHub(s.handleChatImageUpload))
	mux.HandleFunc("GET /api/chat/channels", protHub(s.handleChatChannels))
	mux.HandleFunc("POST /api/chat/channels", protHub(s.handleChatChannelCreate))
	mux.HandleFunc("PATCH /api/chat/channels", protHub(s.handleChatChannelUpdate))
	mux.HandleFunc("DELETE /api/chat/channels", protHub(s.handleChatChannelArchive))
	mux.HandleFunc("POST /api/chat/channels/members", protHub(s.handleChatMemberAdd))
	mux.HandleFunc("DELETE /api/chat/channels/members", protHub(s.handleChatMemberRemove))
	mux.HandleFunc("GET /api/chat/messages", protHub(s.handleChatMessages))
	mux.HandleFunc("POST /api/chat/messages", protHub(s.handleChatMessageCreate))
	mux.HandleFunc("PATCH /api/chat/messages", protHub(s.handleChatMessageEdit))
	mux.HandleFunc("DELETE /api/chat/messages", protHub(s.handleChatMessageDelete))
	mux.HandleFunc("GET /api/chat/thread", protHub(s.handleChatThread))
	mux.HandleFunc("POST /api/chat/reactions", protHub(s.handleChatReactionAdd))
	mux.HandleFunc("DELETE /api/chat/reactions", protHub(s.handleChatReactionRemove))
	mux.HandleFunc("PATCH /api/chat/read", protHub(s.handleChatRead))
	mux.HandleFunc("GET /api/chat/unreads", protHub(s.handleChatUnreads))
	mux.HandleFunc("POST /api/chat/typing", protHub(s.handleChatTyping))
	mux.HandleFunc("GET /api/chat/members", protHub(s.handleChatMembers))

	// Tasks (user-based project tasks; local store, chat-authz roles).
	// /api/tasks/get is separate because GET /api/tasks is the project
	// list (wiki/pages vs wiki/page precedent); /mine is the caller's
	// cross-project task list.
	mux.HandleFunc("GET /api/tasks", protHub(s.handleTasksList))
	mux.HandleFunc("POST /api/tasks", protHub(s.handleTaskCreate))
	mux.HandleFunc("PATCH /api/tasks", protHub(s.handleTaskUpdate))
	mux.HandleFunc("DELETE /api/tasks", protHub(s.handleTaskDelete))
	mux.HandleFunc("GET /api/tasks/get", protHub(s.handleTaskGet))
	mux.HandleFunc("GET /api/tasks/mine", protHub(s.handleTasksMine))
	// /shift moves a set of scheduled tasks by N days in one atomic write
	// (the Gantt stage-bar drag; per-task PATCHes would burst the limiter).
	mux.HandleFunc("POST /api/tasks/shift", protHub(s.handleTasksShift))

	// Admin console (Settings dialog tools). Standard authenticated-session
	// gating; destructive tools confirm in the UI.
	mux.HandleFunc("GET /api/admin/status", protHub(s.handleAdminStatus))
	mux.HandleFunc("GET /api/admin/log", protHub(s.handleAdminLog))
	// Backups (list, manual run, config) + the dirs-only filesystem browse
	// backing the backup-folder picker.
	mux.HandleFunc("GET /api/admin/backups", protHub(s.handleAdminBackups))
	mux.HandleFunc("POST /api/admin/backups/run", protHub(s.handleAdminBackupRun))
	mux.HandleFunc("GET /api/admin/backups/config", protHub(s.handleAdminBackupConfigGet))
	mux.HandleFunc("POST /api/admin/backups/config", protHub(s.handleAdminBackupConfigSet))
	// Verify re-hashes a snapshot against its manifest; restore replaces the
	// live data (typed confirmation, pre-restore safety snapshot, restart).
	mux.HandleFunc("POST /api/admin/backups/verify", protHub(s.handleAdminBackupVerify))
	mux.HandleFunc("POST /api/admin/backups/restore", protHub(s.handleAdminBackupRestore))
	mux.HandleFunc("GET /api/admin/fs/dirs", protHub(s.handleAdminFsDirs))
	// Data tool: disk usage across the local stores, per-project app-data
	// deletion (typed confirmation in the UI), and allow-listed stale-artifact
	// cleanup.
	mux.HandleFunc("GET /api/admin/disk", protHub(s.handleAdminDisk))
	mux.HandleFunc("DELETE /api/admin/projects/data", protHub(s.handleAdminProjectDataDelete))
	mux.HandleFunc("POST /api/admin/cleanup", protHub(s.handleAdminCleanup))

	// Production (light MES job & batch tracker; local store, chat-authz
	// roles). GET /api/production/job (singular) is one job's full graph;
	// GET /api/production/jobs is the project list. Steps, edges, and
	// placeholders mutate a job in place. IDs ride in query params (URNs
	// contain ':'/'/').
	mux.HandleFunc("GET /api/production/jobs", protHub(s.handleProdJobsList))
	mux.HandleFunc("POST /api/production/jobs", protHub(s.handleProdJobCreate))
	mux.HandleFunc("POST /api/production/jobs/duplicate", protHub(s.handleProdJobDuplicate))
	mux.HandleFunc("PATCH /api/production/jobs", protHub(s.handleProdJobUpdate))
	mux.HandleFunc("DELETE /api/production/jobs", protHub(s.handleProdJobDelete))
	mux.HandleFunc("GET /api/production/job", protHub(s.handleProdJobGet))
	mux.HandleFunc("GET /api/production/mine", protHub(s.handleProdMine))
	mux.HandleFunc("POST /api/production/steps", protHub(s.handleProdStepCreate))
	mux.HandleFunc("PATCH /api/production/steps", protHub(s.handleProdStepUpdate))
	mux.HandleFunc("DELETE /api/production/steps", protHub(s.handleProdStepDelete))
	mux.HandleFunc("POST /api/production/edges", protHub(s.handleProdEdgeCreate))
	mux.HandleFunc("DELETE /api/production/edges", protHub(s.handleProdEdgeDelete))
	// Decision results are graph structure (each is an out-port edges branch
	// from), so they return the whole job like the other graph edits.
	mux.HandleFunc("POST /api/production/results", protHub(s.handleProdResultCreate))
	mux.HandleFunc("PATCH /api/production/results", protHub(s.handleProdResultUpdate))
	mux.HandleFunc("DELETE /api/production/results", protHub(s.handleProdResultDelete))
	mux.HandleFunc("POST /api/production/placeholders", protHub(s.handleProdPlaceholderCreate))
	mux.HandleFunc("PATCH /api/production/placeholders", protHub(s.handleProdPlaceholderUpdate))
	mux.HandleFunc("DELETE /api/production/placeholders", protHub(s.handleProdPlaceholderDelete))
	// Plan documents (version-pinned at attach), batches (freeze on create),
	// and fulfillments (version-pinned supplied documents).
	mux.HandleFunc("POST /api/production/plandocs", protHub(s.handleProdPlanDocCreate))
	mux.HandleFunc("DELETE /api/production/plandocs", protHub(s.handleProdPlanDocDelete))
	mux.HandleFunc("POST /api/production/batches", protHub(s.handleProdBatchCreate))
	mux.HandleFunc("GET /api/production/batch", protHub(s.handleProdBatchGet))
	mux.HandleFunc("PATCH /api/production/batches", protHub(s.handleProdBatchUpdate))
	mux.HandleFunc("DELETE /api/production/batches", protHub(s.handleProdBatchDelete))
	mux.HandleFunc("POST /api/production/fulfillments", protHub(s.handleProdFulfillmentCreate))
	mux.HandleFunc("DELETE /api/production/fulfillments", protHub(s.handleProdFulfillmentDelete))
	// Whiteboards (tldraw boards; local store, chat-authz roles). The document
	// endpoints are separate from the metadata ones so listing boards never
	// ships their shapes.
	mux.HandleFunc("GET /api/whiteboards", protHub(s.handleWhiteboardsList))
	mux.HandleFunc("POST /api/whiteboards", protHub(s.handleWhiteboardCreate))
	mux.HandleFunc("PATCH /api/whiteboards", protHub(s.handleWhiteboardUpdate))
	mux.HandleFunc("DELETE /api/whiteboards", protHub(s.handleWhiteboardDelete))
	// Board awareness stream: who has a board open, and when it changed. Its
	// own SSE endpoint rather than a chat event type — see whiteboard_hub.go.
	mux.HandleFunc("GET /api/whiteboards/events", protHub(s.handleWhiteboardEvents))
	// Live editing: one client's RecordsDiff, applied to the board's
	// authoritative record map and fanned out to everyone else on it.
	mux.HandleFunc("POST /api/whiteboards/patch", protHub(s.handleWhiteboardPatch))
	mux.HandleFunc("GET /api/whiteboards/doc", protHub(s.handleWhiteboardDocGet))
	mux.HandleFunc("PUT /api/whiteboards/doc", protHub(s.handleWhiteboardDocPut))

	mux.HandleFunc("POST /api/production/batchrefs", protHub(s.handleProdBatchRefAdd))
	mux.HandleFunc("DELETE /api/production/batchrefs", protHub(s.handleProdBatchRefDelete))
	// Which frozen steps the run view collapses — presentation metadata on the
	// batch, never a field on the frozen step snapshot.
	mux.HandleFunc("POST /api/production/batchhidden", protHub(s.handleProdBatchStepHide))
	mux.HandleFunc("DELETE /api/production/batchhidden", protHub(s.handleProdBatchStepShow))

	// Notifications (the app-chrome bell: the caller's own per-user, per-hub
	// inbox — mentions, assignments, due dates, production changes). The
	// server is the sole author; clients read, mark read, and dismiss.
	mux.HandleFunc("GET /api/notifications", protHub(s.handleNotifList))
	mux.HandleFunc("PATCH /api/notifications/read", protHub(s.handleNotifMarkRead))
	mux.HandleFunc("POST /api/notifications/readall", protHub(s.handleNotifMarkAllRead))
	mux.HandleFunc("DELETE /api/notifications", protHub(s.handleNotifDismiss))

	// Pins.
	mux.HandleFunc("GET /api/pins", protHub(s.handlePinsList))
	mux.HandleFunc("POST /api/pins", protHub(s.handlePinsAdd))
	mux.HandleFunc("DELETE /api/pins", protHub(s.handlePinsRemove))

	// Uploads (background file-upload jobs into a project folder).
	mux.HandleFunc("POST /api/uploads", protHub(s.handleUploadCreate))
	mux.HandleFunc("GET /api/uploads", protHub(s.handleUploadList))
	mux.HandleFunc("POST /api/uploads/cancel", protHub(s.handleUploadCancel))
	mux.HandleFunc("POST /api/uploads/dismiss", protHub(s.handleUploadDismiss))

	// Archives (background F3Z/F3D generation for a Fusion-native document).
	// Same job-manager shape as uploads; /file streams a finished one, and is
	// the target the bell's "archive ready" notification resolves to.
	mux.HandleFunc("POST /api/archives", protHub(s.handleArchiveCreate))
	mux.HandleFunc("GET /api/archives", protHub(s.handleArchiveList))
	mux.HandleFunc("POST /api/archives/cancel", protHub(s.handleArchiveCancel))
	mux.HandleFunc("POST /api/archives/dismiss", protHub(s.handleArchiveDismiss))
	mux.HandleFunc("GET /api/archives/file", protHub(s.handleArchiveFile))

	// Fusion desktop actions (Open / Insert). The SPA half: mint a ticket for
	// the helper app, or — when the caller is on this machine — perform the
	// action here and answer with the result. The helper's half is public, up
	// with the auth routes.
	mux.HandleFunc("POST /api/fusion/action", protHub(s.handleFusionAction))
	mux.HandleFunc("GET /api/fusion/outcome", protHub(s.handleFusionOutcome))

	// Wiki (project-scoped markdown pages in a project-root "Wiki" folder).
	mux.HandleFunc("GET /api/wiki/pages", protHub(s.handleWikiPages))
	mux.HandleFunc("GET /api/wiki/page", protHub(s.handleWikiPage))
	mux.HandleFunc("POST /api/wiki/publish", protHub(s.handleWikiPublish))
	mux.HandleFunc("POST /api/wiki/rename", protHub(s.handleWikiRename))
	mux.HandleFunc("POST /api/wiki/image", protHub(s.handleWikiImageUpload))
	mux.HandleFunc("GET /api/wiki/image", protHub(s.handleWikiImage))

	// Static SPA for everything else.
	mux.Handle("/", s.staticHandler())

	// Middleware chain (outermost first): recover -> log -> security headers ->
	// canonical-host redirect -> same-origin (CSRF) -> dev CORS. The origin
	// check sits after the canonical redirect so an off-host client is bounced
	// to the canonical origin before being judged against it.
	return s.recoverPanic(s.logRequest(s.securityHeaders(s.canonicalRedirect(s.requireSameOrigin(s.devCORS(mux))))))
}

// ensureAuthLimiters builds the pre-session auth buckets if they aren't set.
// Keyed by client IP (there is no session yet at login): starting a login or
// logging out is a human action, so 10 burst refilling one per 2s sits far
// above a real user and far below a scripted flood. /api/auth/me gets its own
// generous bucket — useAuthMe (web/src/api/queries.ts) sets staleTime 0, so
// the SPA re-probes it on every window focus.
func (s *Server) ensureAuthLimiters() {
	if s.authLim == nil {
		s.authLim = chat.NewLimiter(0.5, 10)
	}
	if s.authMeLim == nil {
		s.authMeLim = chat.NewLimiter(5, 60)
	}
	if s.fusionLim == nil {
		// The helper redeems one ticket and posts one callback per user
		// action, so two requests per click. A burst of 20 covers a flurry of
		// clicks; the 1/s refill leaves no room to enumerate ticket ids.
		s.fusionLim = chat.NewLimiter(1, 20)
	}
}

// ensureJobRegistries builds the in-memory background-job state if it isn't
// set: the archive-generation manager and the Fusion action tickets. Both are
// process-lifetime and unpersisted, so building them late costs nothing.
func (s *Server) ensureJobRegistries() {
	if s.archives == nil {
		s.archives = newArchiveManager(archiveConcurrency)
	}
	if s.fusionTickets == nil {
		s.fusionTickets = newFusionTicketStore()
	}
	if s.uploads == nil {
		s.uploads = newUploadManager(uploadConcurrency)
	}
}

// perIP meters a handler with a client-IP-keyed token bucket, replying 429
// (code rate_limited) when the bucket is dry. Used for the pre-session auth
// routes; session-keyed limiting inside the handlers covers everything else.
func (s *Server) perIP(l *chat.Limiter, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(s.clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		h(w, r)
	}
}
