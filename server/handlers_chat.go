package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/notifications"
)

// Chat endpoints (docs/chat/PLAN.md, phase 1). Every handler front-doors
// through the chat authorizer — the caller's APS project role mapped to
// capabilities, plus the private-channel ACL — before touching the store;
// there is no parallel permission system. Reads and writes are REST; the
// SPA polls for updates until the SSE stream lands in phase 2.
//
// Because private channels must not leak their existence, a channel the
// caller cannot access answers 404 (not 403) everywhere.

// chatMaxBody caps every chat request body (same 64 KiB cap as pins).
const chatMaxBody = 64 << 10

// chatCtx is what every chat handler resolves first: the caller's token,
// identity, session id (the rate-limit key), the project in question, and
// the SESSION HUB's chat store + SSE hub (from the requireHub choke point —
// never from any wire hub id).
type chatCtx struct {
	projectID string
	token     string
	id        chat.Identity
	sessID    string

	store *chat.Store
	hub   *chat.Hub
	hubID string
	// notif is the session hub's per-user notification store, so a posted
	// @mention can drop an inbox entry for the mentioned user without a
	// second store lookup. Resolved from the same set as store.
	notif *notifications.Store
}

// chatReq gates a chat request: hub store set resolved, session + token
// present, projectId given. Writes the error response itself when not ok.
func (s *Server) chatReq(w http.ResponseWriter, r *http.Request) (chatCtx, bool) {
	set, ok := reqStores(w, r)
	if !ok {
		return chatCtx{}, false
	}
	tok, ok := s.token(r.Context(), w, r)
	if !ok {
		return chatCtx{}, false
	}
	sess, ok := sessionFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return chatCtx{}, false
	}
	projectID, ok := reqParam(w, r, "projectId")
	if !ok {
		return chatCtx{}, false
	}
	return chatCtx{
		projectID: projectID,
		token:     tok,
		id:        chat.Identity{UserID: sess.Profile.Sub, Email: sess.Profile.Email},
		sessID:    sess.ID,
		store:     set.chat,
		hub:       set.chatHub,
		hubID:     set.hubID,
		notif:     set.notifications,
	}, true
}

// chatCan enforces a capability, writing 403 (or the fetch failure) itself.
func (s *Server) chatCan(ctx context.Context, w http.ResponseWriter, r *http.Request, c chatCtx, cap chat.Capability) bool {
	ok, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, cap)
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, safeErrorMessage(http.StatusForbidden))
		return false
	}
	return true
}

// chatChannel resolves a channel the caller may access. Inaccessible
// private channels 404 like unknown ones, hiding their existence.
func (s *Server) chatChannel(ctx context.Context, w http.ResponseWriter, r *http.Request, c chatCtx, channelID string) (chat.Channel, bool) {
	ch, err := c.store.GetChannel(c.projectID, channelID)
	if err != nil {
		s.chatError(w, r, err)
		return chat.Channel{}, false
	}
	ok, err := s.chatAuthz.CanAccessChannel(ctx, c.token, c.id, c.projectID, ch)
	if err != nil {
		s.fail(w, r, err)
		return chat.Channel{}, false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such channel")
		return chat.Channel{}, false
	}
	return ch, true
}

// chatModOrCreator reports whether the caller moderates the project or
// created the channel — the bar for rename/archive/member management.
func (s *Server) chatModOrCreator(ctx context.Context, w http.ResponseWriter, r *http.Request, c chatCtx, ch chat.Channel) (bool, bool) {
	mod, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapModerate)
	if err != nil {
		s.fail(w, r, err)
		return false, false
	}
	return mod || (ch.CreatedBy != "" && ch.CreatedBy == c.id.UserID), true
}

// chatError maps store errors onto the uniform envelope. Store sentinel
// texts are our own and safe to echo; raw I/O errors are not (they carry
// filesystem paths), so those log fully and answer generically.
func (s *Server) chatError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, chat.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, chat.ErrFutureVersion):
		s.logger.Error("chat: refusing data from a newer version", "err", err)
		writeError(w, http.StatusServiceUnavailable, "chat data on this server was written by a newer version")
	default:
		s.logger.Error("chat: storage error", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "chat storage error")
	}
}

