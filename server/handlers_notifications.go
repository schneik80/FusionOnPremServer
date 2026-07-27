package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/notifications"
	"github.com/schneik80/fusionlocalserver/tasks"
)

// dueSoonDays is how many days ahead a due date is flagged "due soon". Inside
// this window (and not yet past) a task assigned to the caller earns one
// due_soon reminder; once the due date passes it earns one overdue reminder.
const dueSoonDays = 2

// Notification endpoints. The inbox is the caller's OWN — like /api/tasks/mine
// it needs no per-project roster check: a notification the server addressed to
// you is always yours to read. Authorization is structural instead: the store
// set comes from requireHub (the session's hub), so an inbox scopes to the
// session hub and can't be reached across hubs; and the user key comes from
// the session identity, never the wire, so one user can't read another's inbox.
//
// The server is the only author of notifications — clients read, mark read,
// and dismiss, but never create. Emission happens inside the write paths that
// trigger it (chat mentions here; task/production emitters elsewhere).

// notifMaxBody caps every notification request body (same 64 KiB cap as chat).
const notifMaxBody = 64 << 10

// notifCtx is what every notification handler resolves first: the caller's
// per-user inbox key and the SESSION HUB's notification store. userID/email
// are the split identity the temporal reminder reconciler needs to find the
// caller's assigned tasks (tasks.Mine matches by sub, then email); tasks is
// the session hub's task store, read at inbox-fetch time to derive due-soon /
// overdue reminders without a background scheduler.
type notifCtx struct {
	userKey string
	userID  string
	email   string
	store   *notifications.Store
	tasks   *tasks.Store
	// chat backs the derived unread rows — see chatUnreadRows.
	chat   *chat.Store
	sessID string
}

func decodeNotifBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, notifMaxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// notifUserKey is the stable per-user inbox key: the OIDC sub, falling back to
// the lowercased email for sessions predating the sub claim — the same rule
// chat's cursorKey uses for read cursors and user-addressed events, so a
// mention addressed to a user lands in the same key they read from.
func notifUserKey(id chat.Identity) string {
	if id.UserID != "" {
		return id.UserID
	}
	return strings.ToLower(id.Email)
}

// notifSession gates a notification request: store available, session present.
// Writes the error response itself when not ok.
func (s *Server) notifSession(w http.ResponseWriter, r *http.Request) (notifCtx, bool) {
	set, ok := reqStores(w, r)
	if !ok {
		return notifCtx{}, false
	}
	sess, ok := sessionFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return notifCtx{}, false
	}
	key := notifUserKey(chat.Identity{UserID: sess.Profile.Sub, Email: sess.Profile.Email})
	if key == "" {
		// No stable identity → nothing to key an inbox on. An empty inbox is
		// the honest answer rather than a 500.
		return notifCtx{store: nil}, true
	}
	return notifCtx{
		userKey: key,
		userID:  sess.Profile.Sub,
		email:   sess.Profile.Email,
		store:   set.notifications,
		tasks:   set.tasks,
		chat:    set.chat,
		sessID:  sess.ID,
	}, true
}

// handleNotifList returns the caller's inbox (newest first) plus the unread
// total. This is also where temporal reminders (due-soon/overdue) reconcile —
// see reconcileTaskReminders, wired in stage 2.
func (s *Server) handleNotifList(w http.ResponseWriter, r *http.Request) {
	c, ok := s.notifSession(w, r)
	if !ok {
		return
	}
	if c.store == nil || c.userKey == "" {
		writeJSON(w, http.StatusOK, NotificationListDTO{Notifications: []NotificationDTO{}, Unread: 0})
		return
	}
	// Temporal reminders are derived at read time (no background scheduler):
	// the reconciler emits any missing due-soon/overdue entries for the
	// caller's own assigned tasks, idempotent on dedupe key. Best-effort —
	// a failure here must not hide the inbox the user already has.
	s.reconcileTaskReminders(c)
	list, err := c.store.List(c.userKey, 0)
	if err != nil {
		s.notifError(w, r, err)
		return
	}
	unread, err := c.store.UnreadCount(c.userKey)
	if err != nil {
		s.notifError(w, r, err)
		return
	}
	// Chat rows are DERIVED here rather than stored, which is what keeps them
	// honest: they are recomputed from the read cursors on every fetch, so
	// opening a channel makes its row disappear by itself — there is no stored
	// row to go stale, and no emission on chat's write path fanning out to
	// every member of a busy channel.
	chatRows := s.chatUnreadRows(c)
	out := NotificationListDTO{
		Notifications: make([]NotificationDTO, 0, len(list)+len(chatRows)),
		Unread:        unread + len(chatRows),
	}
	// Newest first overall: chat rows carry the timestamp of their newest
	// unread message, so they interleave with stored rows rather than being
	// pinned to the top.
	out.Notifications = append(out.Notifications, chatRows...)
	for _, n := range list {
		out.Notifications = append(out.Notifications, notificationDTO(n))
	}
	sort.SliceStable(out.Notifications, func(i, j int) bool {
		return out.Notifications[i].CreatedAt > out.Notifications[j].CreatedAt
	})
	writeJSON(w, http.StatusOK, out)
}

