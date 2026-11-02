package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// The rendered production contribution talks to a real UserHost and executes a
// real shell effect in a temporary workspace. Navigation and model are fixtures.
func TestPersonalApprovalBrowserToNativeEffect(t *testing.T) {
	if os.Getenv("WUU_PERSONAL_BROWSER_E2E") != "1" {
		t.Skip("set WUU_PERSONAL_BROWSER_E2E=1 after building the Personal fixture")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(root, "desktop/out/personal-fixture/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("WUU_APPROVAL_NATIVE_OUTPUT")
	if output == "" {
		output = filepath.Join(root, "desktop/out/personal-approval-native")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	f := newPersonalBoundaryFixture(t, nil)
	f.setReviewMode(false)
	f.sourceSeq, err = f.rt.Sessions.AppendHistoryRecordReturningSeq(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "请在文件中记录一次确认结果。执行前先让我确认。"})
	if err != nil {
		t.Fatal(err)
	}
	call := providers.ToolCall{ID: "browser-command", Name: "bash", Arguments: `{"command":"printf once >> browser-marker.txt"}`}
	if _, err = f.kit.Execute(f.sourceContext(), call); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
		t.Fatalf("command did not wait: %v", err)
	}
	var handler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(html)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/native-effect" {
			data, readErr := os.ReadFile(filepath.Join(f.rt.RootDir, "browser-marker.txt"))
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				http.Error(w, readErr.Error(), 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": string(data)})
			return
		}
		if r.URL.Path != "/native-review" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		var input struct {
			Method string          `json:"method"`
			Input  json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&input); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if !strings.HasPrefix(input.Method, "review.") {
			http.Error(w, "unsupported test operation", 400)
			return
		}
		if len(input.Input) == 0 {
			input.Input = json.RawMessage(`{}`)
		}
		result, err := f.host.personal.Call(f.owner, input.Method, input.Input, "")
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "value": result})
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "desktop/scripts/personal-approval-native-e2e.cjs"), server.URL, output)
	command.Dir = root
	combined, err := command.CombinedOutput()
	if writeErr := os.WriteFile(filepath.Join(output, "browser.log"), combined, 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil {
		t.Fatalf("browser chain: %v\n%s", err, combined)
	}
	data, err := os.ReadFile(filepath.Join(f.rt.RootDir, "browser-marker.txt"))
	if err != nil || string(data) != "once" {
		t.Fatalf("wrong native effect: %q %v", data, err)
	}
	t.Logf("PASS production UI → authenticated owner API → saved rule → exact approval → real shell effect once; artifacts %s", output)
}
