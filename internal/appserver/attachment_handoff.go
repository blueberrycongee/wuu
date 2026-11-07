package appserver

import (
	"encoding/base64"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Authorization for send is checked before this is used. A handoff supplies
// bytes, never filesystem paths or permission to read the sender's sessions.
// It remains plugin-origin/read-only input even on an owner-local connection.
func validateAttachmentHandoff(p pluginhost.SessionSendParams) ([]runtime.ClientInputAttachment, error) {
	h := p.Input.AttachmentHandoff
	if h == nil {
		return nil, nil
	}
	if h.Version != 1 || h.SessionID != p.SessionID || h.RequestID != p.RequestID || h.Source.SessionID == "" || h.Source.Seq < 1 || len(h.Attachments) == 0 || len(h.Attachments) > 16 || p.UserInputRequestID != "" || p.ResumeReceiptID != "" || len(p.Input.Attachments) > 0 || p.IfRunning == "steer" {
		return nil, errors.New("invalid attachment handoff binding or input mode")
	}
	files := make([]runtime.ClientInputAttachment, 0, len(h.Attachments))
	encoded := 0
	for _, f := range h.Attachments {
		encoded += len(f.Data)
		if encoded > base64.StdEncoding.EncodedLen(pluginhost.MaxSessionHandoffBytes)+4*len(h.Attachments) {
			return nil, errors.New("attachment handoff exceeds 20 MiB")
		}
		files = append(files, runtime.ClientInputAttachment{SHA256: f.SHA256, Filename: f.Filename, MediaType: f.MediaType, Data: f.Data})
	}
	if err := runtime.ValidateClientUserInput(runtime.ClientUserInput{RequestID: "validate-handoff", Prompt: "handoff", Attachments: files}); err != nil {
		return nil, err
	}
	return files, nil
}