// chatPublish emits one event on the SESSION HUB's SSE stream for the
// project (REST → store → publish; docs/chat/PLAN.md phase 2). The hub
// rides in c, so an event can never fan out to another hub's subscribers.
// ch scopes visibility: events about a private channel reach only its
// members and project moderators. extra users receive the event regardless
// of the ACL (a just-removed member learning of their removal). Publish
// failures are logged, never surfaced — the write already succeeded, and
// pollers/reconnects converge anyway.
func (s *Server) chatPublish(c chatCtx, evType string, data any, ch chat.Channel, extra ...string) {
	if c.hub == nil {
		return
	}
	vis := chat.Vis{Private: ch.IsPrivate, Channel: ch, ExtraUserIDs: extra}
	if err := c.hub.Publish(c.projectID, chat.Event{Type: evType, V: 1, Data: data}, vis); err != nil {
		s.logger.Error("chat: publish failed", "type", evType, "err", err)
	}
}

func decodeChatBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, chatMaxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// reqSeq reads a required int64 query parameter (message seq).
func reqSeq(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw, ok := reqParam(w, r, name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return n, true
}

// ---- channels ----

// handleChatChannels lists the channels the caller can see: all public
// ones, plus private ones where they're a member (or project moderator).
func (s *Server) handleChatChannels(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	chans, err := c.store.Channels(c.projectID)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	out := []ChannelDTO{}
	for _, ch := range chans {
		if ch.IsPrivate {
			visible, aerr := s.chatAuthz.CanAccessChannel(ctx, c.token, c.id, c.projectID, ch)
			if aerr != nil {
				s.fail(w, r, aerr)
				return
			}
			if !visible {
				continue
			}
		}
		out = append(out, channelDTO(ch))
	}
	caps := ChatCapsDTO{}
	for _, probe := range []struct {
		cap chat.Capability
		dst *bool
	}{
		{chat.CapPost, &caps.Post},
		{chat.CapCreateChannel, &caps.CreateChannel},
		{chat.CapModerate, &caps.Moderate},
	} {
		v, cerr := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, probe.cap)
		if cerr != nil {
			s.fail(w, r, cerr)
			return
		}
		*probe.dst = v
	}
	writeJSON(w, http.StatusOK, ChannelListDTO{Channels: out, Capabilities: caps})
}

func (s *Server) handleChatChannelCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapCreateChannel) {
		return
	}
	if !s.chatOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		Name      string   `json:"name"`
		Topic     string   `json:"topic"`
		IsPrivate bool     `json:"isPrivate"`
		MemberIDs []string `json:"memberIds"`
	}
	if !decodeChatBody(w, r, &in) {
		return
	}
	if in.IsPrivate && c.id.UserID == "" {
		// Without a stable user id there is nothing to key the ACL on.
		writeError(w, http.StatusBadRequest, "your session has no user id; sign out and back in to create private channels")
		return
	}
	ch, err := c.store.CreateChannel(c.projectID, in.Name, in.Topic, c.id.UserID, in.IsPrivate, in.MemberIDs)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	s.chatPublish(c, "channel.created", ChatChannelEventDTO{Channel: channelDTO(ch)}, ch)
	writeJSON(w, http.StatusCreated, channelDTO(ch))
}

func (s *Server) handleChatChannelUpdate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	allowed, ok := s.chatModOrCreator(ctx, w, r, c, ch)
	if !ok {
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, safeErrorMessage(http.StatusForbidden))
		return
	}
	if !s.chatOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		Name  *string `json:"name"`
		Topic *string `json:"topic"`
	}
	if !decodeChatBody(w, r, &in) {
		return
	}
	updated, err := c.store.UpdateChannel(c.projectID, ch.ID, in.Name, in.Topic)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	s.chatPublish(c, "channel.updated", ChatChannelEventDTO{Channel: channelDTO(updated)}, updated)
	writeJSON(w, http.StatusOK, channelDTO(updated))
}

// handleChatChannelArchive is DELETE on a channel: soft-archive (the root
// channel refuses, per the store invariant).
func (s *Server) handleChatChannelArchive(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	allowed, ok := s.chatModOrCreator(ctx, w, r, c, ch)
	if !ok {
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, safeErrorMessage(http.StatusForbidden))
		return
	}
	archived, err := c.store.ArchiveChannel(c.projectID, ch.ID)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	s.chatPublish(c, "channel.archived", ChatChannelEventDTO{Channel: channelDTO(archived)}, archived)
	writeJSON(w, http.StatusOK, channelDTO(archived))
}

