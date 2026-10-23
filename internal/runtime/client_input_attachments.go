package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"strings"
	"unicode"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const MaxClientInputAttachmentBytes = 20 * 1024 * 1024

// ClientInputAttachment carries only explicitly selected client bytes. It is
// never populated from plugin input, a model-selected path, or a URL.
type ClientInputAttachment struct {
	SHA256    string `json:"sha256"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func ValidateClientUserInput(input ClientUserInput) error {
	if input.ResumeReceiptID != "" {
		if strings.TrimSpace(input.RequestID) == "" || len(input.RequestID) > 128 || strings.TrimSpace(input.ResumeReceiptID) == "" || len(input.ResumeReceiptID) > 128 || input.Prompt != "" || len(input.Attachments) > 0 || input.Relation != nil {
			return errors.New("invalid explicit receipt resume intent")
		}
		return nil
	}

	if strings.TrimSpace(input.RequestID) == "" || len(input.RequestID) > 128 || (strings.TrimSpace(input.Prompt) == "" && len(input.Attachments) == 0) || len(input.Prompt) > 64000 {
		return errors.New("invalid explicit client user_input")
	}
	if len(input.Attachments) > 16 {
		return errors.New("client input supports at most 16 attachments")
	}
	total := 0
	for i, file := range input.Attachments {
		if strings.TrimSpace(file.Filename) == "" || len(file.Filename) > 255 || strings.ContainsAny(file.Filename, "/\\") || strings.IndexFunc(file.Filename, unicode.IsControl) >= 0 {
			return fmt.Errorf("attachment %d has an invalid filename", i+1)
		}
		if media, _, err := mime.ParseMediaType(file.MediaType); err != nil || media == "" || !strings.Contains(media, "/") {
			return fmt.Errorf("attachment %d has an invalid media type", i+1)
		}
		if len(file.SHA256) != 64 || strings.ToLower(file.SHA256) != file.SHA256 {
			return fmt.Errorf("attachment %d requires a SHA256 digest", i+1)
		}
		if len(file.Data) > base64.StdEncoding.EncodedLen(MaxClientInputAttachmentBytes-total) {
			return errors.New("client attachments exceed 20 MB")
		}
		data, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil || len(data) == 0 {
			return fmt.Errorf("attachment %d requires nonempty standard base64 bytes", i+1)
		}
		total += len(data)
		if total > MaxClientInputAttachmentBytes {
			return errors.New("client attachments exceed 20 MB")
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != file.SHA256 {
			return fmt.Errorf("attachment %d digest does not match its bytes", i+1)
		}
	}
	return nil
}

// ClientUserInputAttachments resolves the complete ordered selection from the
// admitted envelope. A plugin may forward references but cannot add, omit,
// rename or replace any part of the user's submitted selection.
func ClientUserInputAttachments(ctx context.Context, pluginID, requestID string, refs []pluginhost.SessionInputAttachment) ([]ClientInputAttachment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	proof, _ := ctx.Value(clientUserInputKey{}).(*clientUserInputProof)
	if proof == nil {
		return nil, errors.New("attachments require a live authenticated client submission")
	}
	if err := proof.admission.Err(); err != nil {
		return nil, errors.New("client user input admission has ended")
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	if proof.pluginID != pluginID || proof.input.RequestID != requestID || len(refs) != len(proof.input.Attachments) {
		return nil, errors.New("attachments do not match the admitted client selection")
	}
	for i, ref := range refs {
		file := proof.input.Attachments[i]
		if ref.SHA256 != file.SHA256 || ref.Filename != file.Filename || ref.MediaType != file.MediaType {
			return nil, errors.New("attachment reference does not match the admitted client selection")
		}
	}
	return append([]ClientInputAttachment(nil), proof.input.Attachments...), nil
}
