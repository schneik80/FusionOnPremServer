package server

import (
	"github.com/schneik80/fusionlocalserver/notifications"
)

// NotifUserDTO is the actor (who caused a notification) on the wire.
type NotifUserDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// NotificationDTO is one inbox entry on the wire. The SPA composes the
// localized sentence from kind + these params (src/i18n enums + notifications
// namespace); subject/channelName are user data rendered verbatim. ref is an
// fls: token the client unfurls for click-through.
type NotificationDTO struct {
	ID          string        `json:"id"`
	Kind        string        `json:"kind"`
	HubID       string        `json:"hubId,omitempty"`
	ProjectID   string        `json:"projectId,omitempty"`
	ProjectName string        `json:"projectName,omitempty"`
	Actor       *NotifUserDTO `json:"actor,omitempty"`
	Subject     string        `json:"subject,omitempty"`
	Ref         string        `json:"ref,omitempty"`
	ChannelID   string        `json:"channelId,omitempty"`
	ChannelName string        `json:"channelName,omitempty"`
	MessageSeq  int64         `json:"messageSeq,omitempty"`
	Read        bool          `json:"read"`
	CreatedAt   string        `json:"createdAt"`
	ReadAt      string        `json:"readAt,omitempty"`
}

// NotificationListDTO is GET /api/notifications: the caller's inbox plus the
// unread total, so the bell renders its badge from one request.
type NotificationListDTO struct {
	Notifications []NotificationDTO `json:"notifications"`
	Unread        int               `json:"unread"`
}

// NotifCountDTO is the mark-read / dismiss response: the fresh unread total so
// the badge updates without a refetch.
type NotifCountDTO struct {
	Unread int `json:"unread"`
}

func notifUserDTO(r *notifications.UserRef) *NotifUserDTO {
	if r == nil {
		return nil
	}
	return &NotifUserDTO{ID: r.ID, Name: r.Name, Email: r.Email}
}

func notificationDTO(n notifications.Notification) NotificationDTO {
	dto := NotificationDTO{
		ID:          n.ID,
		Kind:        n.Kind,
		HubID:       n.HubID,
		ProjectID:   n.ProjectID,
		ProjectName: n.ProjectName,
		Actor:       notifUserDTO(n.Actor),
		Subject:     n.Subject,
		Ref:         n.Ref,
		ChannelID:   n.ChannelID,
		ChannelName: n.ChannelName,
		MessageSeq:  n.MessageSeq,
		Read:        n.Read,
		CreatedAt:   fmtTime(n.CreatedAt),
	}
	if n.ReadAt != nil {
		dto.ReadAt = fmtTime(*n.ReadAt)
	}
	return dto
}
