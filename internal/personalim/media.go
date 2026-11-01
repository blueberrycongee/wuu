package personalim

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"unicode"
)

// InputAttachment is the authenticated client-input wire envelope, not a
// model-supplied artifact reference. The host keeps Data out of plugin input.
type InputAttachment struct {
	SHA256    string `json:"sha256"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// HistoryAttachment pins a visible attachment to its immutable history record.
// It carries no arbitrary filesystem path or remotely supplied download URL.
type HistoryAttachment struct {
	Kind     string `json:"kind"`
	Index    int    `json:"index"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// These are the authenticated client-input wire limits. Host validation remains
// authoritative; transport downloads are bounded before constructing that input.
const inputMediaBytes = 8 * 1024 * 1024

type MediaDownloader interface {
	Download(context.Context, Attachment) ([]byte, error)
}

// DownloadInputAttachments fetches the exact enrolled user's attachment set.
// Callers persist the returned envelope before submission so crash recovery does
// not substitute newer provider bytes for the original accepted input.
func DownloadInputAttachments(ctx context.Context, d MediaDownloader, refs []Attachment) ([]InputAttachment, error) {
	if len(refs) > 16 {
		return nil, errors.New("Personal input supports at most 16 attachments")
	}
	result := make([]InputAttachment, 0, len(refs))
	total := 0
	for i, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ref.Size > int64(inputMediaBytes-total) {
			return nil, errors.New("Personal input attachments exceed 8 MiB")
		}
		name := ref.Name
		if name == "" {
			name = fmt.Sprintf("attachment-%d", i+1)
		}
		if len(name) > 255 || strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return nil, errors.New("provider attachment has an unsafe filename")
		}
		data, err := d.Download(ctx, ref)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, errors.New("provider attachment is empty")
		}
		total += len(data)
		if total > inputMediaBytes {
			return nil, errors.New("Personal input attachments exceed 8 MiB")
		}
		media := ref.MIMEType
		if media == "" {
			media = http.DetectContentType(data)
		}
		parsed, _, err := mime.ParseMediaType(media)
		if err != nil || !strings.Contains(parsed, "/") {
			return nil, errors.New("provider attachment has an invalid media type")
		}
		sum := sha256.Sum256(data)
		result = append(result, InputAttachment{SHA256: hex.EncodeToString(sum[:]), Filename: name, MediaType: media, Data: base64.StdEncoding.EncodeToString(data)})
	}
	return result, nil
}

// ReadHistoryAttachment reads only through Personal's owner-scoped attachment
// API, verifies every page and the final digest, and never opens a local path.
func ReadHistoryAttachment(ctx context.Context, c Client, agentID string, seq int, ref HistoryAttachment) (Attachment, []byte, error) {
	if agentID == "" || seq < 1 || ref.Index < 0 || ref.Size < 1 || ref.Size > inputMediaBytes || len(ref.SHA256) != 64 || (ref.Kind != "image" && ref.Kind != "file" && ref.Kind != "artifact") {
		return Attachment{}, nil, errors.New("invalid Personal attachment reference")
	}
	expectedTotal := base64.StdEncoding.EncodedLen(int(ref.Size))
	encoded := make([]byte, 0, expectedTotal)
	for len(encoded) < expectedTotal {
		if err := ctx.Err(); err != nil {
			return Attachment{}, nil, err
		}
		var page struct {
			Data        string `json:"data"`
			Offset      int    `json:"offset"`
			Total       int    `json:"total"`
			ContentType string `json:"content_type"`
			Name        string `json:"name"`
			SHA256      string `json:"sha256"`
		}
		if err := invoke(ctx, c, "agents.attachments.read", map[string]any{"agent_id": agentID, "seq": seq, "kind": ref.Kind, "index": ref.Index, "sha256": ref.SHA256, "offset": len(encoded)}, &page); err != nil {
			return Attachment{}, nil, err
		}
		if page.Offset != len(encoded) || page.Total != expectedTotal || page.SHA256 != ref.SHA256 || page.Name != ref.Name || page.ContentType != ref.MIMEType || len(page.Data) == 0 || len(page.Data) > expectedTotal-len(encoded) {
			return Attachment{}, nil, errors.New("Personal attachment changed during transfer")
		}
		encoded = append(encoded, page.Data...)
	}
	data, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return Attachment{}, nil, errors.New("invalid Personal attachment encoding")
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != ref.Size || hex.EncodeToString(sum[:]) != ref.SHA256 {
		return Attachment{}, nil, errors.New("Personal attachment digest mismatch")
	}
	kind := "file"
	if strings.HasPrefix(ref.MIMEType, "image/") {
		kind = "image"
	}
	return Attachment{Name: ref.Name, MIMEType: ref.MIMEType, Size: ref.Size, Kind: kind}, data, nil
}

// PrepareOutputAttachment resolves pinned host bytes. Feishu's upload creates a
// resource but does not send a message; persist the returned key before moving
// the delivery to sending. Slack's preparation has no external write.
func PrepareOutputAttachment(ctx context.Context, driver Driver, client Client, agentID string, seq int, ref HistoryAttachment) (Attachment, []byte, error) {
	attachment, data, err := ReadHistoryAttachment(ctx, client, agentID, seq, ref)
	if err != nil {
		return Attachment{}, nil, err
	}
	switch d := driver.(type) {
	case *feishuDriver:
		uploaded, err := d.Upload(ctx, attachment, bytes.NewReader(data))
		if err != nil {
			return Attachment{}, nil, err
		}
		return uploaded, nil, nil
	case *slackDriver:
		return attachment, data, nil
	default:
		return Attachment{}, nil, errors.New("messaging transport does not support outbound attachments")
	}
}

// SendOutputAttachment is an external communication effect. The caller must
// persist sending before invocation and must not repeat an uncertain result.
func SendOutputAttachment(ctx context.Context, driver Driver, message Outgoing, attachment Attachment, data []byte) (Receipt, error) {
	switch d := driver.(type) {
	case *feishuDriver:
		if attachment.ID == "" {
			return Receipt{}, errors.New("Feishu resource was not prepared")
		}
		message.Text = ""
		message.Attachments = []Attachment{attachment}
		return d.Post(ctx, message)
	case *slackDriver:
		if len(data) == 0 {
			return Receipt{}, errors.New("Slack attachment bytes are unavailable")
		}
		message.Text = ""
		return d.Upload(ctx, message, attachment.Name, data)
	default:
		return Receipt{}, errors.New("messaging transport does not support outbound attachments")
	}
}
