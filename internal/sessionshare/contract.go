// Package sessionshare defines the versioned, selected restricted-session view.
// It never returns raw thread records, execution authority, or bearer credentials.
package sessionshare

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/principal"
)

const ServiceName = "session.sharing"
const NativeModule = "wuu.session-sharing.default"
const ConsumerModule = "wuu.session-sharing-access"
const MaxProjectionBytes = 2 * 1024 * 1024
const MaxAttachmentBytes = 8 * 1024 * 1024
const MaxItems = 2000

// Selection is an intersection: explicit item IDs, type and [after,before).
// Empty lists disclose nothing. Tool results and attachments never inherit a
// message selection. There is no reasoning or raw-input kind in this protocol.
type Selection struct {
	MessageIDs    []string   `json:"message_ids,omitempty"`
	ToolResultIDs []string   `json:"tool_result_ids,omitempty"`
	AttachmentIDs []string   `json:"attachment_ids,omitempty"`
	After         *time.Time `json:"after,omitempty"`
	Before        *time.Time `json:"before,omitempty"`
}

// Item is canonical display data supplied only by the host. Data is base64 and
// only valid for an explicitly selected attachment. Arbitrary metadata and paths
// cannot cross this type boundary.
type Item struct {
	ID        string    `json:"id"`
	TurnID    string    `json:"turn_id,omitempty"`
	Kind      string    `json:"kind"`
	Role      string    `json:"role,omitempty"`
	Text      string    `json:"text,omitempty"`
	At        time.Time `json:"at"`
	MediaType string    `json:"media_type,omitempty"`
	Data      string    `json:"data,omitempty"`
}
type Source struct {
	SessionID string `json:"session_id"`
	Items     []Item `json:"items"`
}
type Attachment struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Bytes     int    `json:"bytes"`
}
type VisibleItem struct {
	ID         string      `json:"id"`
	TurnID     string      `json:"turn_id,omitempty"`
	Kind       string      `json:"kind"`
	Role       string      `json:"role,omitempty"`
	Text       string      `json:"text,omitempty"`
	At         time.Time   `json:"at"`
	Attachment *Attachment `json:"attachment,omitempty"`
}

// Text must be rendered as inert text. Embedded URLs and Markdown do not grant
// permission to fetch anything. Attachments use the authenticated share endpoint.
type Document struct {
	Version   int           `json:"version"`
	SessionID string        `json:"session_id"`
	Items     []VisibleItem `json:"items"`
}
type PreviewRequest struct {
	Source    Source    `json:"source"`
	Selection Selection `json:"selection"`
}
type Preview struct {
	Document     Document `json:"document"`
	ReviewDigest string   `json:"review_digest"`
	Warning      string   `json:"warning"`
}
type CreateRequest struct {
	// OperationID is stable across retries and scoped to Actor. Reusing it with
	// different intent fails; a retry never creates or renews a second grant.
	OperationID  string               `json:"operation_id"`
	Actor        principal.Identity   `json:"actor"`
	Source       Source               `json:"source"`
	Selection    Selection            `json:"selection"`
	Mode         string               `json:"mode"`
	Recipients   []principal.Identity `json:"recipients"`
	ExpiresAt    time.Time            `json:"expires_at"`
	Reviewed     bool                 `json:"reviewed"`
	ReviewDigest string               `json:"review_digest"`
	LiveUpdates  bool                 `json:"live_updates,omitempty"`
}
type Grant struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Mode      string    `json:"mode"`
	ExpiresAt time.Time `json:"expires_at"`
}
type ReadRequest struct {
	Actor     principal.Identity `json:"actor"`
	SessionID string             `json:"session_id"`
	ShareID   string             `json:"share_id"`
	Source    *Source            `json:"source,omitempty"`
}
type ReadPlan struct {
	Mode      string    `json:"mode"`
	Selection Selection `json:"selection"`
}
type AttachmentRequest struct {
	ReadRequest
	AttachmentID string `json:"attachment_id"`
}
type AttachmentResult struct {
	Attachment
	Data string `json:"data"`
}
type RevokeRequest struct {
	Actor     principal.Identity `json:"actor"`
	SessionID string             `json:"session_id"`
	ShareID   string             `json:"share_id"`
}

