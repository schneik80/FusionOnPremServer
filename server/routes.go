package server

import "net/http"

// routes builds the full HTTP handler: the /api/* JSON endpoints plus the
// static SPA catch-all, wrapped in the middleware chain. The Go 1.22 ServeMux
// resolves the most specific pattern, so the method-qualified /api routes win
// over the "/" catch-all, and "/api/" backstops any unmatched API path with a
// JSON 404 rather than letting it fall through to the SPA shell.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Public: server self-description, the auth flow, and the /api 404
	// backstop. These must be reachable before a user has a session.
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("GET /api/auth/callback", s.handleAuthCallback)
	mux.HandleFunc("GET /api/auth/me", s.handleAuthMe)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
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

	// Navigation.
	mux.HandleFunc("GET /api/hubs", prot(s.handleHubs))
	mux.HandleFunc("GET /api/projects", protHub(s.handleProjects))
	mux.HandleFunc("GET /api/projects/contents", protHub(s.handleProjectContents))
	mux.HandleFunc("GET /api/folders/contents", protHub(s.handleFolderContents))
	// DM-space folder listing for the in-place hub browser (sees content the
	// GraphQL listing misses, e.g. wiki image folders).
	mux.HandleFunc("GET /api/browse/contents", protHub(s.handleBrowseContents))
	mux.HandleFunc("GET /api/items/details", protHub(s.handleItemDetails))
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

	// Debug (only live when launched with -v; otherwise 404s). A live, real-doc
	// probe for discovering how a version exposes its root component version.
	mux.HandleFunc("GET /api/debug/version-probe", protHub(s.handleDebugVersionProbe))

	// Chat (docs/chat/PLAN.md, phase 1). REST + client polling; the SSE
	// event stream lands in phase 2. URN-style ids ride query params, per
	// the repo-wide convention.
	mux.HandleFunc("GET /api/chat/events", protHub(s.handleChatEvents))
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
	mux.HandleFunc("PATCH /api/production/jobs", protHub(s.handleProdJobUpdate))
	mux.HandleFunc("DELETE /api/production/jobs", protHub(s.handleProdJobDelete))
	mux.HandleFunc("GET /api/production/job", protHub(s.handleProdJobGet))
	mux.HandleFunc("GET /api/production/mine", protHub(s.handleProdMine))
	mux.HandleFunc("POST /api/production/steps", protHub(s.handleProdStepCreate))
	mux.HandleFunc("PATCH /api/production/steps", protHub(s.handleProdStepUpdate))
	mux.HandleFunc("DELETE /api/production/steps", protHub(s.handleProdStepDelete))
	mux.HandleFunc("POST /api/production/edges", protHub(s.handleProdEdgeCreate))
	mux.HandleFunc("DELETE /api/production/edges", protHub(s.handleProdEdgeDelete))
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
	mux.HandleFunc("GET /api/whiteboards/doc", protHub(s.handleWhiteboardDocGet))
	mux.HandleFunc("PUT /api/whiteboards/doc", protHub(s.handleWhiteboardDocPut))

	mux.HandleFunc("POST /api/production/batchrefs", protHub(s.handleProdBatchRefAdd))
	mux.HandleFunc("DELETE /api/production/batchrefs", protHub(s.handleProdBatchRefDelete))

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
	// canonical-host redirect -> dev CORS.
	return s.recoverPanic(s.logRequest(s.securityHeaders(s.canonicalRedirect(s.devCORS(mux)))))
}
