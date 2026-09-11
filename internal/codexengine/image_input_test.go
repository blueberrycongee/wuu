package codexengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestEngineTransmitsImages(t *testing.T) {
	binary := buildFakeCodex(t)
	const imageData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII="
	const secondImageData = "/9j/4AAQSkZJRg=="
	for _, tc := range []struct{ name, prompt string }{
		{"text_and_images", "Describe these images"},
		{"image_only", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := tc.prompt
			capture := filepath.Join(t.TempDir(), "input.json")
			t.Setenv("WUU_TEST_CODEX_INPUT", capture)
			host := NewHost(binary, t.TempDir())
			engine := NewEngine(host)
			defer host.Release()
			sess, err := engine.SessionForThread(context.Background(), agentengine.ThreadBinding{
				ThreadID: t.Name(), RootDir: t.TempDir(), Model: "gpt-6-astra",
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = sess.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{
				{Role: "user", Content: "Old prompt must not be replayed", Images: []providers.InputImage{{MediaType: "image/gif", Data: "old"}}},
				{Role: "assistant", Content: "Previous response"},
				{Role: "user", Content: prompt, Images: []providers.InputImage{{MediaType: "image/png", Data: imageData}, {MediaType: "image/jpeg", Data: secondImageData}}},
			}}, func(providers.StreamEvent) {})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var request struct {
				Input []struct {
					Type string `json:"type"`
					Text string `json:"text"`
					URL  string `json:"url"`
				} `json:"input"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			var texts []string
			var images []string
			for _, item := range request.Input {
				switch item.Type {
				case "text":
					texts = append(texts, item.Text)
				case "image":
					images = append(images, item.URL)
				default:
					t.Fatalf("unexpected input type %q", item.Type)
				}
			}
			wantImages := []string{"data:image/png;base64," + imageData, "data:image/jpeg;base64," + secondImageData}
			if !reflect.DeepEqual(images, wantImages) {
				t.Fatalf("images = %v, want %v", images, wantImages)
			}
			var wantTexts []string
			if prompt != "" {
				wantTexts = []string{prompt}
			}
			if !reflect.DeepEqual(texts, wantTexts) {
				t.Fatalf("texts = %v, want %v", texts, wantTexts)
			}
		})
	}
}