// handleNotifMarkRead marks the given notification ids read and returns the
// fresh unread total.
func (s *Server) handleNotifMarkRead(w http.ResponseWriter, r *http.Request) {
	c, ok := s.notifSession(w, r)
	if !ok {
		return
	}
	if c.store == nil || c.userKey == "" {
		writeJSON(w, http.StatusOK, NotifCountDTO{Unread: 0})
		return
	}
	if !s.notifOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeNotifBody(w, r, &in) {
		return
	}
	unread, err := c.store.MarkRead(c.userKey, in.IDs)
	if err != nil {
		s.notifError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NotifCountDTO{Unread: unread})
}

// handleNotifMarkAllRead marks the caller's entire inbox read.
func (s *Server) handleNotifMarkAllRead(w http.ResponseWriter, r *http.Request) {
	c, ok := s.notifSession(w, r)
	if !ok {
		return
	}
	if c.store == nil || c.userKey == "" {
		writeJSON(w, http.StatusOK, NotifCountDTO{Unread: 0})
		return
	}
	if !s.notifOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	unread, err := c.store.MarkAllRead(c.userKey)
	if err != nil {
		s.notifError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NotifCountDTO{Unread: unread})
}

// handleNotifDismiss removes one notification from the caller's inbox.
func (s *Server) handleNotifDismiss(w http.ResponseWriter, r *http.Request) {
	c, ok := s.notifSession(w, r)
	if !ok {
		return
	}
	id, ok := reqParam(w, r, "id")
	if !ok {
		return
	}
	if c.store == nil || c.userKey == "" {
		writeJSON(w, http.StatusOK, NotifCountDTO{Unread: 0})
		return
	}
	if !s.notifOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	unread, err := c.store.Delete(c.userKey, id)
	if err != nil {
		s.notifError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NotifCountDTO{Unread: unread})
}

// notifError maps store errors onto the uniform envelope. The store's own
// sentinel texts are safe to echo; raw I/O errors carry filesystem paths and
// answer generically after a full log.
func (s *Server) notifError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, notifications.ErrFutureVersion):
		s.logger.Error("notifications: refusing data from a newer version", "err", err)
		writeError(w, http.StatusServiceUnavailable, "notification data on this server was written by a newer version")
	default:
		s.logger.Error("notifications: storage error", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "notification storage error")
	}
}

// ---- emission seams ----

// emitChatMentions turns @mentions in a just-posted message into inbox
// entries for the mentioned users. It runs after the message is stored and
// never fails the request — a mention notification is best-effort. No APS
// call: the mention tokens carry the target ids, and a private channel's ACL
// (ch.Members) is local, so a mention in a private channel only notifies its
// members (never leaking the channel to an outsider). You are never notified
// for mentioning yourself.
func (s *Server) emitChatMentions(c chatCtx, ch chat.Channel, msg chat.Message) {
	if c.notif == nil {
		return
	}
	mentions := notifications.ParseMentions(msg.Body)
	if len(mentions) == 0 {
		return
	}
	var acl map[string]bool
	if ch.IsPrivate {
		acl = make(map[string]bool, len(ch.Members))
		for _, m := range ch.Members {
			acl[m.UserID] = true
		}
	}
	actor := &notifications.UserRef{ID: c.id.UserID, Name: msg.AuthorName, Email: c.id.Email}
	for _, m := range mentions {
		if m.UserID == "" || m.UserID == c.id.UserID {
			continue // never notify yourself
		}
		if acl != nil && !acl[m.UserID] {
			continue // private channel: don't leak it to a non-member
		}
		if _, _, err := c.notif.Add(m.UserID, notifications.Notification{
			Kind:        notifications.KindMention,
			HubID:       c.hubID,
			ProjectID:   c.projectID,
			Actor:       actor,
			Subject:     ch.Name,
			ChannelID:   ch.ID,
			ChannelName: ch.Name,
			MessageSeq:  msg.Seq,
		}); err != nil {
			s.logger.Error("notifications: mention emit failed", "target", m.UserID, "err", err)
		}
	}
}

