package server

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/schneik80/fusionlocalserver/internal/hubmigrate"
	"github.com/schneik80/fusionlocalserver/internal/hubslug"
)

// Quarantine adoption (hub isolation, phase H2). The layout migration parks
// chat project dirs it could not attribute to a hub (chat's meta.json names
// no hubId, and some projects have no tasks/production/whiteboards sibling)
// under hubs/_unassigned/chat/. The first chat request for such a project
// that PASSES the APS roster authorization proves both that the caller may
// see the project and which hub their session is locked to — so the
// quarantined directory is renamed into that session hub's profile right
// there, before the store reads it.
//
// The index of quarantined slugs is scanned once at startup; when nothing is
// quarantined the Server field stays nil and the per-request cost is a
// single nil check. The index only ever shrinks.

// chatQuarantine indexes hubs/_unassigned/chat/ project slugs awaiting
// adoption. mu also serializes the adopting rename itself, so two
// concurrent first-requests for one project cannot race the store into
// creating a fresh project dir at the destination mid-move.
type chatQuarantine struct {
	dir string // <configDir>/hubs/_unassigned/chat

	mu    sync.Mutex
	slugs map[string]struct{}
}

// loadChatQuarantine builds the startup index. Returns nil when nothing is
// quarantined — the zero-per-request-cost fast path.
func loadChatQuarantine(configDir string) *chatQuarantine {
	dir := filepath.Join(configDir, "hubs", hubslug.Unassigned, "chat")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	slugs := make(map[string]struct{})
	for _, e := range entries {
		if e.IsDir() {
			slugs[e.Name()] = struct{}{}
		}
	}
	if len(slugs) == 0 {
		return nil
	}
	return &chatQuarantine{dir: dir, slugs: slugs}
}

// adopt moves the quarantined chat dir for projectID (if any) into the
// session hub's profile at hubRoot. Call only AFTER the APS roster
// authorization passed for this project. Nil-safe; failures other than
// dest-exists keep the index entry so a later request retries.
func (q *chatQuarantine) adopt(projectID, hubRoot string, log *slog.Logger) {
	if q == nil || hubRoot == "" {
		return
	}
	// hubslug.Slug is byte-identical to the stores' sanitizeID, so the slug
	// computed here matches the directory name chat.Store would use.
	slug := hubslug.Slug(projectID)

	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.slugs[slug]; !ok {
		return
	}
	src := filepath.Join(q.dir, slug)
	dst := filepath.Join(hubRoot, "chat", slug)
	if _, err := os.Lstat(dst); err == nil {
		// The hub already has chat data for this project (e.g. written fresh
		// after migration). Never merge or overwrite — leave the quarantined
		// copy for hand recovery and stop retrying.
		log.Warn("chat: quarantined project data NOT adopted — destination already exists",
			"project", projectID, "quarantined", src, "dest", dst)
		delete(q.slugs, slug)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		log.Error("chat: quarantine adoption failed", "project", projectID, "err", err)
		return
	}
	if err := hubmigrate.MoveDir(src, dst); err != nil {
		log.Error("chat: quarantine adoption failed", "project", projectID, "err", err)
		return
	}
	delete(q.slugs, slug)
	log.Info("chat: adopted quarantined project data into the session hub",
		"project", projectID, "dest", dst)
}
