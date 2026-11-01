package personalim

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// Feishu uses the official authenticated long connection: it opens no listening
// port. Credentials and the expected tenant come from host configuration.
// Construct a new instance when credentials change; do not mutate a running one.
type Feishu struct {
	AppID, AppSecret, TenantID string
	APIURL                     string
	HTTPClient                 *http.Client
	OnStatus                   func(ConnectionState)
	mu                         sync.Mutex
	token                      string
	tokenUntil                 time.Time
}

// FeishuError distinguishes a rejected request from a write whose receipt was
// lost. Feishu deduplicates message UUIDs for one hour only; an uncertain write
// must not be retried indefinitely, even with its original ClientMessageID.
type FeishuError struct {
	Code                 string
	Retryable, Uncertain bool
	RetryAfter           time.Duration
}

func (e *FeishuError) Error() string { return "feishu: " + e.Code }

func (f *Feishu) status(state string) {
	if f.OnStatus != nil {
		s := ConnectionState{State: state}
		if state == "connected" {
			s.ConnectedAt = time.Now()
		}
		f.OnStatus(s)
	}
}
func (f *Feishu) base() string {
	if f.APIURL != "" {
		return strings.TrimRight(f.APIURL, "/")
	}
	return "https://open.feishu.cn"
}
func (f *Feishu) httpClient() *http.Client {
	// Never forward bearer credentials on redirects, including attachment reads.
	c := http.Client{Timeout: 30 * time.Second}
	if f.HTTPClient != nil {
		c = *f.HTTPClient
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}
func (f *Feishu) Run(ctx context.Context, accept func(context.Context, Incoming) error) error {
	if f.AppID == "" || f.AppSecret == "" || f.TenantID == "" || accept == nil {
		return errors.New("feishu: app credentials, tenant and durable input handler required")
	}
	f.status("connecting")
	client := larkws.NewClient(f.AppID, f.AppSecret, larkws.WithDomain(f.base()), larkws.WithHttpClient(f.httpClient()), larkws.WithLogLevel(larkcore.LogLevelError), larkws.WithEventHandler(f.dispatcher(accept)),
		larkws.WithOnReady(func() { f.status("connected") }), larkws.WithOnReconnected(func() { f.status("connected") }), larkws.WithOnReconnecting(func() { f.status("reconnecting") }), larkws.WithOnDisconnected(func() { f.status("disconnected") }))
	err := client.Start(ctx)
	if ctx.Err() != nil {
		f.status("stopped")
		return ctx.Err()
	}
	if err != nil {
		f.status("failed")
		return errors.New("feishu: long connection failed; verify credentials and event subscription")
	}
	f.status("stopped")
	return nil
}
func (f *Feishu) dispatcher(accept func(context.Context, Incoming) error) *dispatcher.EventDispatcher {
	return dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(ctx context.Context, e *larkim.P2MessageReceiveV1) error {
		m, ok, err := f.incoming(e)
		if err != nil || !ok {
			return err
		}
		// The SDK sends its success frame only after this callback returns nil.
		return accept(ctx, m)
	})
}
func feishuString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (f *Feishu) incoming(e *larkim.P2MessageReceiveV1) (Incoming, bool, error) {
	if e == nil || e.EventV2Base == nil || e.EventV2Base.Header == nil || e.Event == nil || e.Event.Sender == nil || e.Event.Message == nil {
		return Incoming{}, false, errors.New("feishu: incomplete event")
	}
	h, s, m := e.EventV2Base.Header, e.Event.Sender, e.Event.Message
	if h.AppID != f.AppID || h.TenantKey == "" || (f.TenantID != "" && h.TenantKey != f.TenantID) || feishuString(s.TenantKey) != h.TenantKey {
		return Incoming{}, false, errors.New("feishu: event identity mismatch")
	}
	if feishuString(s.SenderType) != "user" {
		return Incoming{}, false, nil
	}
	if s.SenderId == nil {
		return Incoming{}, false, errors.New("feishu: missing sender")
	}
	in := Incoming{Platform: "feishu", AppID: h.AppID, TenantID: h.TenantKey, SenderID: feishuString(s.SenderId.OpenId), ConversationID: feishuString(m.ChatId), MessageID: feishuString(m.MessageId), EventID: h.EventID, ThreadID: feishuString(m.RootId)}
	if in.ThreadID == "" {
		in.ThreadID = in.MessageID
	}
	if in.EventID == "" || in.SenderID == "" || in.ConversationID == "" || in.MessageID == "" {
		return Incoming{}, false, errors.New("feishu: missing event provenance")
	}
	var content struct {
		Text     string `json:"text"`
		ImageKey string `json:"image_key"`
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
	}
	if err := json.Unmarshal([]byte(feishuString(m.Content)), &content); err != nil {
		return Incoming{}, false, errors.New("feishu: malformed message content")
	}
	switch feishuString(m.MessageType) {
	case "text":
		in.Text = content.Text
	case "post":
		var post struct {
			Title   string `json:"title"`
			Content [][]struct {
				Tag      string `json:"tag"`
				Text     string `json:"text"`
				Href     string `json:"href"`
				ImageKey string `json:"image_key"`
			} `json:"content"`
		}
		if err := json.Unmarshal([]byte(feishuString(m.Content)), &post); err != nil {
			return Incoming{}, false, errors.New("feishu: malformed rich message")
		}
		var lines []string
		if post.Title != "" {
			lines = append(lines, post.Title)
		}
		for _, row := range post.Content {
			var line strings.Builder
			for _, item := range row {
				switch item.Tag {
				case "text", "a", "at":
					line.WriteString(item.Text)
					if item.Tag == "a" && item.Href != "" {
						line.WriteString(" (" + item.Href + ")")
					}
				case "img":
					if item.ImageKey != "" {
						in.Attachments = append(in.Attachments, Attachment{ID: item.ImageKey, MessageID: in.MessageID, Kind: "image"})
					}
				}
			}
			if line.Len() > 0 {
				lines = append(lines, line.String())
			}
		}
		in.Text = strings.Join(lines, "\n")
	case "image":
		if content.ImageKey == "" {
			return Incoming{}, false, errors.New("feishu: missing image key")
		}
		in.Attachments = []Attachment{{ID: content.ImageKey, MessageID: in.MessageID, Kind: "image"}}
	case "file", "audio", "media":
		if content.FileKey == "" {
			return Incoming{}, false, errors.New("feishu: missing file key")
		}
		in.Attachments = []Attachment{{ID: content.FileKey, MessageID: in.MessageID, Kind: "file", Name: content.FileName}}
	default:
		return Incoming{}, false, nil
	}
	return in, true, nil
}

