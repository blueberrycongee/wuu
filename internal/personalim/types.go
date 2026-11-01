// Package personalim connects enrolled messaging identities to Personal without
// treating provider message content as transport authority.
package personalim

import "time"

type Incoming struct {
	Platform       string       `json:"platform"`
	AppID          string       `json:"app_id"`
	TenantID       string       `json:"tenant_id"`
	SenderID       string       `json:"sender_id"`
	ConversationID string       `json:"conversation_id"`
	ThreadID       string       `json:"thread_id"`
	MessageID      string       `json:"message_id"`
	EventID        string       `json:"event_id"`
	Text           string       `json:"text"`
	Attachments    []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name"`
	MIMEType  string `json:"mime_type"`
	URL       string `json:"url,omitempty"`
	Size      int64  `json:"size"`
}

type Outgoing struct {
	ConversationID  string       `json:"conversation_id"`
	ThreadID        string       `json:"thread_id"`
	Text            string       `json:"text"`
	ClientMessageID string       `json:"client_message_id"`
	Attachments     []Attachment `json:"attachments,omitempty"`
}

type Receipt struct {
	AttachmentID   string `json:"attachment_id,omitempty"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

type ConnectionState struct {
	State       string    `json:"state"`
	LastError   string    `json:"last_error,omitempty"`
	ConnectedAt time.Time `json:"connected_at,omitempty"`
}
