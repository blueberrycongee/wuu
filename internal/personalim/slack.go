// Package personalim connects authenticated messaging transports to Personal.
package personalim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// SlackError distinguishes known rejection from a write that may have landed.
// Uncertain writes require reconciliation, not an automatic retry. Error text
// contains only a provider code, never tokens, message text, or socket URLs.
type SlackError struct {
	Code       string
	RetryAfter time.Duration
	Uncertain  bool
}

func (e *SlackError) Error() string { return "Slack: " + e.Code }

// Slack uses Socket Mode, so the host needs no public inbound HTTP endpoint.
// Tokens are host-owned credentials and must not be persisted in bridge state.
// APIURL and HTTPClient may override transport for a local protocol fixture.
type Slack struct {
	AppToken   string
	BotToken   string
	APIURL     string
	HTTPClient *http.Client
	OnStatus   func(ConnectionState)
}

// Run reconnects until canceled or Slack rejects the app credentials. accept
// must durably enqueue quickly; Slack receives an ACK only after it succeeds.
// Duplicate deliveries keep the same EventID and are deduplicated by the caller.
func (s *Slack) Run(ctx context.Context, accept func(context.Context, Incoming) error) error {
	if s.AppToken == "" || accept == nil {
		return &SlackError{Code: "configuration_required"}
	}
	if s.OnStatus != nil {
		s.OnStatus(ConnectionState{State: "connecting"})
	}
	defer func() {
		if s.OnStatus != nil {
			s.OnStatus(ConnectionState{State: "disconnected"})
		}
	}()
	delay := time.Second
	for {
		err := s.receive(ctx, accept)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var fault *SlackError
		if errors.As(err, &fault) {
			switch fault.Code {
			case "invalid_auth", "not_authed", "token_revoked", "account_inactive", "missing_scope":
				return err
			}
			if fault.RetryAfter > delay {
				delay = fault.RetryAfter
			}
		}
		if s.OnStatus != nil {
			s.OnStatus(ConnectionState{State: "reconnecting", LastError: err.Error()})
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func (s *Slack) receive(ctx context.Context, accept func(context.Context, Incoming) error) error {
	var opened struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		URL   string `json:"url"`
	}
	if err := s.call(ctx, "apps.connections.open", s.AppToken, struct{}{}, &opened, false); err != nil {
		return err
	}
	if opened.URL == "" {
		return &SlackError{Code: "invalid_socket_response"}
	}
	// The URL is a short-lived credential; never include a dial/read error in logs.
	conn, _, err := websocket.Dial(ctx, opened.URL, &websocket.DialOptions{HTTPClient: s.HTTPClient})
	if err != nil {
		return &SlackError{Code: "socket_unavailable"}
	}
	defer conn.CloseNow()
	if s.OnStatus != nil {
		s.OnStatus(ConnectionState{State: "connected", ConnectedAt: time.Now().UTC()})
	}
	conn.SetReadLimit(1 << 20)
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return &SlackError{Code: "socket_disconnected"}
		}
		var envelope struct {
			EnvelopeID string `json:"envelope_id"`
			Type       string `json:"type"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return &SlackError{Code: "invalid_envelope"}
		}
		if envelope.Type == "disconnect" {
			return &SlackError{Code: "socket_disconnected"}
		}
		if envelope.EnvelopeID == "" {
			continue
		}
		incoming, accepted, err := slackIncoming(raw)
		if err != nil {
			return err
		}
		if accepted {
			if err = accept(ctx, incoming); err != nil {
				return &SlackError{Code: "inbox_unavailable"}
			}
		}
		if err = wsjson.Write(ctx, conn, map[string]string{"envelope_id": envelope.EnvelopeID}); err != nil {
			return &SlackError{Code: "ack_unconfirmed"}
		}
	}
}

func slackIncoming(raw []byte) (Incoming, bool, error) {
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			TenantID string `json:"team_id"`
			AppID    string `json:"api_app_id"`
			EventID  string `json:"event_id"`
			Event    struct {
				Type     string `json:"type"`
				Subtype  string `json:"subtype"`
				BotID    string `json:"bot_id"`
				User     string `json:"user"`
				Channel  string `json:"channel"`
				TS       string `json:"ts"`
				ThreadID string `json:"thread_ts"`
				Text     string `json:"text"`
				Files    []struct {
					ID         string `json:"id"`
					Name       string `json:"name"`
					MIMEType   string `json:"mimetype"`
					Size       int64  `json:"size"`
					URL        string `json:"url_private_download"`
					PrivateURL string `json:"url_private"`
				} `json:"files"`
			} `json:"event"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return Incoming{}, false, &SlackError{Code: "invalid_envelope"}
	}
	p := envelope.Payload
	e := p.Event
	if envelope.Type != "events_api" || (e.Type != "message" && e.Type != "app_mention") || (e.Subtype != "" && e.Subtype != "file_share") || e.BotID != "" {
		return Incoming{}, false, nil
	}
	if p.TenantID == "" || p.EventID == "" || e.User == "" || e.Channel == "" || e.TS == "" {
		return Incoming{}, false, &SlackError{Code: "invalid_message_identity"}
	}
	var attachments []Attachment
	for _, f := range e.Files {
		if f.ID == "" {
			continue
		}
		u := f.URL
		if u == "" {
			u = f.PrivateURL
		}
		attachments = append(attachments, Attachment{ID: f.ID, MessageID: e.TS, Kind: "file", Name: f.Name, MIMEType: f.MIMEType, Size: f.Size, URL: u})
	}
	if strings.TrimSpace(e.Text) == "" && len(attachments) == 0 {
		return Incoming{}, false, nil
	}
	if e.ThreadID == "" {
		e.ThreadID = e.TS
	}
	return Incoming{Platform: "slack", AppID: p.AppID, Attachments: attachments, TenantID: p.TenantID, SenderID: e.User, ConversationID: e.Channel, ThreadID: e.ThreadID, MessageID: e.TS, EventID: p.EventID, Text: e.Text}, true, nil
}

// Post sends once. ClientMessageID must remain stable across reconciliation,
// but it is not treated as a provider guarantee of exactly-once delivery.
func (s *Slack) Post(ctx context.Context, message Outgoing) (Receipt, error) {
	if s.BotToken == "" || message.ConversationID == "" || message.ThreadID == "" || message.ClientMessageID == "" || strings.TrimSpace(message.Text) == "" || len(message.Attachments) > 0 {
		return Receipt{}, &SlackError{Code: "invalid_message"}
	}
	var result struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	payload := map[string]any{"channel": message.ConversationID, "thread_ts": message.ThreadID, "text": message.Text, "client_msg_id": message.ClientMessageID}
	if err := s.call(ctx, "chat.postMessage", s.BotToken, payload, &result, true); err != nil {
		return Receipt{}, err
	}
	if result.Channel != message.ConversationID || result.TS == "" {
		return Receipt{}, &SlackError{Code: "invalid_receipt", Uncertain: true}
	}
	return Receipt{ConversationID: result.Channel, MessageID: result.TS}, nil
}

func (s *Slack) call(ctx context.Context, method, token string, input, output any, write bool) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	base := s.APIURL
	if base == "" {
		base = "https://slack.com/api"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return &SlackError{Code: "invalid_endpoint"}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return &SlackError{Code: "transport_error", Uncertain: write}
	}
	defer response.Body.Close()
	retry, _ := strconv.Atoi(response.Header.Get("Retry-After"))
	if retry < 0 {
		retry = 0
	}
	if retry > 86400 {
		retry = 86400
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return &SlackError{Code: "ratelimited", RetryAfter: time.Duration(retry) * time.Second}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &SlackError{Code: "http_error", Uncertain: write && response.StatusCode >= 500}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return &SlackError{Code: "invalid_response", Uncertain: write}
	}
	var status struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &status) != nil {
		return &SlackError{Code: "invalid_response", Uncertain: write}
	}
	if !status.OK {
		code := status.Error
		if code == "" {
			code = "invalid_response"
		}
		// Slack documents that these failures may follow a partially applied write.
		uncertain := write && (code == "internal_error" || code == "fatal_error" || code == "request_timeout" || code == "service_unavailable" || code == "invalid_response" || strings.HasPrefix(code, "duplicate_"))
		return &SlackError{Code: code, RetryAfter: time.Duration(retry) * time.Second, Uncertain: uncertain}
	}
	if json.Unmarshal(body, output) != nil {
		return &SlackError{Code: "invalid_response", Uncertain: write}
	}
	return nil
}

