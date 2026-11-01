package personalim

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mediaDownloader struct {
	data  []byte
	err   error
	calls int
}

func (d *mediaDownloader) Download(context.Context, Attachment) ([]byte, error) {
	d.calls++
	return d.data, d.err
}

func TestMediaAdmissionCarriesExactBytesAndDigest(t *testing.T) {
	d := &mediaDownloader{data: []byte("hello")}
	files, err := DownloadInputAttachments(context.Background(), d, []Attachment{{ID: "provider-key", Name: "note.txt", MIMEType: "text/plain"}})
	if err != nil || len(files) != 1 {
		t.Fatalf("%+v %v", files, err)
	}
	sum := sha256.Sum256(d.data)
	if files[0].Data != base64.StdEncoding.EncodeToString(d.data) || files[0].SHA256 != hex.EncodeToString(sum[:]) || files[0].Filename != "note.txt" {
		t.Fatalf("incorrect admitted bytes: %+v", files)
	}
	d.err = errors.New("provider download failed")
	if _, err = DownloadInputAttachments(context.Background(), d, []Attachment{{ID: "a"}}); err == nil {
		t.Fatal("download failure swallowed")
	}
}
func TestMediaAdmissionRejectsUnsafeNamesAndLimits(t *testing.T) {
	for _, name := range []string{"../../credentials", "x\\secret", "bad\nname"} {
		d := &mediaDownloader{data: []byte("x")}
		if _, err := DownloadInputAttachments(context.Background(), d, []Attachment{{ID: "key", Name: name}}); err == nil {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
	d := &mediaDownloader{data: make([]byte, 20*1024*1024+1)}
	if _, err := DownloadInputAttachments(context.Background(), d, []Attachment{{ID: "key"}}); err == nil {
		t.Fatal("unbounded input")
	}
}

type attachmentClient struct {
	data   []byte
	calls  int
	tamper bool
}

func (c *attachmentClient) Close() error { return nil }
func (c *attachmentClient) Call(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method != "agents.attachments.read" {
		return nil, errors.New("unexpected method")
	}
	var p struct {
		Agent  string `json:"agent_id"`
		Seq    int    `json:"seq"`
		Offset int    `json:"offset"`
		Hash   string `json:"sha256"`
	}
	json.Unmarshal(raw, &p)
	if p.Agent != "agent" || p.Seq != 5 {
		return nil, errors.New("unscoped attachment read")
	}
	encoded := base64.StdEncoding.EncodeToString(c.data)
	end := p.Offset + 4
	if end > len(encoded) {
		end = len(encoded)
	}
	hash := p.Hash
	if c.tamper {
		hash = "wrong"
	}
	c.calls++
	return json.Marshal(map[string]any{"data": encoded[p.Offset:end], "offset": p.Offset, "total": len(encoded), "sha256": hash, "name": "answer.txt", "content_type": "text/plain"})
}
func TestMediaOutputReadsPinnedChunksAndRejectsMutation(t *testing.T) {
	c := &attachmentClient{data: []byte("verified output")}
	hash := sha256.Sum256(c.data)
	ref := HistoryAttachment{Kind: "artifact", Index: 0, Name: "answer.txt", MIMEType: "text/plain", Size: int64(len(c.data)), SHA256: hex.EncodeToString(hash[:])}
	meta, data, err := ReadHistoryAttachment(context.Background(), c, "agent", 5, ref)
	if err != nil || string(data) != string(c.data) || meta.Name != "answer.txt" || c.calls < 2 {
		t.Fatalf("%+v %s %v", meta, data, err)
	}
	c.tamper = true
	if _, _, err = ReadHistoryAttachment(context.Background(), c, "agent", 5, ref); err == nil {
		t.Fatal("accepted changed attachment")
	}
}

func TestMediaFeishuPreparationDoesNotSend(t *testing.T) {
	sends := 0
	uploads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			w.Write([]byte(`{"code":0,"tenant_access_token":"token","expire":7200}`))
		case "/open-apis/im/v1/files":
			uploads++
			w.Write([]byte(`{"code":0,"data":{"file_key":"uploaded"}}`))
		case "/open-apis/im/v1/messages":
			sends++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["msg_type"] != "file" || body["content"] != `{"file_key":"uploaded"}` {
				t.Error("lost prepared resource")
			}
			w.Write([]byte(`{"code":0,"data":{"message_id":"sent","chat_id":"chat"}}`))
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	driver := &feishuDriver{Feishu: &Feishu{AppID: "app", AppSecret: "secret", APIURL: server.URL, HTTPClient: server.Client()}}
	c := &attachmentClient{data: []byte("verified output")}
	sum := sha256.Sum256(c.data)
	ref := HistoryAttachment{Kind: "artifact", Index: 0, Name: "answer.txt", MIMEType: "text/plain", Size: int64(len(c.data)), SHA256: hex.EncodeToString(sum[:])}
	a, data, err := PrepareOutputAttachment(context.Background(), driver, c, "agent", 5, ref)
	if err != nil || uploads != 1 || sends != 0 || a.ID != "uploaded" {
		t.Fatalf("prepare sent user message: %+v %v uploads=%d sends=%d", a, err, uploads, sends)
	}
	receipt, err := SendOutputAttachment(context.Background(), driver, Outgoing{ConversationID: "chat", ClientMessageID: "stable"}, a, data)
	if err != nil || sends != 1 || receipt.MessageID != "sent" {
		t.Fatalf("send %+v %v", receipt, err)
	}
}