func (f *Feishu) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.tokenUntil) {
		return f.token, nil
	}
	if f.AppID == "" || f.AppSecret == "" {
		return "", errors.New("feishu: missing app credentials")
	}
	payload, _ := json.Marshal(map[string]string{"app_id": f.AppID, "app_secret": f.AppSecret})
	raw, err := f.request(ctx, "POST", "/open-apis/auth/v3/tenant_access_token/internal", "application/json", bytes.NewReader(payload), "", false)
	if err != nil {
		return "", err
	}
	var result struct {
		Token  string `json:"tenant_access_token"`
		Expire int    `json:"expire"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Token == "" || result.Expire <= 0 {
		return "", errors.New("feishu: invalid credential response")
	}
	f.token = result.Token
	f.tokenUntil = time.Now().Add(time.Duration(result.Expire)*time.Second - time.Minute)
	return f.token, nil
}
func (f *Feishu) request(ctx context.Context, method, path, contentType string, body io.Reader, token string, write bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, f.base()+path, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, &FeishuError{Code: "transport", Retryable: !write, Uncertain: write}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &FeishuError{Code: "read_response", Retryable: !write, Uncertain: write}
	}
	seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
	retryAfter := time.Duration(seconds) * time.Second
	var envelope struct {
		Code *int `json:"code"`
	}
	parsed := json.Unmarshal(raw, &envelope) == nil && envelope.Code != nil
	if parsed && *envelope.Code != 0 {
		code := *envelope.Code
		if code == 99991663 && token != "" {
			f.mu.Lock()
			f.token = ""
			f.mu.Unlock()
		}
		return nil, &FeishuError{Code: strconv.Itoa(code), Retryable: code == 99991400 || code == 230020 || code == 99991663 || (!write && resp.StatusCode >= 500), Uncertain: write && (code == 99991500 || resp.StatusCode >= 500), RetryAfter: retryAfter}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &FeishuError{Code: fmt.Sprintf("http_%d", resp.StatusCode), Retryable: resp.StatusCode == 429 || (!write && resp.StatusCode >= 500), Uncertain: write && resp.StatusCode >= 500, RetryAfter: retryAfter}
	}
	if !parsed {
		return nil, &FeishuError{Code: "invalid_response", Uncertain: write}
	}
	return raw, nil
}
func (f *Feishu) api(ctx context.Context, method, path string, body any, write bool) ([]byte, error) {
	token, err := f.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	return f.request(ctx, method, path, "application/json", reader, token, write)
}

// Test checks bot credentials only. It does not prove that any sender is the owner.
func (f *Feishu) Test(ctx context.Context) error {
	_, err := f.api(ctx, "GET", "/open-apis/bot/v3/info", nil, false)
	return err
}

// ValidateBinding verifies that the app can resolve the configured native user
// and conversation. Owner authorization remains an explicit host-side binding.
func (f *Feishu) ValidateBinding(ctx context.Context, tenant, sender, conversation string) error {
	if tenant == "" || (f.TenantID != "" && tenant != f.TenantID) || sender == "" || conversation == "" {
		return errors.New("feishu: incomplete or mismatched binding")
	}
	raw, err := f.api(ctx, "GET", "/open-apis/contact/v3/users/"+url.PathEscape(sender)+"?user_id_type=open_id", nil, false)
	if err != nil {
		return err
	}
	var user struct {
		Data struct {
			User struct {
				OpenID string `json:"open_id"`
			} `json:"user"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &user) != nil || user.Data.User.OpenID != sender {
		return errors.New("feishu: user identity not verified")
	}
	_, err = f.api(ctx, "GET", "/open-apis/im/v1/chats/"+url.PathEscape(conversation), nil, false)
	return err
}
func (f *Feishu) Post(ctx context.Context, m Outgoing) (Receipt, error) {
	if m.ConversationID == "" || m.ClientMessageID == "" {
		return Receipt{}, errors.New("feishu: destination and stable message ID required")
	}
	if len(m.Attachments) > 1 || (len(m.Attachments) > 0 && m.Text != "") {
		return Receipt{}, errors.New("feishu: text and each attachment require separate durable messages")
	}
	kind := "text"
	content := map[string]string{"text": m.Text}
	if len(m.Attachments) == 1 {
		a := m.Attachments[0]
		kind = a.Kind
		if a.ID == "" || (kind != "image" && kind != "file") {
			return Receipt{}, errors.New("feishu: invalid uploaded attachment")
		}
		key := "file_key"
		if kind == "image" {
			key = "image_key"
		}
		content = map[string]string{key: a.ID}
	}
	encoded, _ := json.Marshal(content)
	uuid := m.ClientMessageID
	if len(uuid) > 50 {
		digest := sha256.Sum256([]byte(uuid))
		uuid = base64.RawURLEncoding.EncodeToString(digest[:])
	}
	body := map[string]any{"msg_type": kind, "content": string(encoded), "uuid": uuid}
	path := "/open-apis/im/v1/messages?receive_id_type=chat_id"
	if m.ThreadID != "" {
		path = "/open-apis/im/v1/messages/" + url.PathEscape(m.ThreadID) + "/reply"
		body["reply_in_thread"] = true
	} else {
		body["receive_id"] = m.ConversationID
	}
	raw, err := f.api(ctx, "POST", path, body, true)
	if err != nil {
		return Receipt{}, err
	}
	var result struct {
		Data struct {
			MessageID string `json:"message_id"`
			ChatID    string `json:"chat_id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Data.MessageID == "" || result.Data.ChatID != m.ConversationID {
		return Receipt{}, &FeishuError{Code: "invalid_receipt", Uncertain: true}
	}
	return Receipt{ConversationID: result.Data.ChatID, MessageID: result.Data.MessageID}, nil
}

const feishuMediaLimit = 32 << 20

// Download never follows an attachment-supplied URL. Resources are fetched only
// from the configured provider endpoint, scoped to their original message.
func (f *Feishu) Download(ctx context.Context, a Attachment) ([]byte, error) {
	if a.ID == "" || a.MessageID == "" || (a.Kind != "image" && a.Kind != "file") {
		return nil, errors.New("feishu: missing resource provenance")
	}
	token, err := f.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	path := "/open-apis/im/v1/messages/" + url.PathEscape(a.MessageID) + "/resources/" + url.PathEscape(a.ID) + "?type=" + a.Kind
	req, err := http.NewRequestWithContext(ctx, "GET", f.base()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, &FeishuError{Code: "download_transport", Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &FeishuError{Code: fmt.Sprintf("download_http_%d", resp.StatusCode), Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, feishuMediaLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > feishuMediaLimit {
		return nil, errors.New("feishu: attachment exceeds 32 MiB")
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		var failure struct {
			Code    *int   `json:"code"`
			Message string `json:"msg"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Code != nil && *failure.Code != 0 && failure.Message != "" {
			return nil, &FeishuError{Code: strconv.Itoa(*failure.Code)}
		}
	}
	return data, nil
}
func (f *Feishu) Upload(ctx context.Context, a Attachment, r io.Reader) (Attachment, error) {
	if a.Kind != "image" && a.Kind != "file" {
		return Attachment{}, errors.New("feishu: unsupported upload kind")
	}
	data, err := io.ReadAll(io.LimitReader(r, feishuMediaLimit+1))
	if err != nil {
		return Attachment{}, err
	}
	if len(data) > feishuMediaLimit {
		return Attachment{}, errors.New("feishu: attachment exceeds 32 MiB")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	field, path := "file", "/open-apis/im/v1/files"
	if a.Kind == "image" {
		field, path = "image", "/open-apis/im/v1/images"
		form.WriteField("image_type", "message")
	} else {
		form.WriteField("file_type", "stream")
		form.WriteField("file_name", a.Name)
	}
	part, err := form.CreateFormFile(field, a.Name)
	if err != nil {
		return Attachment{}, err
	}
	if _, err = part.Write(data); err != nil {
		return Attachment{}, err
	}
	if err = form.Close(); err != nil {
		return Attachment{}, err
	}
	token, err := f.accessToken(ctx)
	if err != nil {
		return Attachment{}, err
	}
	raw, err := f.request(ctx, "POST", path, form.FormDataContentType(), &body, token, true)
	if err != nil {
		return Attachment{}, err
	}
	var result struct {
		Data struct {
			FileKey  string `json:"file_key"`
			ImageKey string `json:"image_key"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return Attachment{}, &FeishuError{Code: "invalid_upload_receipt", Uncertain: true}
	}
	a.ID = result.Data.FileKey
	if a.Kind == "image" {
		a.ID = result.Data.ImageKey
	}
	if a.ID == "" {
		return Attachment{}, &FeishuError{Code: "invalid_upload_receipt", Uncertain: true}
	}
	a.Size = int64(len(data))
	a.URL = ""
	return a, nil
}
