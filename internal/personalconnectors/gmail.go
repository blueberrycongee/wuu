package personalconnectors

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

func buildGmail(c Connection, op string, raw json.RawMessage) (Plan, error) {
	var in struct {
		Query        string   `json:"query,omitempty"`
		PageToken    string   `json:"page_token,omitempty"`
		ThreadID     string   `json:"thread_id,omitempty"`
		MessageID    string   `json:"message_id,omitempty"`
		AttachmentID string   `json:"attachment_id,omitempty"`
		To           []string `json:"to,omitempty"`
		Subject      string   `json:"subject,omitempty"`
		Body         string   `json:"body,omitempty"`
	}
	if e := decode(raw, &in); e != nil {
		return Plan{}, e
	}
	p := Plan{Method: "GET", Resource: "me", RequiredScopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}}
	base := "https://gmail.googleapis.com/gmail/v1/users/me/"
	identifier := func(v string) bool {
		return v != "" && len(v) <= 1024 && !strings.ContainsAny(v, "/\\?#\r\n ") && v != "." && v != ".."
	}
	switch op {
	case "gmail.search":
		if len(in.Query) > 2048 || len(in.PageToken) > 4096 {
			return Plan{}, fault("invalid_query")
		}
		q := url.Values{"maxResults": {"25"}, "q": {in.Query}}
		if in.PageToken != "" {
			q.Set("pageToken", in.PageToken)
		}
		p.URL = base + "threads?" + q.Encode()
	case "gmail.thread":
		if !identifier(in.ThreadID) {
			return Plan{}, fault("thread_id_required")
		}
		p.URL = base + "threads/" + url.PathEscape(in.ThreadID) + "?format=full"
	case "gmail.attachment":
		if !identifier(in.MessageID) || !identifier(in.AttachmentID) {
			return Plan{}, fault("attachment_identity_required")
		}
		p.URL = base + "messages/" + url.PathEscape(in.MessageID) + "/attachments/" + url.PathEscape(in.AttachmentID)
	case "gmail.draft", "gmail.send":
		if len(in.To) == 0 || len(in.To) > 50 || len(in.Body) > 256*1024 || len(in.Subject) > 512 || !utf8.ValidString(in.Body) || !utf8.ValidString(in.Subject) || strings.ContainsAny(in.Subject, "\r\n") {
			return Plan{}, fault("invalid_message")
		}
		recipients := make([]string, 0, len(in.To))
		for _, v := range in.To {
			a, e := mail.ParseAddress(v)
			if e != nil || len(v) > 254 || a.Address != v || strings.ContainsAny(v, "\r\n") {
				return Plan{}, fault("invalid_recipient")
			}
			if !slices.Contains(recipients, v) {
				recipients = append(recipients, v)
			}
		}
		encodedBody := base64.StdEncoding.EncodeToString([]byte(in.Body))
		lines := []string{}
		for len(encodedBody) > 76 {
			lines = append(lines, encodedBody[:76])
			encodedBody = encodedBody[76:]
		}
		lines = append(lines, encodedBody)
		message := "To: " + strings.Join(recipients, ",\r\n ") + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", in.Subject) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + strings.Join(lines, "\r\n") + "\r\n"
		payload := map[string]any{"raw": base64.RawURLEncoding.EncodeToString([]byte(message))}
		p.Method = "POST"
		p.Write = true
		p.Summary = "Send one email to " + strings.Join(recipients, ", ")
		p.URL = base + "messages/send"
		p.RequiredScopes = []string{"https://www.googleapis.com/auth/gmail.send"}
		if op == "gmail.draft" {
			p.URL = base + "drafts"
			payload = map[string]any{"message": payload}
			p.RequiredScopes = []string{"https://www.googleapis.com/auth/gmail.compose"}
			p.Summary = "Create one Gmail draft for " + strings.Join(recipients, ", ")
		}
		p.Body, _ = json.Marshal(payload)
		p.Filter = func(data []byte) (json.RawMessage, error) {
			var response struct {
				ID       string `json:"id"`
				ThreadID string `json:"threadId"`
				Message  struct {
					ID       string `json:"id"`
					ThreadID string `json:"threadId"`
				} `json:"message"`
			}
			if json.Unmarshal(data, &response) != nil || !identifier(response.ID) {
				return nil, fault("message_acknowledgement_missing")
			}
			if op == "gmail.draft" {
				if !identifier(response.Message.ID) {
					return nil, fault("draft_acknowledgement_missing")
				}
				return providerJSON(map[string]string{"draft_id": response.ID, "message_id": response.Message.ID, "state": "draft_created", "recipient_delivery": "not_sent"}), nil
			}
			if !identifier(response.ThreadID) {
				return nil, fault("message_acknowledgement_missing")
			}
			return providerJSON(map[string]string{"message_id": response.ID, "thread_id": response.ThreadID, "state": "provider_accepted", "recipient_delivery": "unverified"}), nil
		}
	default:
		return Plan{}, fault("unsupported_operation")
	}
	return p, nil
}