func (s *Server) handleChatMemberAdd(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	allowed, ok := s.chatModOrCreator(ctx, w, r, c, ch)
	if !ok {
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, safeErrorMessage(http.StatusForbidden))
		return
	}
	var in struct {
		UserID string `json:"userId"`
	}
	if !decodeChatBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.UserID) == "" {
		writeError(w, http.StatusBadRequest, "userId is required")
		return
	}
	// The target must be an active member of the project (design doc §REST).
	// This is a THIRD-party check, so it uses the strict IsActiveMember, not
	// Can: the caller's own project access (which the group-derived fallback
	// keys on) says nothing about whether the target belongs here.
	target := chat.Identity{UserID: in.UserID}
	isMember, err := s.chatAuthz.IsActiveMember(ctx, c.token, target, c.projectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !isMember {
		writeError(w, http.StatusBadRequest, "user is not an active member of this project")
		return
	}
	updated, err := c.store.AddChannelMember(c.projectID, ch.ID, in.UserID, c.id.UserID)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	// The post-add snapshot's ACL includes the new member, so this event is
	// how their sidebar learns the channel now exists for them.
	s.chatPublish(c, "channel.member_added",
		ChatMemberEventDTO{ChannelID: updated.ID, UserID: in.UserID, Channel: channelDTO(updated)}, updated)
	writeJSON(w, http.StatusOK, channelDTO(updated))
}

func (s *Server) handleChatMemberRemove(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	userID, ok := reqParam(w, r, "userId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	if userID != c.id.UserID { // anyone may leave; removing others takes moderator/creator
		allowed, ok := s.chatModOrCreator(ctx, w, r, c, ch)
		if !ok {
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, safeErrorMessage(http.StatusForbidden))
			return
		}
	}
	updated, err := c.store.RemoveChannelMember(c.projectID, ch.ID, userID)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	// The removed user is no longer in the ACL snapshot, so they're listed
	// explicitly — this event is how their sidebar drops the channel.
	s.chatPublish(c, "channel.member_removed",
		ChatMemberEventDTO{ChannelID: updated.ID, UserID: userID, Channel: channelDTO(updated)}, updated, userID)
	writeJSON(w, http.StatusOK, channelDTO(updated))
}

// ---- messages ----

// handleChatMessages reads a channel. Without cursors it returns the newest
// page of the top-level timeline; beforeSeq pages history backward;
// afterSeq returns the polling delta (every message, replies included,
// newer than the cursor).
func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}

	q := r.URL.Query()
	var msgs []chat.Message
	var err error
	if raw := q.Get("afterSeq"); raw != "" {
		after, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || after < 0 {
			writeError(w, http.StatusBadRequest, "invalid afterSeq")
			return
		}
		msgs, err = c.store.ListMessagesAfter(c.projectID, ch.ID, after)
	} else {
		var before int64
		if raw := q.Get("beforeSeq"); raw != "" {
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before <= 0 {
				writeError(w, http.StatusBadRequest, "invalid beforeSeq")
				return
			}
		}
		limit := 0
		if raw := q.Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit <= 0 {
				writeError(w, http.StatusBadRequest, "invalid limit")
				return
			}
		}
		msgs, err = c.store.ListMessages(c.projectID, ch.ID, before, limit)
	}
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	latest, err := c.store.LatestSeq(c.projectID, ch.ID)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, messageListDTO(msgs, latest))
}

// handleChatMessageCreate posts a message (or thread reply). clientMsgId
// dedupes retries: a replay answers 200 with the original message where a
// fresh create answers 201.
func (s *Server) handleChatMessageCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	if !s.chatMsgLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		Body          string `json:"body"`
		ClientMsgID   string `json:"clientMsgId"`
		ThreadRootSeq int64  `json:"threadRootSeq"`
	}
	if !decodeChatBody(w, r, &in) {
		return
	}
	authorName := c.id.Email
	if sess, ok := sessionFromCtx(r.Context()); ok && sess.Profile.Name != "" {
		authorName = sess.Profile.Name
	}
	msg, created, err := c.store.CreateMessage(c.projectID, ch.ID, c.id.UserID, authorName, in.ClientMsgID, in.Body, in.ThreadRootSeq)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	status := http.StatusOK
	if created { // a clientMsgId replay publishes nothing — it created nothing
		status = http.StatusCreated
		s.chatPublish(c, "message.created",
			ChatMessageEventDTO{ChannelID: ch.ID, Message: messageDTO(msg)}, ch)
		s.chatPublish(c, "channel.activity",
			ChatActivityEventDTO{ChannelID: ch.ID, LastMessageSeq: msg.Seq}, ch)
		// Turn any @mentions in the new message into inbox entries. Best-effort
		// and after the write — never fails the post.
		s.emitChatMentions(c, ch, msg)
	}
	writeJSON(w, status, messageDTO(msg))
}

