package server

import (
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
)

// ChannelDTO is a chat channel on the wire. MemberIDs is populated only for
// private channels (and only for callers who can see them at all).
type ChannelDTO struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Topic      string   `json:"topic"`
	IsRoot     bool     `json:"isRoot"`
	IsPrivate  bool     `json:"isPrivate"`
	CreatedBy  string   `json:"createdBy"`
	CreatedAt  string   `json:"createdAt"`
	ArchivedAt string   `json:"archivedAt,omitempty"`
	MemberIDs  []string `json:"memberIds,omitempty"`
}

// MessageDTO is a chat message on the wire. Deleted messages keep their
// place in the timeline as tombstones: Deleted=true, empty body, no
// reactions. ThreadRoot is 0 for top-level messages.
type MessageDTO struct {
	Seq         int64         `json:"seq"`
	ThreadRoot  int64         `json:"threadRoot,omitempty"`
	AuthorID    string        `json:"authorId"`
	AuthorName  string        `json:"authorName"`
	ClientMsgID string        `json:"clientMsgId"`
	Body        string        `json:"body"`
	CreatedAt   string        `json:"createdAt"`
	EditedAt    string        `json:"editedAt,omitempty"`
	Deleted     bool          `json:"deleted"`
	ReplyCount  int           `json:"replyCount"`
	LastReplyAt string        `json:"lastReplyAt,omitempty"`
	Reactions   []ReactionDTO `json:"reactions"`
}

// ReactionDTO is one user's emoji on a message.
type ReactionDTO struct {
	UserID string `json:"userId"`
	Emoji  string `json:"emoji"`
	At     string `json:"at"`
}

// ChatCapsDTO tells the SPA what the caller may do in this project's chat,
// so it can disable the composer for read-only roles instead of letting a
// post bounce off the 403.
type ChatCapsDTO struct {
	Post          bool `json:"post"`
	CreateChannel bool `json:"createChannel"`
	Moderate      bool `json:"moderate"`
}

// ChannelListDTO is GET /api/chat/channels.
type ChannelListDTO struct {
	Channels     []ChannelDTO `json:"channels"`
	Capabilities ChatCapsDTO  `json:"capabilities"`
}

// MessageListDTO is GET /api/chat/messages: a page (or delta) of messages
// plus the channel's newest seq, which the client keeps as its polling
// cursor.
type MessageListDTO struct {
	Messages  []MessageDTO `json:"messages"`
	LatestSeq int64        `json:"latestSeq"`
}

// SSE event payloads (the `data` field of the {type, v, data} envelope).
// message.* and reaction.* events carry the full post-mutation message so
// the client can replace its copy wholesale; channel.activity is the
// lightweight sidebar signal (design doc §3).
type ChatMessageEventDTO struct {
	ChannelID string     `json:"channelId"`
	Message   MessageDTO `json:"message"`
}

type ChatChannelEventDTO struct {
	Channel ChannelDTO `json:"channel"`
}

type ChatMemberEventDTO struct {
	ChannelID string     `json:"channelId"`
	UserID    string     `json:"userId"`
	Channel   ChannelDTO `json:"channel"`
}

type ChatActivityEventDTO struct {
	ChannelID      string `json:"channelId"`
	LastMessageSeq int64  `json:"lastMessageSeq"`
}

// ChatUnreadDTO is one channel's unread summary for the caller. It is both
// an element of GET /api/chat/unreads and the payload of the user-only
// read.updated event (PATCH /api/chat/read), so marking a channel read in
// one tab updates the same user's other tabs.
type ChatUnreadDTO struct {
	ChannelID   string `json:"channelId"`
	LastReadSeq int64  `json:"lastReadSeq"`
	UnreadCount int    `json:"unreadCount"`
	LatestSeq   int64  `json:"latestSeq"`
}

// ChatUnreadListDTO is GET /api/chat/unreads.
type ChatUnreadListDTO struct {
	Unreads []ChatUnreadDTO `json:"unreads"`
}

// ChatTypingEventDTO is the ephemeral typing event (never persisted, never
// replayed; docs/chat/PLAN.md phase 4).
type ChatTypingEventDTO struct {
	ChannelID string `json:"channelId"`
	UserID    string `json:"userId"`
	Name      string `json:"name"`
}

func channelDTO(c chat.Channel) ChannelDTO {
	out := ChannelDTO{
		ID:         c.ID,
		Name:       c.Name,
		Topic:      c.Topic,
		IsRoot:     c.IsRoot,
		IsPrivate:  c.IsPrivate,
		CreatedBy:  c.CreatedBy,
		CreatedAt:  fmtTime(c.CreatedAt),
		ArchivedAt: fmtTimePtr(c.ArchivedAt),
	}
	if c.IsPrivate {
		out.MemberIDs = make([]string, len(c.Members))
		for i, m := range c.Members {
			out.MemberIDs[i] = m.UserID
		}
	}
	return out
}

func messageDTO(m chat.Message) MessageDTO {
	out := MessageDTO{
		Seq:         m.Seq,
		ThreadRoot:  m.ThreadRoot,
		AuthorID:    m.AuthorID,
		AuthorName:  m.AuthorName,
		ClientMsgID: m.ClientMsgID,
		Body:        m.Body,
		CreatedAt:   fmtTime(m.CreatedAt),
		EditedAt:    fmtTimePtr(m.EditedAt),
		Deleted:     m.DeletedAt != nil,
		ReplyCount:  m.ReplyCount,
		LastReplyAt: fmtTimePtr(m.LastReplyAt),
		Reactions:   make([]ReactionDTO, len(m.Reactions)),
	}
	for i, rx := range m.Reactions {
		out.Reactions[i] = ReactionDTO{UserID: rx.UserID, Emoji: rx.Emoji, At: fmtTime(rx.At)}
	}
	return out
}

func messageListDTO(ms []chat.Message, latestSeq int64) MessageListDTO {
	out := MessageListDTO{Messages: make([]MessageDTO, len(ms)), LatestSeq: latestSeq}
	for i, m := range ms {
		out.Messages[i] = messageDTO(m)
	}
	return out
}

func unreadDTO(u chat.Unread) ChatUnreadDTO {
	return ChatUnreadDTO{
		ChannelID:   u.ChannelID,
		LastReadSeq: u.LastReadSeq,
		UnreadCount: u.UnreadCount,
		LatestSeq:   u.LatestSeq,
	}
}

// fmtTimePtr renders an optional timestamp as RFC3339, or "" when absent.
func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmtTime(*t)
}

// ChatImageDTO is returned by POST /api/chat/image after an attachment
// upload — the lineage urn the fls:img token references, plus the stored name.
type ChatImageDTO struct {
	ItemID string `json:"itemId"`
	Name   string `json:"name"`
}
