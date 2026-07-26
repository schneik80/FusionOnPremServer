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
	// request context (or replies 401).
	prot := func(h http.HandlerFunc) http.HandlerFunc {
		return s.requireAuth(h).ServeHTTP
	}

	// Navigation.
	mux.HandleFunc("GET /api/hubs", prot(s.handleHubs))
	mux.HandleFunc("GET /api/projects", prot(s.handleProjects))
	mux.HandleFunc("GET /api/projects/contents", prot(s.handleProjectContents))
	mux.HandleFunc("GET /api/folders/contents", prot(s.handleFolderContents))
	// DM-space folder listing for the in-place hub browser (sees content the
	// GraphQL listing misses, e.g. wiki image folders).
	mux.HandleFunc("GET /api/browse/contents", prot(s.handleBrowseContents))
	mux.HandleFunc("GET /api/items/details", prot(s.handleItemDetails))
	mux.HandleFunc("GET /api/items/location", prot(s.handleItemLocation))
	// Raw bytes of an uploaded (non-native) file's tip, for the preview viewers.
	mux.HandleFunc("GET /api/items/file", prot(s.handleFile))

	// References.
	mux.HandleFunc("GET /api/items/uses", prot(s.handleUses))
	mux.HandleFunc("GET /api/items/descendants", prot(s.handleDescendants))
	mux.HandleFunc("GET /api/items/where-used", prot(s.handleWhereUsed))
	mux.HandleFunc("GET /api/items/drawings", prot(s.handleDrawings))
	mux.HandleFunc("GET /api/items/bom", prot(s.handleBOM))

	// Permissions (project groups + roles; group members need hub-admin access).
	mux.HandleFunc("GET /api/projects/groups", prot(s.handleProjectGroups))
	mux.HandleFunc("GET /api/permissions/path", prot(s.handlePermissionsPath))
	mux.HandleFunc("GET /api/groups/members", prot(s.handleGroupMembers))
	mux.HandleFunc("GET /api/items/classify", prot(s.handleClassify))
	mux.HandleFunc("GET /api/items/thumbnail", prot(s.handleThumbnail))
	mux.HandleFunc("GET /api/items/thumbnail/image", prot(s.handleThumbnailImage))
	mux.HandleFunc("GET /api/items/drawing/preview", prot(s.handleDrawingPreview))
	mux.HandleFunc("GET /api/items/properties", prot(s.handleProperties))
	mux.HandleFunc("GET /api/items/custom-properties", prot(s.handleCustomProperties))

	// Activity reports (per design; rollup merges in child documents).
	mux.HandleFunc("GET /api/activity/report", prot(s.handleActivityReport))
	mux.HandleFunc("POST /api/activity/rollup", prot(s.handleActivityRollup))

	// Settings.
	mux.HandleFunc("POST /api/settings/port", prot(s.handleSetPort))

	// Debug (only live when launched with -v; otherwise 404s). A live, real-doc
	// probe for discovering how a version exposes its root component version.
	mux.HandleFunc("GET /api/debug/version-probe", prot(s.handleDebugVersionProbe))

	// Chat (docs/chat/PLAN.md, phase 1). REST + client polling; the SSE
	// event stream lands in phase 2. URN-style ids ride query params, per
	// the repo-wide convention.
	mux.HandleFunc("GET /api/chat/events", prot(s.handleChatEvents))
	mux.HandleFunc("GET /api/chat/channels", prot(s.handleChatChannels))
	mux.HandleFunc("POST /api/chat/channels", prot(s.handleChatChannelCreate))
	mux.HandleFunc("PATCH /api/chat/channels", prot(s.handleChatChannelUpdate))
	mux.HandleFunc("DELETE /api/chat/channels", prot(s.handleChatChannelArchive))
	mux.HandleFunc("POST /api/chat/channels/members", prot(s.handleChatMemberAdd))
	mux.HandleFunc("DELETE /api/chat/channels/members", prot(s.handleChatMemberRemove))
	mux.HandleFunc("GET /api/chat/messages", prot(s.handleChatMessages))
	mux.HandleFunc("POST /api/chat/messages", prot(s.handleChatMessageCreate))
	mux.HandleFunc("PATCH /api/chat/messages", prot(s.handleChatMessageEdit))
	mux.HandleFunc("DELETE /api/chat/messages", prot(s.handleChatMessageDelete))
	mux.HandleFunc("GET /api/chat/thread", prot(s.handleChatThread))
	mux.HandleFunc("POST /api/chat/reactions", prot(s.handleChatReactionAdd))
	mux.HandleFunc("DELETE /api/chat/reactions", prot(s.handleChatReactionRemove))
	mux.HandleFunc("PATCH /api/chat/read", prot(s.handleChatRead))
	mux.HandleFunc("GET /api/chat/unreads", prot(s.handleChatUnreads))
	mux.HandleFunc("POST /api/chat/typing", prot(s.handleChatTyping))
	mux.HandleFunc("GET /api/chat/members", prot(s.handleChatMembers))

	// Tasks (user-based project tasks; local store, chat-authz roles).
	// /api/tasks/get is separate because GET /api/tasks is the project
	// list (wiki/pages vs wiki/page precedent); /mine is the caller's
	// cross-project task list.
	mux.HandleFunc("GET /api/tasks", prot(s.handleTasksList))
	mux.HandleFunc("POST /api/tasks", prot(s.handleTaskCreate))
	mux.HandleFunc("PATCH /api/tasks", prot(s.handleTaskUpdate))
	mux.HandleFunc("DELETE /api/tasks", prot(s.handleTaskDelete))
	mux.HandleFunc("GET /api/tasks/get", prot(s.handleTaskGet))
	mux.HandleFunc("GET /api/tasks/mine", prot(s.handleTasksMine))
	// /shift moves a set of scheduled tasks by N days in one atomic write
	// (the Gantt stage-bar drag; per-task PATCHes would burst the limiter).
	mux.HandleFunc("POST /api/tasks/shift", prot(s.handleTasksShift))

	// Admin console (Settings dialog tools). Standard authenticated-session
	// gating; destructive tools confirm in the UI.
	mux.HandleFunc("GET /api/admin/status", prot(s.handleAdminStatus))
	mux.HandleFunc("GET /api/admin/log", prot(s.handleAdminLog))
	// Backups (list, manual run, config) + the dirs-only filesystem browse
	// backing the backup-folder picker.
	mux.HandleFunc("GET /api/admin/backups", prot(s.handleAdminBackups))
	mux.HandleFunc("POST /api/admin/backups/run", prot(s.handleAdminBackupRun))
	mux.HandleFunc("GET /api/admin/backups/config", prot(s.handleAdminBackupConfigGet))
	mux.HandleFunc("POST /api/admin/backups/config", prot(s.handleAdminBackupConfigSet))
	// Verify re-hashes a snapshot against its manifest; restore replaces the
	// live data (typed confirmation, pre-restore safety snapshot, restart).
	mux.HandleFunc("POST /api/admin/backups/verify", prot(s.handleAdminBackupVerify))
	mux.HandleFunc("POST /api/admin/backups/restore", prot(s.handleAdminBackupRestore))
	mux.HandleFunc("GET /api/admin/fs/dirs", prot(s.handleAdminFsDirs))

	// Production (light MES job & batch tracker; local store, chat-authz
	// roles). GET /api/production/job (singular) is one job's full graph;
	// GET /api/production/jobs is the project list. Steps, edges, and
	// placeholders mutate a job in place. IDs ride in query params (URNs
	// contain ':'/'/').
	mux.HandleFunc("GET /api/production/jobs", prot(s.handleProdJobsList))
	mux.HandleFunc("POST /api/production/jobs", prot(s.handleProdJobCreate))
	mux.HandleFunc("PATCH /api/production/jobs", prot(s.handleProdJobUpdate))
	mux.HandleFunc("DELETE /api/production/jobs", prot(s.handleProdJobDelete))
	mux.HandleFunc("GET /api/production/job", prot(s.handleProdJobGet))
	mux.HandleFunc("GET /api/production/mine", prot(s.handleProdMine))
	mux.HandleFunc("POST /api/production/steps", prot(s.handleProdStepCreate))
	mux.HandleFunc("PATCH /api/production/steps", prot(s.handleProdStepUpdate))
	mux.HandleFunc("DELETE /api/production/steps", prot(s.handleProdStepDelete))
	mux.HandleFunc("POST /api/production/edges", prot(s.handleProdEdgeCreate))
	mux.HandleFunc("DELETE /api/production/edges", prot(s.handleProdEdgeDelete))
	mux.HandleFunc("POST /api/production/placeholders", prot(s.handleProdPlaceholderCreate))
	mux.HandleFunc("PATCH /api/production/placeholders", prot(s.handleProdPlaceholderUpdate))
	mux.HandleFunc("DELETE /api/production/placeholders", prot(s.handleProdPlaceholderDelete))
	// Plan documents (version-pinned at attach), batches (freeze on create),
	// and fulfillments (version-pinned supplied documents).
	mux.HandleFunc("POST /api/production/plandocs", prot(s.handleProdPlanDocCreate))
	mux.HandleFunc("DELETE /api/production/plandocs", prot(s.handleProdPlanDocDelete))
	mux.HandleFunc("POST /api/production/batches", prot(s.handleProdBatchCreate))
	mux.HandleFunc("GET /api/production/batch", prot(s.handleProdBatchGet))
	mux.HandleFunc("PATCH /api/production/batches", prot(s.handleProdBatchUpdate))
	mux.HandleFunc("DELETE /api/production/batches", prot(s.handleProdBatchDelete))
	mux.HandleFunc("POST /api/production/fulfillments", prot(s.handleProdFulfillmentCreate))
	mux.HandleFunc("DELETE /api/production/fulfillments", prot(s.handleProdFulfillmentDelete))
	// Whiteboards (tldraw boards; local store, chat-authz roles). The document
	// endpoints are separate from the metadata ones so listing boards never
	// ships their shapes.
	mux.HandleFunc("GET /api/whiteboards", prot(s.handleWhiteboardsList))
	mux.HandleFunc("POST /api/whiteboards", prot(s.handleWhiteboardCreate))
	mux.HandleFunc("PATCH /api/whiteboards", prot(s.handleWhiteboardUpdate))
	mux.HandleFunc("DELETE /api/whiteboards", prot(s.handleWhiteboardDelete))
	mux.HandleFunc("GET /api/whiteboards/doc", prot(s.handleWhiteboardDocGet))
	mux.HandleFunc("PUT /api/whiteboards/doc", prot(s.handleWhiteboardDocPut))

	mux.HandleFunc("POST /api/production/batchrefs", prot(s.handleProdBatchRefAdd))
	mux.HandleFunc("DELETE /api/production/batchrefs", prot(s.handleProdBatchRefDelete))

	// Pins.
	mux.HandleFunc("GET /api/pins", prot(s.handlePinsList))
	mux.HandleFunc("POST /api/pins", prot(s.handlePinsAdd))
	mux.HandleFunc("DELETE /api/pins", prot(s.handlePinsRemove))

	// Uploads (background file-upload jobs into a project folder).
	mux.HandleFunc("POST /api/uploads", prot(s.handleUploadCreate))
	mux.HandleFunc("GET /api/uploads", prot(s.handleUploadList))
	mux.HandleFunc("POST /api/uploads/cancel", prot(s.handleUploadCancel))
	mux.HandleFunc("POST /api/uploads/dismiss", prot(s.handleUploadDismiss))

	// Wiki (project-scoped markdown pages in a project-root "Wiki" folder).
	mux.HandleFunc("GET /api/wiki/pages", prot(s.handleWikiPages))
	mux.HandleFunc("GET /api/wiki/page", prot(s.handleWikiPage))
	mux.HandleFunc("POST /api/wiki/publish", prot(s.handleWikiPublish))
	mux.HandleFunc("POST /api/wiki/rename", prot(s.handleWikiRename))
	mux.HandleFunc("POST /api/wiki/image", prot(s.handleWikiImageUpload))
	mux.HandleFunc("GET /api/wiki/image", prot(s.handleWikiImage))

	// Static SPA for everything else.
	mux.Handle("/", s.staticHandler())

	// Middleware chain (outermost first): recover -> log -> security headers ->
	// canonical-host redirect -> dev CORS.
	return s.recoverPanic(s.logRequest(s.securityHeaders(s.canonicalRedirect(s.devCORS(mux)))))
}