// SlackIdentity is resolved from Slack, not from configuration or message text.
type SlackIdentity struct {
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
	BotID  string `json:"bot_id"`
}

func (s *Slack) Identity(ctx context.Context) (SlackIdentity, error) {
	var result SlackIdentity
	if s.BotToken == "" {
		return result, &SlackError{Code: "configuration_required"}
	}
	if err := s.call(ctx, "auth.test", s.BotToken, struct{}{}, &result, false); err != nil {
		return result, err
	}
	if result.TeamID == "" || result.UserID == "" || result.BotID == "" {
		return result, &SlackError{Code: "bot_identity_required"}
	}
	return result, nil
}

// ValidateConversation verifies that the bot can access the selected destination.
func (s *Slack) ValidateConversation(ctx context.Context, id string) error {
	var result struct {
		Channel struct {
			ID         string `json:"id"`
			IsIM       bool   `json:"is_im"`
			IsMember   bool   `json:"is_member"`
			IsArchived bool   `json:"is_archived"`
		} `json:"channel"`
	}
	if id == "" {
		return &SlackError{Code: "conversation_required"}
	}
	if err := s.call(ctx, "conversations.info", s.BotToken, map[string]string{"channel": id}, &result, false); err != nil {
		return err
	}
	if result.Channel.ID != id || result.Channel.IsArchived || (!result.Channel.IsIM && !result.Channel.IsMember) {
		return &SlackError{Code: "conversation_unavailable"}
	}
	return nil
}

