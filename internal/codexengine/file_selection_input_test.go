package codexengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestEngineTransmitsFileSelectionCanonicalText(t *testing.T) {
	binary := buildFakeCodex(t)
	for _, intent := range []string{"comment", "edit", "quote"} {
		for _, ref := range []string{"", "codex-thread-1"} {
			name := intent + "/new"
			if ref != "" {
				name = intent + "/resumed"
			}
			t.Run(name, func(t *testing.T) {
				capture := filepath.Join(t.TempDir(), "input.json")
				t.Setenv("WUU_TEST_CODEX_INPUT", capture)
				host := NewHost(binary, t.TempDir())
				defer host.Release()
				sess, err := NewEngine(host).SessionForThread(context.Background(), agentengine.ThreadBinding{
					ThreadID: t.Name(), RootDir: t.TempDir(), ExternalRef: ref,
				})
				if err != nil {
					t.Fatal(err)
				}
				defer sess.Close(context.Background())
				part := providers.MessageContentPart{
					Type: "file_selection", ID: "selection-1", Intent: intent,
					Source: &providers.FileSelectionSource{
						Workspace: "/workspace", Path: "notes/示例.md",
						StartLine: 2, StartColumn: 1, EndLine: 2, EndColumn: 3,
						Quote: "🙂", Revision: "text-v1:captured",
					},
				}
				if intent != "quote" {
					part.Comment = "Consider this selection.\nKeep `quoted` context intact."
				}
				// The client owns serialization. Include an English task wrapper and
				// JSON reference data, with Unicode and whitespace preserved on the wire.
				reference, err := json.MarshalIndent(part, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				part.Text = "Use the attached file selection.\nReference data:\n" + string(reference) + "\n\n"
				prefix, suffix := "\nPlease review:\n", "Additional reference\n"
				prompt := prefix + part.Text + suffix
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, err = sess.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{
					{Role: "user", Content: "Earlier context already belongs to the native thread."},
					{Role: "assistant", Content: "Previous response"},
					{Role: "user", Content: prompt, ContentParts: []providers.MessageContentPart{
						{Type: "text", Text: prefix}, part, {Type: "pasted_text", Text: suffix},
					}},
				}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				var request struct {
					Input []map[string]any `json:"input"`
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				if len(request.Input) != 1 || request.Input[0]["type"] != "text" || request.Input[0]["text"] != prompt {
					t.Fatalf("Codex input did not preserve the complete canonical text: %s", raw)
				}
				if _, present := request.Input[0]["text_elements"]; present {
					t.Fatalf("file-selection context must be self-contained in ordinary text: %s", raw)
				}
			})
		}
	}
}