// handleChatMessageEdit edits the caller's own message (moderators delete,
// they don't rewrite others' words — per the design doc's capability table).
func (s *Server) handleChatMessageEdit(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	seq, ok := reqSeq(w, r, "seq")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapEditOwn) {
		return
	}
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	msg, err := c.store.GetMessage(c.projectID, ch.ID, seq)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	if msg.AuthorID == "" || msg.AuthorID != c.id.UserID {
		writeError(w, http.StatusForbidden, "you can only edit your own messages")
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decodeChatBody(w, r, &in) {
		return
	}
	updated, err := c.store.EditMessage(c.projectID, ch.ID, seq, in.Body)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	s.chatPublish(c, "message.updated",
		ChatMessageEventDTO{ChannelID: ch.ID, Message: messageDTO(updated)}, ch)
	writeJSON(w, http.StatusOK, messageDTO(updated))
}

// handleChatMessageDelete soft-deletes: the author may delete their own
// message; moderators may delete anyone's.
func (s *Server) handleChatMessageDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	seq, ok := reqSeq(w, r, "seq")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	msg, err := c.store.GetMessage(c.projectID, ch.ID, seq)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	own := msg.AuthorID != "" && msg.AuthorID == c.id.UserID
	if own {
		if !s.chatCan(ctx, w, r, c, chat.CapEditOwn) {
			return
		}
	} else if !s.chatCan(ctx, w, r, c, chat.CapModerate) {
		return
	}
	deleted, err := c.store.DeleteMessage(c.projectID, ch.ID, seq)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	s.chatPublish(c, "message.deleted",
		ChatMessageEventDTO{ChannelID: ch.ID, Message: messageDTO(deleted)}, ch)
	writeJSON(w, http.StatusOK, messageDTO(deleted))
}

// handleChatThread returns a thread: root message first, replies ascending.
func (s *Server) handleChatThread(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	rootSeq, ok := reqSeq(w, r, "rootSeq")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	msgs, err := c.store.ListThread(c.projectID, ch.ID, rootSeq)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	latest, err := c.store.LatestSeq(c.projectID, ch.ID)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, messageListDTO(msgs, latest))
}

// ---- reactions ----

func (s *Server) handleChatReactionAdd(w http.ResponseWriter, r *http.Request) {
	s.handleChatReaction(w, r, (*chat.Store).AddReaction, "reaction.added")
}

func (s *Server) handleChatReactionRemove(w http.ResponseWriter, r *http.Request) {
	s.handleChatReaction(w, r, (*chat.Store).RemoveReaction, "reaction.removed")
}

// handleChatReaction shares the add/remove flow. apply is a method
// expression so it binds to the SESSION HUB's store resolved below, not to
// any store captured at route-registration time.
func (s *Server) handleChatReaction(w http.ResponseWriter, r *http.Request, apply func(cs *chat.Store, projectID, channelID string, seq int64, userID, emoji string) (chat.Message, error), evType string) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	seq, ok := reqSeq(w, r, "seq")
	if !ok {
		return
	}
	emoji, ok := reqParam(w, r, "emoji")
	if !ok {
		return
	}
	if len(emoji) > 64 {
		writeError(w, http.StatusBadRequest, "invalid emoji")
		return
	}
	// Adds come from the palette only; removal stays unrestricted so a
	// reaction placed under an older palette is still removable.
	if evType == "reaction.added" && !chat.IsAllowedEmoji(emoji) {
		writeError(w, http.StatusBadRequest, "unsupported emoji")
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapReact) {
		return
	}
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	if c.id.UserID == "" {
		writeError(w, http.StatusBadRequest, "your session has no user id; sign out and back in to react")
		return
	}
	msg, err := apply(c.store, c.projectID, ch.ID, seq, c.id.UserID, emoji)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	s.chatPublish(c, evType,
		ChatMessageEventDTO{ChannelID: ch.ID, Message: messageDTO(msg)}, ch)
	writeJSON(w, http.StatusOK, messageDTO(msg))
}