// reconcileTaskReminders derives due-soon/overdue reminders for the caller's
// own assigned tasks at inbox-read time — no background scheduler. It scans
// tasks.Mine (the caller's cross-project tasks on the session hub), keeps the
// ones assigned to the caller with a due date and not done, and emits one
// reminder per task per threshold. The dedupe key folds in the due date, so a
// reminder fires at most once, but a rescheduled task earns a fresh one; and
// due_soon vs overdue are distinct keys, so a task crossing its deadline
// picks up an overdue reminder even though it was flagged due-soon earlier.
// Best-effort: any error is swallowed (the inbox the user already has must
// still render).
func (s *Server) reconcileTaskReminders(c notifCtx) {
	if c.store == nil || c.tasks == nil || c.userKey == "" {
		return
	}
	mine, err := c.tasks.Mine(c.userID, c.email)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	soon := now.AddDate(0, 0, dueSoonDays).Format("2006-01-02")
	for _, pt := range mine {
		t := pt.Task
		if t.DueDate == "" || t.Status == "done" {
			continue
		}
		// tasks.Mine also returns tasks the caller CREATED; a reminder is only
		// for a task assigned to them.
		if t.Assignee == nil || !refMatchesUser(t.Assignee.ID, t.Assignee.Email, c.userID, c.email) {
			continue
		}
		var kind, key string
		switch {
		case t.DueDate < today: // date-only strings sort as dates
			kind = notifications.KindOverdue
			key = "overdue:" + pt.ProjectID + ":" + t.ID + ":" + t.DueDate
		case t.DueDate <= soon:
			kind = notifications.KindDueSoon
			key = "due_soon:" + pt.ProjectID + ":" + t.ID + ":" + t.DueDate
		default:
			continue
		}
		if _, _, aerr := c.store.Add(c.userKey, notifications.Notification{
			Kind:        kind,
			HubID:       pt.HubID,
			ProjectID:   pt.ProjectID,
			ProjectName: pt.ProjectName,
			Subject:     t.Title,
			DedupeKey:   key,
		}); aerr != nil {
			s.logger.Error("notifications: reminder emit failed", "task", t.ID, "err", aerr)
		}
	}
}

// refMatchesUser reports whether a captured user ref (id + email) is the given
// user, matched by OIDC sub first and case-insensitive email as a fallback —
// the same rule tasks.Mine and chat use.
func refMatchesUser(refID, refEmail, userID, email string) bool {
	if refID != "" && refID == userID {
		return true
	}
	return refEmail != "" && email != "" && strings.EqualFold(refEmail, email)
}

// chatUnreadRows renders the caller's unread channels as inbox rows.
//
// They are derived, not stored, so they have no id to mark read or dismiss:
// the way to clear one is to read the channel, which is what the row is asking
// you to do anyway. The client renders them with a synthetic id so React can
// key them and a click can navigate.
//
// Best-effort: a scan failure yields no chat rows rather than failing the
// inbox the user already has.
func (s *Server) chatUnreadRows(c notifCtx) []NotificationDTO {
	if c.chat == nil || c.userKey == "" {
		return nil
	}
	unreads, err := c.chat.MyUnreads(c.userKey)
	if err != nil {
		s.logger.Debug("notifications: chat unread scan failed", "err", err)
		return nil
	}
	out := make([]NotificationDTO, 0, len(unreads))
	for _, u := range unreads {
		out = append(out, NotificationDTO{
			// Derived rows carry no store id. The prefix marks them as such —
			// mark-read and dismiss reject anything they do not recognise, so
			// a client sending one back gets a clean 404 rather than a
			// mysterious no-op.
			ID:          derivedChatID(u.ProjectID, u.ChannelID),
			Kind:        notifications.KindChatUnread,
			ProjectID:   u.ProjectID,
			Subject:     u.ChannelName,
			ChannelID:   u.ChannelID,
			ChannelName: u.ChannelName,
			MessageSeq:  u.LatestSeq,
			Count:       u.UnreadCount,
			Derived:     true,
			Read:        false,
			CreatedAt:   fmtTime(u.LatestAt),
		})
	}
	return out
}

// derivedChatID is a stable client-side key for a derived row. It is not a
// store id and will never resolve to one.
func derivedChatID(projectID, channelID string) string {
	return "chat:" + projectID + ":" + channelID
}
