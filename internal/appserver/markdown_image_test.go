package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testReplyImage(t *testing.T, path string) string {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			picture.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x * y), 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func TestReplyImagesResolveOnlyVisibleMarkdownAndVerifyChangingOriginals(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	client := &rpcClient{server: server, out: out}
	filename := "answer (1).png"
	path := filepath.Join(rt.RootDir, filename)
	original := testReplyImage(t, path)
	body := "Result ![preview](<answer (1).png>)\n\n`![code](secret.png)`\n\n[document](private.png)"
	th := newThreadState("images", nil, "", "", rt.RootDir, false, time.Now())
	th.Turns = []Turn{{ID: "turn", Items: []ThreadItem{{ID: "answer", Type: ThreadItemAgentMessage, Text: body}}}}
	server.threads[th.ID] = th
	projected := RemoteThreadItem(th.ID, "turn", th.Turns[0].Items[0])
	if len(projected.MarkdownImages) != 1 || projected.MarkdownImages[0].Data != "" {
		t.Fatalf("unexpected image projection: %+v", projected.MarkdownImages)
	}
	params := map[string]any{"kind": "thread", "scope_id": th.ID, "turn_id": "turn", "message_id": "answer", "source": filename, "preview": true}
	var preview attachmentChunk
	client.rpc(t, "message/image/read", params, &preview)
	raw, err := base64.StdEncoding.DecodeString(preview.Data)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != 384 || config.Height != 192 || preview.MediaType != "image/jpeg" {
		t.Fatalf("invalid thumbnail: %+v %v", config, err)
	}
	params["preview"] = false
	var first attachmentChunk
	client.rpc(t, "message/image/read", params, &first)
	if first.Data != original[:min(len(original), 128*1024)] || len(first.SHA256) != 64 {
		t.Fatal("original bytes or digest missing")
	}
	assertError := func(params map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(params)
		if err := server.handleMarkdownImageRead(context.Background(), Request{ID: json.RawMessage(`"rejected"`), Params: raw}); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		var result struct {
			Error *ResponseError `json:"error"`
		}
		_ = json.Unmarshal([]byte(lines[len(lines)-1]), &result)
		if result.Error == nil {
			t.Fatal("unauthorized or changed image was served")
		}
	}
	for _, source := range []string{"secret.png", "private.png", "https://example.test/remote.png", filepath.Join(rt.RootDir, "other.png")} {
		p := map[string]any{"kind": "thread", "scope_id": th.ID, "turn_id": "turn", "message_id": "answer", "source": source}
		assertError(p)
	}
	params["sha256"] = first.SHA256
	// A valid but different image at the same declared path cannot mix chunks.
	var replacement bytes.Buffer
	_ = png.Encode(&replacement, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	_ = os.WriteFile(path, replacement.Bytes(), 0600)
	assertError(params)
}

func TestMarkdownImageReferencesExcludeCodeAndExternalRequests(t *testing.T) {
	body := "![one][img]\n\n[img]: <folder/a (1).png> \"caption\"\n\n```\n![no](secret.png)\n```\n\n![tracking](https://example.test/p.png)\n\n\\![escaped](private.png)"
	images := markdownImageReferences("thread", "t", "turn", "item", body)
	if len(images) != 1 {
		t.Fatalf("unexpected image references: %+v", images)
	}
}