// Download retrieves a source-authenticated Slack file. The bound ingress event
// supplies the attachment; arbitrary URLs must not be admitted as attachments.
func (s *Slack) Download(ctx context.Context, attachment Attachment) ([]byte, error) {
	const maxFileBytes = 32 << 20
	if s.BotToken == "" || attachment.ID == "" || !s.fileURL(attachment.URL) || attachment.Size > maxFileBytes {
		return nil, &SlackError{Code: "invalid_attachment"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, attachment.URL, nil)
	if err != nil {
		return nil, &SlackError{Code: "invalid_attachment"}
	}
	request.Header.Set("Authorization", "Bearer "+s.BotToken)
	client := http.Client{Timeout: 60 * time.Second}
	if s.HTTPClient != nil {
		client = *s.HTTPClient
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !s.fileURL(req.URL.String()) {
			return errors.New("invalid file redirect")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &SlackError{Code: "download_failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &SlackError{Code: "download_failed"}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFileBytes+1))
	if err != nil || len(body) > maxFileBytes {
		return nil, &SlackError{Code: "attachment_too_large_or_incomplete"}
	}
	return body, nil
}

func (s *Slack) fileURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return false
	}
	if s.APIURL != "" {
		base, err := url.Parse(s.APIURL)
		if err == nil && u.Scheme == base.Scheme && u.Host == base.Host {
			return true
		}
	}
	return u.Scheme == "https" && (u.Host == "files.slack.com" || u.Host == "files.slack-files.com")
}

// Upload performs Slack's external upload sequence and shares exactly one file.
// The caller records a separate file outbox effect before invoking it. If the
// final sharing request is uncertain, do not restart the upload sequence.
func (s *Slack) Upload(ctx context.Context, message Outgoing, name string, content []byte) (Receipt, error) {
	if s.BotToken == "" || message.ConversationID == "" || message.ThreadID == "" || strings.TrimSpace(name) == "" || len(content) > 32<<20 {
		return Receipt{}, &SlackError{Code: "invalid_attachment"}
	}
	var opened struct {
		URL    string `json:"upload_url"`
		FileID string `json:"file_id"`
	}
	if err := s.call(ctx, "files.getUploadURLExternal", s.BotToken, map[string]any{"filename": name, "length": len(content)}, &opened, false); err != nil {
		return Receipt{}, err
	}
	if opened.FileID == "" || !s.fileURL(opened.URL) {
		return Receipt{}, &SlackError{Code: "invalid_upload_response"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, opened.URL, bytes.NewReader(content))
	if err != nil {
		return Receipt{}, &SlackError{Code: "invalid_upload_response"}
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	client := http.Client{Timeout: 60 * time.Second}
	if s.HTTPClient != nil {
		client = *s.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return Receipt{}, &SlackError{Code: "upload_failed"}
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Receipt{}, &SlackError{Code: "upload_failed"}
	}
	var completed struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	input := map[string]any{"files": []map[string]string{{"id": opened.FileID, "title": name}}, "channel_id": message.ConversationID, "thread_ts": message.ThreadID}
	if message.Text != "" {
		input["initial_comment"] = message.Text
	}
	if err = s.call(ctx, "files.completeUploadExternal", s.BotToken, input, &completed, true); err != nil {
		return Receipt{ConversationID: message.ConversationID, AttachmentID: opened.FileID}, err
	}
	if len(completed.Files) != 1 || completed.Files[0].ID != opened.FileID {
		return Receipt{ConversationID: message.ConversationID, AttachmentID: opened.FileID}, &SlackError{Code: "invalid_receipt", Uncertain: true}
	}
	return Receipt{ConversationID: message.ConversationID, AttachmentID: opened.FileID}, nil
}

// FindReceipt looks only in the original destination thread. A match establishes
// delivery; absence does not establish non-delivery or authorize another send.
// Slack may deny thread reads for the installed bot's conversation scopes.
func (s *Slack) FindReceipt(ctx context.Context, message Outgoing) (Receipt, bool, error) {
	if s.BotToken == "" || message.ConversationID == "" || message.ThreadID == "" || message.ClientMessageID == "" {
		return Receipt{}, false, &SlackError{Code: "invalid_message"}
	}
	identity, err := s.Identity(ctx)
	if err != nil {
		return Receipt{}, false, err
	}
	cursor := ""
	for {
		var result struct {
			Messages []struct {
				TS       string `json:"ts"`
				ClientID string `json:"client_msg_id"`
				BotID    string `json:"bot_id"`
				Text     string `json:"text"`
			} `json:"messages"`
			Metadata struct {
				Next string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		if err := s.call(ctx, "conversations.replies", s.BotToken, map[string]any{"channel": message.ConversationID, "ts": message.ThreadID, "cursor": cursor, "limit": 100}, &result, false); err != nil {
			return Receipt{}, false, err
		}
		for _, m := range result.Messages {
			if m.ClientID == message.ClientMessageID && m.TS != "" && m.BotID == identity.BotID && m.Text == message.Text {
				return Receipt{ConversationID: message.ConversationID, MessageID: m.TS}, true, nil
			}
		}
		if result.Metadata.Next == "" {
			return Receipt{}, false, nil
		}
		if result.Metadata.Next == cursor {
			return Receipt{}, false, &SlackError{Code: "invalid_cursor"}
		}
		cursor = result.Metadata.Next
	}
}