type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) invoke(ctx context.Context, method string, in, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	raw, err = c.call(ctx, method, raw)
	if err != nil {
		return err
	}
	if len(raw) > MaxProjectionBytes+MaxAttachmentBytes*2 {
		return errors.New("sharing response exceeds protocol limit")
	}
	return json.Unmarshal(raw, out)
}
func (c *Client) Preview(ctx context.Context, in PreviewRequest) (Preview, error) {
	var out Preview
	err := c.invoke(ctx, "preview", in, &out)
	if err == nil {
		err = out.Document.Validate()
	}
	return out, err
}
func (c *Client) Create(ctx context.Context, in CreateRequest) (Grant, error) {
	var out Grant
	err := c.invoke(ctx, "create", in, &out)
	if err == nil && (out.ID == "" || out.SessionID != in.Source.SessionID || (out.Mode != "snapshot" && out.Mode != "live")) {
		err = errors.New("invalid sharing grant")
	}
	return out, err
}
func (c *Client) Resolve(ctx context.Context, in ReadRequest) (ReadPlan, error) {
	var out ReadPlan
	err := c.invoke(ctx, "resolve", in, &out)
	if err == nil && (out.Mode != "snapshot" && out.Mode != "live") {
		err = errors.New("invalid sharing mode")
	}
	return out, err
}
func (c *Client) Read(ctx context.Context, in ReadRequest) (Document, error) {
	var out Document
	err := c.invoke(ctx, "read", in, &out)
	if err == nil {
		err = out.Validate()
	}
	if err == nil && out.SessionID != in.SessionID {
		err = errors.New("sharing source mismatch")
	}
	return out, err
}
func (c *Client) Attachment(ctx context.Context, in AttachmentRequest) (AttachmentResult, error) {
	var out AttachmentResult
	err := c.invoke(ctx, "attachment", in, &out)
	if err == nil {
		data, decodeErr := base64.StdEncoding.DecodeString(out.Data)
		sum := sha256.Sum256(data)
		if decodeErr != nil || out.ID != in.AttachmentID || out.Bytes != len(data) || len(data) > MaxAttachmentBytes || out.SHA256 != hex.EncodeToString(sum[:]) {
			err = errors.New("invalid shared attachment")
		}
	}
	return out, err
}
func (c *Client) Revoke(ctx context.Context, in RevokeRequest) error {
	return c.invoke(ctx, "revoke", in, new(struct{}))
}
func (c *Client) Authorize(ctx context.Context, in access.Request) (access.Decision, error) {
	var out access.Decision
	err := c.invoke(ctx, "authorize", in, &out)
	return out, err
}
func (d Document) Validate() error {
	if d.Version != 1 || d.SessionID == "" || len(d.Items) > MaxItems {
		return errors.New("invalid sharing document")
	}
	seen := map[string]bool{}
	for _, item := range d.Items {
		if item.ID == "" || seen[item.ID] {
			return errors.New("invalid shared item identity")
		}
		seen[item.ID] = true
		switch item.Kind {
		case "message":
			if item.Role != "user" && item.Role != "assistant" {
				return errors.New("invalid shared message role")
			}
			if item.Attachment != nil {
				return errors.New("message cannot contain an attachment")
			}
		case "tool_result":
			if item.Attachment != nil {
				return errors.New("result cannot contain an attachment")
			}
		case "attachment":
			if item.Attachment == nil || item.Attachment.ID != item.ID || item.Text != "" || len(item.Attachment.SHA256) != 64 || item.Attachment.Bytes < 0 || item.Attachment.Bytes > MaxAttachmentBytes {
				return errors.New("invalid shared attachment metadata")
			}
		default:
			return fmt.Errorf("unsupported shared item kind %q", item.Kind)
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(raw) > MaxProjectionBytes {
		return errors.New("sharing projection is too large; select fewer messages")
	}
	return nil
}
func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