// handleChatMembers is GET /api/chat/members — the project's ACTIVE roster,
// for the private-channel member pickers (docs/chat/PLAN.md phase 5). Same
// source as the authorizer (api.GetProjectMembers with the caller's own
// token), so the picker can only ever offer users the ACL would accept.
func (s *Server) handleChatMembers(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	members, err := api.GetProjectMembers(ctx, c.token, c.projectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []MemberDTO{}
	for _, m := range members {
		if strings.EqualFold(m.Status, "ACTIVE") {
			out = append(out, MemberDTO{UserID: m.UserID, Name: m.Name, Email: m.Email, Role: m.Role})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- read cursors, unreads, typing (docs/chat/PLAN.md phase 4) ----

// cursorKey is the stable per-user key read cursors are stored under (and
// user-only events are addressed to): the OIDC sub, falling back to the
// lowercase email for sessions that predate the Sub claim.
func (c chatCtx) cursorKey() string {
	if c.id.UserID != "" {
		return c.id.UserID
	}
	return strings.ToLower(c.id.Email)
}

// handleChatRead is PATCH /api/chat/read — advance the caller's read cursor
// in a channel. Moves are monotonic (racing tabs can only advance) and the
// advance is echoed to the same user's other tabs as a user-only
// read.updated event carrying the fresh unread summary.
func (s *Server) handleChatRead(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	if !s.chatSyncLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		LastReadSeq int64 `json:"lastReadSeq"`
	}
	if !decodeChatBody(w, r, &in) {
		return
	}
	unread, advanced, err := c.store.SetReadCursor(c.projectID, c.cursorKey(), ch.ID, in.LastReadSeq)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	if advanced && c.hub != nil {
		ev := chat.Event{Type: "read.updated", V: 1, Data: unreadDTO(unread)}
		vis := chat.Vis{UserOnly: true, ExtraUserIDs: []string{c.cursorKey()}}
		if perr := c.hub.Publish(c.projectID, ev, vis); perr != nil {
			s.logger.Error("chat: publish failed", "type", "read.updated", "err", perr)
		}
	}
	writeJSON(w, http.StatusOK, unreadDTO(unread))
}

// handleChatUnreads is GET /api/chat/unreads — the caller's unread summary
// for every channel they can see (archived ones excluded).
func (s *Server) handleChatUnreads(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	chans, err := c.store.Channels(c.projectID)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	visible := []chat.Channel{}
	for _, ch := range chans {
		if ch.IsPrivate {
			v, aerr := s.chatAuthz.CanAccessChannel(ctx, c.token, c.id, c.projectID, ch)
			if aerr != nil {
				s.fail(w, r, aerr)
				return
			}
			if !v {
				continue
			}
		}
		visible = append(visible, ch)
	}
	unreads, err := c.store.Unreads(c.projectID, c.cursorKey(), visible)
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	out := ChatUnreadListDTO{Unreads: make([]ChatUnreadDTO, len(unreads))}
	for i, u := range unreads {
		out.Unreads[i] = unreadDTO(u)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleChatTyping is POST /api/chat/typing — an ephemeral "the caller is
// typing in this channel" ping, fanned out without an SSE id so it never
// advances Last-Event-ID and is never replayed. Nothing is stored.
func (s *Server) handleChatTyping(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	channelID, ok := reqParam(w, r, "channelId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.chatCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	ch, ok := s.chatChannel(ctx, w, r, c, channelID)
	if !ok {
		return
	}
	if !s.chatSyncLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	if ch.ArchivedAt == nil && c.hub != nil {
		name := c.id.Email
		if sess, sok := sessionFromCtx(r.Context()); sok && sess.Profile.Name != "" {
			name = sess.Profile.Name
		}
		ev := chat.Event{Type: "typing", V: 1, Data: ChatTypingEventDTO{
			ChannelID: ch.ID, UserID: c.cursorKey(), Name: name,
		}}
		vis := chat.Vis{Private: ch.IsPrivate, Channel: ch}
		if perr := c.hub.PublishEphemeral(c.projectID, ev, vis); perr != nil {
			s.logger.Error("chat: publish failed", "type", "typing", "err", perr)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
