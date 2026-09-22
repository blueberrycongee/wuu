package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(content)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type toolkitFakeClient struct {
	content string
}

func (f *toolkitFakeClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: f.content}, nil
}

func (f *toolkitFakeClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 2)
	if f.content != "" {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: f.content}
	}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

type toolkitNoopExecutor struct{}

func (toolkitNoopExecutor) Definitions() []providers.ToolDefinition { return nil }

func (toolkitNoopExecutor) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", nil
}

type richToolkitTool struct {
	richCalls   int
	legacyCalls int
}

func (t *richToolkitTool) Name() string { return "rich_test" }
func (t *richToolkitTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), InputSchema: map[string]any{"type": "object"}}
}
func (t *richToolkitTool) Execute(context.Context, string) (string, error) {
	t.legacyCalls++
	return "legacy path", nil
}
func (t *richToolkitTool) ExecuteResult(context.Context, string) (toolresult.Result, error) {
	t.richCalls++
	return toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: "text", Text: "rich text"},
			{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"},
		},
		StructuredContent: json.RawMessage(`{"ok":true}`),
		Meta:              json.RawMessage(`{"internal":"kept"}`),
	}, nil
}
func (t *richToolkitTool) IsReadOnly() bool        { return true }
func (t *richToolkitTool) IsConcurrencySafe() bool { return true }

func TestToolkitExecuteResultPrefersRichToolAndKeepsTextAdapter(t *testing.T) {
	root := t.TempDir()
	rich := &richToolkitTool{}
	kit := &Toolkit{
		env:      &Env{RootDir: root},
		registry: NewRegistry(rich),
		boundary: StandardBoundary(),
	}
	call := providers.ToolCall{ID: "call-rich", Name: rich.Name(), Arguments: `{}`}

	result, err := kit.ExecuteResult(context.Background(), call)
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	if rich.richCalls != 1 || rich.legacyCalls != 0 {
		t.Fatalf("rich calls = %d, legacy calls = %d", rich.richCalls, rich.legacyCalls)
	}
	if len(result.Content) != 2 || len(result.StructuredContent) == 0 || len(result.Meta) == 0 {
		t.Fatalf("rich result was flattened: %+v", result)
	}

	text, err := kit.Execute(context.Background(), providers.ToolCall{ID: "call-text", Name: rich.Name(), Arguments: `{"projection":true}`})
	if err != nil {
		t.Fatalf("Execute text adapter: %v", err)
	}
	if text != providers.ProjectToolResult(result).ToolText {
		t.Fatalf("text adapter projection = %q", text)
	}
	if rich.richCalls != 2 || rich.legacyCalls != 0 {
		t.Fatalf("rich calls = %d, legacy calls = %d", rich.richCalls, rich.legacyCalls)
	}
}

func stopToolkitAgentControl(control *agentcontrol.AgentControl) {
	if control == nil {
		return
	}
	control.StopAll()
	time.Sleep(100 * time.Millisecond)
}

func TestToolkit_WriteAndReadFile(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	writeResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"dir/a.txt","content":"hello"}`,
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(writeResp, "written_bytes") {
		t.Fatalf("unexpected write response: %s", writeResp)
	}
	var writeCreated struct {
		WorkspaceRevision string `json:"workspace_revision"`
		NewFileSHA        string `json:"new_file_sha"`
	}
	if err := json.Unmarshal([]byte(writeResp), &writeCreated); err != nil {
		t.Fatalf("parse write response: %v", err)
	}
	if !strings.HasPrefix(writeCreated.WorkspaceRevision, "fs:worktree:") {
		t.Fatalf("write_file response missing filesystem workspace revision: %+v", writeCreated)
	}
	if writeCreated.NewFileSHA != formatFileSHA(sha256Hex([]byte("hello"))) {
		t.Fatalf("write_file response missing new content hash: %+v", writeCreated)
	}

	readResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"dir/a.txt"}`,
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(readResp, "hello") {
		t.Fatalf("unexpected read response: %s", readResp)
	}
	var readParsed struct {
		WorkspaceRevision string `json:"workspace_revision"`
		Content           string `json:"content"`
		Range             struct {
			StartLine int `json:"start_line"`
			EndLine   int `json:"end_line"`
		} `json:"range"`
		OmittedRanges []map[string]int `json:"omitted_ranges"`
		Suggestions   []string         `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(readResp), &readParsed); err != nil {
		t.Fatalf("parse read response: %v", err)
	}
	if !strings.HasPrefix(readParsed.WorkspaceRevision, "fs:worktree:") {
		t.Fatalf("read_file response missing filesystem workspace revision: %+v", readParsed)
	}
	if readParsed.Range.StartLine != 1 || readParsed.Range.EndLine != 1 || len(readParsed.OmittedRanges) != 0 {
		t.Fatalf("unexpected read range metadata: %+v", readParsed)
	}
	if len(readParsed.Suggestions) == 0 || !strings.Contains(strings.Join(readParsed.Suggestions, " "), "excerpt") {
		t.Fatalf("read_file response missing evidence suggestion: %+v", readParsed.Suggestions)
	}

	unchangedResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"dir/a.txt"}`,
	})
	if err != nil {
		t.Fatalf("second read_file: %v", err)
	}
	var unchangedParsed struct {
		WorkspaceRevision string   `json:"workspace_revision"`
		Unchanged         bool     `json:"unchanged"`
		Suggestions       []string `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(unchangedResp), &unchangedParsed); err != nil {
		t.Fatalf("parse unchanged read response: %v", err)
	}
	if !unchangedParsed.Unchanged || unchangedParsed.WorkspaceRevision == "" || len(unchangedParsed.Suggestions) == 0 {
		t.Fatalf("unexpected unchanged read metadata: %+v", unchangedParsed)
	}
}

func TestToolkit_ReadFileAllowsSessionArtifactRefs(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sessionDir := filepath.Join(t.TempDir(), "session-artifacts")
	kit.SetSessionDir(sessionDir)
	artifactPath := filepath.Join(sessionDir, "harness", "artifacts", "worker-1", "result.md")
	mustWriteFile(t, artifactPath, "artifact result\nsecond line\n")

	readResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"$SESSION_DIR/harness/artifacts/worker-1/result.md"}`,
	})
	if err != nil {
		t.Fatalf("read_file session artifact: %v", err)
	}
	var parsed struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(readResp), &parsed); err != nil {
		t.Fatalf("parse read response: %v", err)
	}
	if parsed.Path != "$SESSION_DIR/harness/artifacts/worker-1/result.md" || !strings.Contains(parsed.Content, "artifact result") {
		t.Fatalf("unexpected session artifact read: %+v", parsed)
	}

	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: fmt.Sprintf(`{"path":%q,"content":"bad"}`, artifactPath),
	}); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("write_file should not write session artifacts, got err=%v", err)
	}
}

func TestToolkit_WriteFileOverwritesExistingFilesWithoutPriorRead(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "a.txt"), "old\n")

	writeResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"a.txt","content":"new\n"}`,
	})
	if err != nil {
		t.Fatalf("write_file without prior read: %v", err)
	}
	var writeParsed struct {
		WorkspaceRevision string `json:"workspace_revision"`
	}
	if err := json.Unmarshal([]byte(writeResp), &writeParsed); err != nil {
		t.Fatalf("parse write response: %v", err)
	}
	if !strings.HasPrefix(writeParsed.WorkspaceRevision, "fs:worktree:") {
		t.Fatalf("write_file response missing filesystem workspace revision: %+v", writeParsed)
	}

	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"a.txt","content":"newer\n"}`,
	}); err != nil {
		t.Fatalf("consecutive intentional overwrite: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "newer\n" {
		t.Fatalf("unexpected content after consecutive overwrite: %q", got)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"a.txt","content":"again\n","create_only":true}`,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected create_only to reject existing file, got: %v", err)
	}

	largeContent := strings.Repeat("large existing file line\n", 1800)
	largePath := filepath.Join(root, "large.txt")
	mustWriteFile(t, largePath, largeContent)
	largeReadResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"large.txt","offset":1,"limit":1}`,
	})
	if err != nil {
		t.Fatalf("read large.txt: %v", err)
	}
	_ = largeReadResp
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"large.txt","content":"replacement\n"}`,
	})
	if err == nil ||
		!strings.Contains(err.Error(), "error_kind=broad_overwrite") ||
		!strings.Contains(err.Error(), "overwrite_policy") ||
		!strings.Contains(err.Error(), "scoped file editing tool exposed in this session") {
		t.Fatalf("expected broad overwrite rejection, got: %v", err)
	}
	if got := mustReadFile(t, largePath); got != largeContent {
		t.Fatalf("broad overwrite rejection should not mutate file: got %d bytes", len(got))
	}
	writeLargeResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"large.txt","content":"replacement\n","overwrite_policy":"explicit_user_requested"}`,
	})
	if err != nil {
		t.Fatalf("write_file explicit broad overwrite: %v", err)
	}
	var writeLarge struct {
		Diff DiffResult `json:"diff"`
	}
	if err := json.Unmarshal([]byte(writeLargeResp), &writeLarge); err != nil {
		t.Fatalf("parse broad overwrite response: %v\n%s", err, writeLargeResp)
	}
	if mustReadFile(t, largePath) != "replacement\n" {
		t.Fatalf("explicit broad overwrite did not apply: %+v", writeLarge)
	}
	if !writeLarge.Diff.Truncated || writeLarge.Diff.OldLines <= 0 || writeLarge.Diff.NewLines != 1 || len(writeLarge.Diff.Hunks) != 0 {
		t.Fatalf("broad overwrite should return compact diff summary: %+v", writeLarge.Diff)
	}
}

func TestToolkit_WriteFileRejectsSensitivePaths(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":".env","content":"API_KEY=secret\n"}`,
	})
	if err == nil {
		t.Fatal("expected sensitive path rejection")
	}
	if !strings.Contains(err.Error(), "write_file refuses") || !strings.Contains(err.Error(), "sensitive path") {
		t.Fatalf("expected write_file sensitive path guidance, got: %v", err)
	}
	if strings.Contains(err.Error(), "API_KEY") {
		t.Fatalf("write_file sensitive path error leaked content: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(statErr) {
		t.Fatalf("write_file should not create sensitive file, stat err=%v", statErr)
	}
}

func TestToolkit_ReadFileStreamsLargeFileRange(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var big strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&big, "line-%04d %s\n", i, strings.Repeat("x", 80))
	}
	if big.Len() <= defaultMaxFileBytes {
		t.Fatalf("fixture must exceed max file bytes: got %d", big.Len())
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"big.txt"}`,
	})
	if err == nil {
		t.Fatal("expected no-limit read to reject oversized file")
	}
	if !strings.Contains(err.Error(), "too large") || !strings.Contains(err.Error(), "offset and limit") {
		t.Fatalf("expected oversized guidance, got: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"big.txt","offset":3001,"limit":3}`,
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}

	var parsed struct {
		FileSHA string `json:"file_sha"`
		Content string `json:"content"`
		Range   struct {
			StartLine int `json:"start_line"`
			EndLine   int `json:"end_line"`
		} `json:"range"`
		NumLines      int              `json:"num_lines"`
		StartLine     int              `json:"start_line"`
		TotalLines    int              `json:"total_lines"`
		Truncated     bool             `json:"truncated"`
		OmittedRanges []map[string]int `json:"omitted_ranges"`
		Suggestions   []string         `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if parsed.NumLines != 3 || parsed.StartLine != 3001 || parsed.TotalLines != 5000 || !parsed.Truncated {
		t.Fatalf("unexpected metadata: %+v", parsed)
	}
	if parsed.Range.StartLine != 3001 || parsed.Range.EndLine != 3003 {
		t.Fatalf("unexpected range metadata: %+v", parsed.Range)
	}
	wantOmitted := []map[string]int{
		{"start_line": 1, "end_line": 3000},
		{"start_line": 3004, "end_line": 5000},
	}
	if !reflect.DeepEqual(parsed.OmittedRanges, wantOmitted) {
		t.Fatalf("omitted_ranges = %+v, want %+v", parsed.OmittedRanges, wantOmitted)
	}
	if len(parsed.Suggestions) == 0 || !strings.Contains(strings.Join(parsed.Suggestions, " "), "omitted range") {
		t.Fatalf("read_file response missing omitted-range suggestion: %+v", parsed.Suggestions)
	}
	for _, want := range []string{"  3001\tline-3001", "  3002\tline-3002", "  3003\tline-3003"} {
		if !strings.Contains(parsed.Content, want) {
			t.Fatalf("expected content to include %q, got: %q", want, parsed.Content)
		}
	}
	for _, unwanted := range []string{"line-3000", "line-3004"} {
		if strings.Contains(parsed.Content, unwanted) {
			t.Fatalf("content included line outside requested range %q: %q", unwanted, parsed.Content)
		}
	}

	rangeResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"big.txt","offset":42,"limit":3}`,
	})
	if err != nil {
		t.Fatalf("read_file with range: %v", err)
	}
	var rangeParsed struct {
		Content string `json:"content"`
		Range   struct {
			StartLine int `json:"start_line"`
			EndLine   int `json:"end_line"`
		} `json:"range"`
		NumLines int `json:"num_lines"`
	}
	if err := json.Unmarshal([]byte(rangeResp), &rangeParsed); err != nil {
		t.Fatalf("parse range response: %v", err)
	}
	if rangeParsed.NumLines != 3 || rangeParsed.Range.StartLine != 42 || rangeParsed.Range.EndLine != 44 {
		t.Fatalf("unexpected range response metadata: %+v", rangeParsed)
	}
	for _, want := range []string{"    42\tline-0042", "    43\tline-0043", "    44\tline-0044"} {
		if !strings.Contains(rangeParsed.Content, want) {
			t.Fatalf("range response missing %q:\n%s", want, rangeParsed.Content)
		}
	}

}

func TestToolkit_ReadFileNormalizesProviderFilledEmptySelectors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "all-zero.json"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"config.json","offset":1,"limit":3}`,
	})
	if err != nil {
		t.Fatalf("read_file with provider-filled selectors: %v", err)
	}
	if !strings.Contains(resp, "one") || !strings.Contains(resp, "three") {
		t.Fatalf("normalized read omitted content: %s", resp)
	}

	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"all-zero.json","offset":0,"limit":0}`,
	})
	if err != nil {
		t.Fatalf("read_file with all-zero provider selectors: %v", err)
	}
	if !strings.Contains(resp, "beta") {
		t.Fatalf("whole-file normalized read omitted content: %s", resp)
	}
}

func TestToolkit_ReadFileRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"dir"}`,
	})
	if err == nil {
		t.Fatal("expected directory rejection")
	}
	if !strings.Contains(err.Error(), "path is a directory") {
		t.Fatalf("expected directory guidance, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Use list_files") {
		t.Fatalf("expected list_files guidance, got: %v", err)
	}
}

func TestToolkit_ReadFileRejectsSensitivePaths(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, ".env"), "API_KEY=secret\n")

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":".env"}`,
	})
	if err == nil {
		t.Fatal("expected sensitive path rejection")
	}
	if !strings.Contains(err.Error(), "sensitive path") || !strings.Contains(err.Error(), "Chat approval does not lift this guard") {
		t.Fatalf("expected sensitive path guidance, got: %v", err)
	}
}

func TestToolkit_FileToolsRejectProtectedMetadataPaths(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gitConfigContent := "remote = origin\n"
	mustWriteFile(t, filepath.Join(root, ".git", "config"), gitConfigContent)
	mustWriteFile(t, filepath.Join(root, ".wuu", "runtime", "trace.jsonl"), "{}\n")

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":".git/config"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "version-control metadata") {
		t.Fatalf("expected read_file to reject VCS metadata, got: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":".wuu/runtime/new.json","content":"{}\n"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "wuu runtime state") {
		t.Fatalf("expected write_file to reject wuu runtime state, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".wuu", "runtime", "new.json")); !os.IsNotExist(statErr) {
		t.Fatalf("write_file should not create protected runtime state file, stat err=%v", statErr)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":".git/config","old_text":"remote = origin","new_text":"remote = changed"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "version-control metadata") {
		t.Fatalf("expected edit_file to reject VCS metadata, got: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, ".git", "config")); got != gitConfigContent {
		t.Fatalf("edit_file should not mutate protected VCS metadata: %q", got)
	}

	kit.SetEditToolMode(EditToolModePatch)
	patchArgs, err := json.Marshal(map[string]any{
		"patchText": "*** Begin Patch\n*** Add File: .wuu/runtime/new-from-patch.json\n+{}\n*** End Patch",
	})
	if err != nil {
		t.Fatalf("marshal patch args: %v", err)
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(patchArgs),
	})
	if err == nil || !strings.Contains(err.Error(), "wuu runtime state") {
		t.Fatalf("expected apply_patch to reject wuu runtime state, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".wuu", "runtime", "new-from-patch.json")); !os.IsNotExist(statErr) {
		t.Fatalf("apply_patch should not create protected runtime state file, stat err=%v", statErr)
	}
}

func TestToolkit_EditFileUsesCurrentContentAfterEarlierRead(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"a.txt"}`,
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	originalModTime := info.ModTime()

	if err := os.WriteFile(path, []byte("bravo\n"), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}
	if err := os.Chtimes(path, originalModTime, originalModTime); err != nil {
		t.Fatalf("preserve modtime: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"bravo","new_text":"BRAVO"}`,
	})
	if err != nil {
		t.Fatalf("edit_file should match current content at execution time: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	if string(got) != "BRAVO\n" {
		t.Fatalf("unexpected final content: %q", got)
	}
}

func TestToolkit_EditFileDoesNotRequirePriorRead(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "a.txt"), "alpha\n")

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"alpha","new_text":"bravo"}`,
	})
	if err != nil {
		t.Fatalf("edit_file without prior read: %v", err)
	}
	var parsed struct {
		WorkspaceRevision string   `json:"workspace_revision"`
		OldFileSHA        string   `json:"old_file_sha"`
		NewFileSHA        string   `json:"new_file_sha"`
		Suggestions       []string `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse edit response: %v", err)
	}
	if !strings.HasPrefix(parsed.WorkspaceRevision, "fs:worktree:") {
		t.Fatalf("edit_file response missing filesystem workspace revision: %+v", parsed)
	}
	if parsed.OldFileSHA != formatFileSHA(sha256Hex([]byte("alpha\n"))) || parsed.NewFileSHA != formatFileSHA(sha256Hex([]byte("bravo\n"))) {
		t.Fatalf("edit_file response missing before/after content hashes: %+v", parsed)
	}
	if len(parsed.Suggestions) == 0 || !strings.Contains(strings.Join(parsed.Suggestions, " "), "command execution") {
		t.Fatalf("edit_file response missing validation suggestion: %+v", parsed.Suggestions)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "bravo\n" {
		t.Fatalf("unexpected edited content: %q", got)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"bravo","new_text":"charlie"}`,
	}); err != nil {
		t.Fatalf("consecutive exact edit: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "charlie\n" {
		t.Fatalf("unexpected content after consecutive edit: %q", got)
	}

}

func TestToolkit_EditFileReportsRecoverableTextMatchErrors(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	content := "alpha\nbravo\ncharlie\nbravo\n"
	mustWriteFile(t, filepath.Join(root, "a.txt"), content)
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"a.txt"}`,
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"bravx","new_text":"BRAVX"}`,
	})
	if err == nil {
		t.Fatal("expected old_text not found error")
	}
	if !strings.Contains(err.Error(), "old_text_not_found") ||
		!strings.Contains(err.Error(), "candidates") ||
		!strings.Contains(err.Error(), "2| bravo") ||
		!strings.Contains(err.Error(), "safe_retry") {
		t.Fatalf("expected recoverable old_text guidance, got: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"bravo","new_text":"BRAVO"}`,
	})
	if err == nil {
		t.Fatal("expected ambiguous old_text error")
	}
	if !strings.Contains(err.Error(), "ambiguous_old_text") ||
		!strings.Contains(err.Error(), "matched 2 locations") ||
		!strings.Contains(err.Error(), "lines 2-2") ||
		!strings.Contains(err.Error(), "lines 4-4") ||
		!strings.Contains(err.Error(), "replace_all=true") {
		t.Fatalf("expected ambiguous old_text guidance, got: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != content {
		t.Fatalf("failed edit should not mutate file: %q", got)
	}
}

func TestToolkit_EditFileRejectsSensitivePaths(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, ".env"), "API_KEY=secret\n")

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":".env","old_text":"API_KEY=secret","new_text":"API_KEY=changed"}`,
	})
	if err == nil {
		t.Fatal("expected sensitive path rejection")
	}
	if !strings.Contains(err.Error(), "edit_file refuses") || !strings.Contains(err.Error(), "sensitive path") {
		t.Fatalf("expected edit_file sensitive path guidance, got: %v", err)
	}
	if strings.Contains(err.Error(), "API_KEY") {
		t.Fatalf("edit_file sensitive path error leaked content: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, ".env")); got != "API_KEY=secret\n" {
		t.Fatalf("edit_file should not mutate sensitive file: %q", got)
	}
}

func TestToolkit_ApplyPatchEditsAddsDeletesAndMoves(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	sessionDir := filepath.Join(t.TempDir(), "session")
	kit.SetSessionID("session-apply-patch")
	kit.SetSessionDir(sessionDir)

	mustWriteFile(t, filepath.Join(root, "a.txt"), "line one\nline two\nline three\n")
	mustWriteFile(t, filepath.Join(root, "remove.txt"), "remove me\n")
	mustWriteFile(t, filepath.Join(root, "oldname.txt"), "old name\n")

	patchText := `*** Begin Patch
*** Update File: a.txt
@@
 line one
-line two
+line 2
 line three
*** Add File: dir/new.txt
+created
*** Delete File: remove.txt
*** Update File: oldname.txt
*** Move to: renamed.txt
@@
-old name
+new name
*** End Patch`
	args, err := json.Marshal(map[string]any{
		"patchText": patchText,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(args),
	})
	if err != nil {
		t.Fatalf("apply_patch: %v", err)
	}
	resp := result.Content[0].Text
	wantResponse := strings.Join([]string{
		"Success. Updated the following files:",
		"M a.txt",
		"A dir/new.txt",
		"D remove.txt",
		"M oldname.txt -> renamed.txt",
	}, "\n")
	if resp != wantResponse {
		t.Fatalf("unexpected apply_patch model response:\ngot:\n%s\nwant:\n%s", resp, wantResponse)
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(result.StructuredContent, &detail); err != nil {
		t.Fatalf("parse apply_patch structured detail: %v", err)
	}
	if len(detail) != 1 || detail["files"] == nil {
		t.Fatalf("structured detail should contain only actual file changes: %s", result.StructuredContent)
	}
	var files []applyPatchFileResult
	if err := json.Unmarshal(detail["files"], &files); err != nil {
		t.Fatalf("parse apply_patch file changes: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("file changes = %+v, want four entries", files)
	}
	actions := map[string]string{}
	for _, file := range files {
		actions[file.Path] = file.Action
		if file.Action != "add" && len(file.Diff.Hunks) == 0 {
			t.Fatalf("file change missing diff: %+v", file)
		}
	}
	wantActions := map[string]string{"a.txt": "update", "dir/new.txt": "add", "remove.txt": "delete", "oldname.txt": "move"}
	if !reflect.DeepEqual(actions, wantActions) {
		t.Fatalf("file actions = %+v, want %+v", actions, wantActions)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "patch-journal")); !os.IsNotExist(err) {
		t.Fatalf("apply_patch should not create a journal, stat err=%v", err)
	}
	records := kit.ToolTelemetry()
	if len(records) != 1 || len(records[0].ArtifactRefs) != 0 || records[0].PatchRiskSummary != nil {
		t.Fatalf("apply_patch telemetry should not duplicate client file changes: %+v", records)
	}

	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "line one\nline 2\nline three\n" {
		t.Fatalf("unexpected updated content: %q", got)
	}
	if got := mustReadFile(t, filepath.Join(root, "dir/new.txt")); got != "created\n" {
		t.Fatalf("unexpected added content: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "remove.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected remove.txt to be deleted, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "oldname.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected oldname.txt to be moved, stat err=%v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, "renamed.txt")); got != "new name\n" {
		t.Fatalf("unexpected moved content: %q", got)
	}
}

func TestApplyPatchModelSummaryContainsOnlyStableFileRecords(t *testing.T) {
	files := make([]applyPatchFileResult, 1000)
	for i := range files {
		files[i] = applyPatchFileResult{
			Path:   fmt.Sprintf("generated/%04d-%s.txt", i, strings.Repeat("long-name-", 8)),
			Action: "update",
			Diff: DiffResult{Hunks: []DiffHunk{{
				Lines: []DiffLine{{Op: "insert", Content: strings.Repeat("large diff content ", 100)}},
			}}},
		}
	}
	first := applyPatchModelSummary(false, files)
	second := applyPatchModelSummary(false, files)
	if first != second {
		t.Fatal("apply_patch model summary changed for identical input")
	}
	if !strings.Contains(first, "M generated/0000-") || !strings.Contains(first, "M generated/0999-") {
		t.Fatalf("apply_patch model summary should list every changed file:\n%s", first)
	}
	for _, extra := range []string{"files omitted", "large diff content", "Workspace revision:", "Patch journal:", "Recovery:", "Warning:"} {
		if strings.Contains(first, extra) {
			t.Fatalf("apply_patch model summary contains unsupported extra %q:\n%s", extra, first)
		}
	}
}

func TestToolkit_ApplyPatchDryRunDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	changedHookCalls := 0
	kit.SetOnFileChanged(func(string) {
		changedHookCalls++
	})

	mustWriteFile(t, filepath.Join(root, "a.txt"), "line one\nline two\nline three\n")
	mustWriteFile(t, filepath.Join(root, "remove.txt"), "remove me\n")
	mustWriteFile(t, filepath.Join(root, "oldname.txt"), "old name\n")

	patchText := `*** Begin Patch
*** Update File: a.txt
@@
 line one
-line two
+line 2
 line three
*** Add File: dir/new.txt
+created
*** Delete File: remove.txt
*** Update File: oldname.txt
*** Move to: renamed.txt
@@
-old name
+new name
*** End Patch`
	args, err := json.Marshal(map[string]any{"patchText": patchText, "dry_run": true})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(args),
	})
	if err != nil {
		t.Fatalf("apply_patch dry-run: %v", err)
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(result.StructuredContent, &detail); err != nil {
		t.Fatalf("parse apply_patch structured detail: %v", err)
	}
	if len(detail) != 1 || detail["files"] == nil {
		t.Fatalf("dry-run structured detail should contain only file changes: %s", result.StructuredContent)
	}
	var files []applyPatchFileResult
	if err := json.Unmarshal(detail["files"], &files); err != nil {
		t.Fatalf("parse apply_patch dry-run file changes: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("unexpected dry-run file changes: %+v", files)
	}
	resp := result.Content[0].Text
	if !strings.HasPrefix(resp, "Patch validation succeeded.") || strings.Contains(resp, `"diff"`) || strings.Contains(resp, "Patch journal:") {
		t.Fatalf("unexpected dry-run model response:\n%s", resp)
	}
	if changedHookCalls != 0 {
		t.Fatalf("dry-run should not fire file-change hooks, got %d", changedHookCalls)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "line one\nline two\nline three\n" {
		t.Fatalf("dry-run mutated update file: %q", got)
	}
	if got := mustReadFile(t, filepath.Join(root, "remove.txt")); got != "remove me\n" {
		t.Fatalf("dry-run deleted file content: %q", got)
	}
	if got := mustReadFile(t, filepath.Join(root, "oldname.txt")); got != "old name\n" {
		t.Fatalf("dry-run moved source content: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "dir/new.txt")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create new file, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "renamed.txt")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create move target, stat err=%v", err)
	}
}

func TestToolkit_ApplyPatchRejectsInvalidPatchAtomically(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	changedHookCalls := 0
	kit.SetOnFileChanged(func(string) {
		changedHookCalls++
	})

	mustWriteFile(t, filepath.Join(root, "a.txt"), "alpha\n")
	mustWriteFile(t, filepath.Join(root, "b.txt"), "beta\n")

	patchText := `*** Begin Patch
*** Update File: a.txt
@@
-alpha
+ALPHA
*** Update File: b.txt
@@
-missing
+BETA
*** End Patch`
	args, err := json.Marshal(map[string]any{
		"patchText": patchText,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(args),
	})
	if err == nil || !strings.Contains(err.Error(), "apply_patch verification failed") ||
		!strings.Contains(err.Error(), "b.txt") {
		t.Fatalf("expected failed verification for b.txt, got: %v", err)
	}
	if !strings.Contains(err.Error(), "anchor_not_found") ||
		!strings.Contains(err.Error(), "candidates") ||
		!strings.Contains(err.Error(), "1| beta") ||
		!strings.Contains(err.Error(), "safe_retry") {
		t.Fatalf("expected recoverable patch guidance, got: %v", err)
	}
	if changedHookCalls != 0 {
		t.Fatalf("failed patch should not fire file-change hooks, got %d", changedHookCalls)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "alpha\n" {
		t.Fatalf("failed patch mutated first file: %q", got)
	}
	if got := mustReadFile(t, filepath.Join(root, "b.txt")); got != "beta\n" {
		t.Fatalf("failed patch mutated second file: %q", got)
	}
}

func TestToolkit_ApplyPatchUsesCurrentAnchorsWithoutReadBaseline(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	mustWriteFile(t, filepath.Join(root, "a.txt"), "alpha\n")

	patchText := `*** Begin Patch
*** Update File: a.txt
@@
-alpha
+bravo
*** End Patch`
	args, err := json.Marshal(map[string]string{"patchText": patchText})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(args),
	})
	if err != nil {
		t.Fatalf("apply_patch without read baseline: %v", err)
	}
	var parsed struct {
		Files []applyPatchFileResult `json:"files"`
	}
	if err := json.Unmarshal(result.StructuredContent, &parsed); err != nil {
		t.Fatalf("parse apply_patch structured detail: %v", err)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "a.txt" || parsed.Files[0].Action != "update" || len(parsed.Files[0].Diff.Hunks) == 0 {
		t.Fatalf("unexpected file change: %+v", parsed.Files)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "bravo\n" {
		t.Fatalf("unexpected patched content: %q", got)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(args),
	})
	if err == nil || !strings.Contains(err.Error(), "anchor_not_found") {
		t.Fatalf("expected stale patch anchor rejection, got: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "bravo\n" {
		t.Fatalf("failed stale patch should not mutate file: %q", got)
	}

	resolvedA, err := kit.env.ResolvePath("a.txt")
	if err != nil {
		t.Fatalf("resolve a.txt: %v", err)
	}
	entry, ok := kit.env.GetReadEntry(resolvedA)
	if !ok || !entry.WrittenByTool || entry.ContentSHA256 != sha256Hex([]byte("bravo\n")) {
		t.Fatalf("apply_patch should record current written content, got ok=%t entry=%+v", ok, entry)
	}
}

func TestToolkit_ApplyPatchRejectsAmbiguousUpdate(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	mustWriteFile(t, filepath.Join(root, "a.txt"), "same\nsame\n")

	args, err := json.Marshal(map[string]string{"patchText": `*** Begin Patch
*** Update File: a.txt
@@
-same
+different
*** End Patch`})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(args),
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous update error, got %v", err)
	}
	if !strings.Contains(err.Error(), "ambiguous_anchor") ||
		!strings.Contains(err.Error(), "lines 1-1") ||
		!strings.Contains(err.Error(), "lines 2-2") ||
		!strings.Contains(err.Error(), "safe_retry") {
		t.Fatalf("expected ambiguous anchor recovery guidance, got %v", err)
	}
}

func TestToolkit_ApplyPatchRejectsSensitivePaths(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	mustWriteFile(t, filepath.Join(root, ".env"), "API_KEY=secret\n")

	cases := []struct {
		name    string
		patch   string
		dryRun  bool
		wantOp  string
		leakKey string
	}{
		{
			name: "dry-run update",
			patch: `*** Begin Patch
*** Update File: .env
@@
-API_KEY=secret
+API_KEY=changed
*** End Patch`,
			dryRun:  true,
			wantOp:  "update",
			leakKey: "API_KEY=secret",
		},
		{
			name: "add",
			patch: `*** Begin Patch
*** Add File: secrets/config.txt
+token=secret
*** End Patch`,
			wantOp:  "add",
			leakKey: "token=secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"patchText": tc.patch, "dry_run": tc.dryRun})
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			_, err = kit.Execute(context.Background(), providers.ToolCall{
				Name:      "apply_patch",
				Arguments: string(args),
			})
			if err == nil {
				t.Fatalf("expected sensitive path rejection for %s", tc.name)
			}
			if !strings.Contains(err.Error(), "sensitive path") || !strings.Contains(err.Error(), tc.wantOp) {
				t.Fatalf("expected sensitive path %s guidance, got: %v", tc.wantOp, err)
			}
			if strings.Contains(err.Error(), tc.leakKey) {
				t.Fatalf("apply_patch sensitive path error leaked content for %s: %v", tc.name, err)
			}
		})
	}
}

func TestToolkit_ToolMetadata_ClassifiesApplyPatchDryRun(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)

	meta, ok := kit.ToolMetadata(providers.ToolCall{
		Name:      "apply_patch",
		Arguments: `{"patchText":"*** Begin Patch\n*** End Patch","dry_run":true}`,
	})
	if !ok {
		t.Fatal("apply_patch metadata not found")
	}
	if !meta.ReadOnly || !meta.ConcurrencySafe || meta.Risk != string(ToolRiskLow) || meta.Reason != "patch dry-run preview" {
		t.Fatalf("dry-run apply_patch metadata = %+v, want low-risk read-only preview", meta)
	}

	meta, ok = kit.ToolMetadata(providers.ToolCall{
		Name:      "apply_patch",
		Arguments: `{"patchText":"*** Begin Patch\n*** End Patch"}`,
	})
	if !ok {
		t.Fatal("apply_patch metadata not found")
	}
	if meta.ReadOnly || meta.ConcurrencySafe || meta.Risk != string(ToolRiskHigh) || meta.Reason != "patch applies workspace changes" {
		t.Fatalf("mutating apply_patch metadata = %+v, want high-risk write", meta)
	}
}

func TestToolkit_ApplyPatchAppendsAtEndOfFile(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	mustWriteFile(t, filepath.Join(root, "a.txt"), "first\n")

	args, err := json.Marshal(map[string]string{"patchText": `*** Begin Patch
*** Update File: a.txt
@@
*** End of File
+second
*** End Patch`})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "apply_patch",
		Arguments: string(args),
	}); err != nil {
		t.Fatalf("apply_patch: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "first\nsecond\n" {
		t.Fatalf("unexpected appended content: %q", got)
	}
}

func TestToolkit_ListFilesRejectsFile(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "list_files",
		Arguments: `{"path":"a.txt"}`,
	})
	if err == nil {
		t.Fatal("expected file rejection")
	}
	if !strings.Contains(err.Error(), "path is not a directory") {
		t.Fatalf("expected file guidance, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Use read_file") {
		t.Fatalf("expected read_file guidance, got: %v", err)
	}
}

func TestToolkit_ListFilesRejectsAndFiltersProtectedPaths(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "visible.txt"), "hello\n")
	mustWriteFile(t, filepath.Join(root, ".env"), "API_KEY=secret\n")
	mustWriteFile(t, filepath.Join(root, ".git", "config"), "remote = origin\n")
	mustWriteFile(t, filepath.Join(root, ".wuu", "runtime", "trace.jsonl"), "{}\n")

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "list_files",
		Arguments: `{"path":".wuu"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "wuu runtime state") {
		t.Fatalf("expected direct .wuu list rejection, got: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "list_files",
		Arguments: `{}`,
	})
	if err != nil {
		t.Fatalf("list_files root: %v", err)
	}
	var parsed struct {
		OmittedProtected int `json:"omitted_protected"`
		Entries          []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse list_files response: %v\n%s", err, resp)
	}
	if parsed.OmittedProtected != 3 {
		t.Fatalf("expected three protected entries omitted, got %+v from %s", parsed, resp)
	}
	if len(parsed.Entries) != 1 || parsed.Entries[0].Name != "visible.txt" || parsed.Entries[0].Path != "visible.txt" {
		t.Fatalf("list_files should only return visible entries: %+v", parsed.Entries)
	}
	if strings.Contains(resp, ".wuu") || strings.Contains(resp, ".git") || strings.Contains(resp, ".env") || strings.Contains(resp, "secret") {
		t.Fatalf("list_files leaked protected entry names or content: %s", resp)
	}
}

func TestToolkit_ListFilesReturnsEntryPathsAndSuggestions(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "dir", "a.txt"), "hello\n")
	if err := os.MkdirAll(filepath.Join(root, "dir", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "list_files",
		Arguments: `{"path":"dir"}`,
	})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	var parsed struct {
		Path              string `json:"path"`
		WorkspaceRevision string `json:"workspace_revision"`
		Total             int    `json:"total"`
		OmittedEntryCount int    `json:"omitted_entry_count"`
		Entries           []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
			Size  int64  `json:"size,omitempty"`
		} `json:"entries"`
		Suggestions []string `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse list_files response: %v", err)
	}
	if parsed.Path != "dir" || parsed.Total != 2 || parsed.OmittedEntryCount != 0 {
		t.Fatalf("unexpected list_files metadata: %+v", parsed)
	}
	if !strings.HasPrefix(parsed.WorkspaceRevision, "fs:worktree:") {
		t.Fatalf("list_files response missing filesystem workspace revision: %+v", parsed)
	}
	wantPaths := []string{"dir/a.txt", "dir/sub"}
	gotPaths := make([]string, 0, len(parsed.Entries))
	for _, entry := range parsed.Entries {
		gotPaths = append(gotPaths, entry.Path)
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("entry paths = %+v, want %+v", gotPaths, wantPaths)
	}
	if len(parsed.Suggestions) == 0 || !strings.Contains(strings.Join(parsed.Suggestions, " "), "read_file") {
		t.Fatalf("list_files response missing next suggestion: %+v", parsed.Suggestions)
	}
}

func TestToolkit_FileToolTelemetryRecordsResultActions(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "a.txt"), "one\n")

	if _, err := kit.Execute(toolctx.WithStepIndex(context.Background(), 2), providers.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "list_files", Arguments: `{}`}); err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "write_file", Arguments: `{"path":"new.txt","content":"new\n"}`}); err != nil {
		t.Fatalf("write_file create: %v", err)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "edit_file",
		Arguments: `{"path":"a.txt","old_text":"one","new_text":"two"}`,
	}); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"a.txt","content":"three\n"}`,
	}); err != nil {
		t.Fatalf("write_file overwrite: %v", err)
	}

	records := kit.ToolTelemetry()
	if len(records) != 5 {
		t.Fatalf("expected five telemetry records, got %+v", records)
	}
	got := make([]string, 0, len(records))
	for _, record := range records {
		got = append(got, record.Name+":"+record.ResultAction)
	}
	want := []string{"read_file:read", "list_files:list", "write_file:create", "edit_file:edit", "write_file:overwrite"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("file tool result actions = %+v, want %+v", got, want)
	}
	if records[0].StepIndex == nil || *records[0].StepIndex != 2 {
		t.Fatalf("read_file step index = %+v, want 2", records[0].StepIndex)
	}
	if records[1].StepIndex != nil {
		t.Fatalf("tool execution without step context should not record step index: %+v", records[1].StepIndex)
	}
}

func definitionNames(defs []providers.ToolDefinition) map[string]bool {
	out := make(map[string]bool, len(defs))
	for _, def := range defs {
		out[def.Name] = true
	}
	return out
}

func TestToolkit_DisableTools_HidesDefinitionsAndBlocksExecute(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.DisableTools("write_file", "edit_file", "bash")

	defs := kit.Definitions()
	for _, d := range defs {
		if d.Name == "write_file" || d.Name == "edit_file" || d.Name == "bash" {
			t.Fatalf("disabled tool %q should not appear in definitions", d.Name)
		}
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"a.txt","content":"x"}`,
	})
	if err == nil {
		t.Fatal("expected disabled write_file to error")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"echo hi"}`,
	})
	if err == nil {
		t.Fatal("expected disabled run_shell to error")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got: %v", err)
	}
}

func TestToolkit_RunShell(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"echo hi","purpose":"confirm shell purpose metadata"}`,
	})
	if err != nil {
		t.Fatalf("run_shell: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if parsed["exit_code"].(float64) != 0 {
		t.Fatalf("unexpected exit code: %v", parsed["exit_code"])
	}
	if parsed["action"].(string) != "run" {
		t.Fatalf("unexpected run_shell action: %+v", parsed)
	}
	if !strings.Contains(parsed["output"].(string), "hi") {
		t.Fatalf("unexpected output: %v", parsed["output"])
	}
	if parsed["purpose"].(string) != "confirm shell purpose metadata" {
		t.Fatalf("unexpected purpose: %+v", parsed)
	}
	if !strings.Contains(parsed["stdout_tail"].(string), "hi") {
		t.Fatalf("unexpected stdout tail: %v", parsed["stdout_tail"])
	}
	if parsed["stderr_tail"].(string) != "" {
		t.Fatalf("unexpected stderr tail: %v", parsed["stderr_tail"])
	}
	if parsed["duration_ms"].(float64) < 0 {
		t.Fatalf("unexpected duration: %v", parsed["duration_ms"])
	}
	if revision, _ := parsed["workspace_revision"].(string); !strings.HasPrefix(revision, "fs:worktree:") {
		t.Fatalf("run_shell response missing filesystem workspace revision: %+v", parsed)
	}
	if suggestions, ok := parsed["next_suggestions"].([]any); !ok || len(suggestions) == 0 {
		t.Fatalf("run_shell response missing next_suggestions: %+v", parsed)
	}
	records := kit.ToolTelemetry()
	if len(records) != 1 || records[0].ResultAction != "run" {
		t.Fatalf("run_shell telemetry missing result action: %+v", records)
	}
}

func TestToolkit_RunShellRedactsSensitiveOutput(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	sessionDir := filepath.Join(t.TempDir(), "session")
	kit.SetSessionDir(sessionDir)

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"printf 'API_KEY=secret-value-1234567890\nAuthorization: Bearer abcdefghijklmnop\nsk-testsecret123456\n'","purpose":"diagnose TOKEN=purpose-secret-value-1234567890"}`,
	})
	if err != nil {
		t.Fatalf("run_shell: %v", err)
	}

	for _, leaked := range []string{"secret-value", "abcdefghijklmnop", "sk-testsecret", "purpose-secret"} {
		if strings.Contains(resp, leaked) {
			t.Fatalf("run_shell response leaked %q: %s", leaked, resp)
		}
	}
	if strings.Count(resp, "[REDACTED]") < 3 {
		t.Fatalf("expected redaction markers, got: %s", resp)
	}
	var parsed struct {
		FullLogRef        string `json:"full_log_ref"`
		FullLogBytes      int    `json:"full_log_bytes"`
		WorkspaceRevision string `json:"workspace_revision"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse run_shell response: %v\n%s", err, resp)
	}
	if !strings.HasPrefix(parsed.WorkspaceRevision, "fs:worktree:") {
		t.Fatalf("run_shell response missing filesystem workspace revision: %+v", parsed)
	}
	if parsed.FullLogRef == "" || parsed.FullLogBytes <= 0 {
		t.Fatalf("run_shell response missing full log artifact: %+v", parsed)
	}
	if !strings.HasPrefix(parsed.FullLogRef, filepath.Join(sessionDir, "tool-results", "shell-logs")) {
		t.Fatalf("full log ref outside session dir: %q", parsed.FullLogRef)
	}
	logData, err := os.ReadFile(parsed.FullLogRef)
	if err != nil {
		t.Fatalf("read full log artifact: %v", err)
	}
	logText := string(logData)
	for _, leaked := range []string{"secret-value", "abcdefghijklmnop", "sk-testsecret", "purpose-secret"} {
		if strings.Contains(logText, leaked) {
			t.Fatalf("run_shell full log leaked %q:\n%s", leaked, logText)
		}
	}
	if !strings.Contains(logText, "purpose: diagnose TOKEN=[REDACTED]") {
		t.Fatalf("full log artifact missing redacted purpose:\n%s", logText)
	}
	if strings.Count(logText, "[REDACTED]") < 3 || !strings.Contains(logText, "exit_code: 0") {
		t.Fatalf("full log artifact missing redacted evidence:\n%s", logText)
	}
	if !strings.Contains(logText, "workspace_revision: "+parsed.WorkspaceRevision) {
		t.Fatalf("full log artifact missing workspace revision:\n%s", logText)
	}
	records := kit.ToolTelemetry()
	if len(records) != 1 || !containsString(records[0].ArtifactRefs, parsed.FullLogRef) {
		t.Fatalf("tool telemetry missing shell log artifact ref: records=%+v full_log_ref=%q", records, parsed.FullLogRef)
	}
}

func TestToolkit_RunShellStructuredFailureOutput(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"printf out; printf err >&2; exit 7"}`,
	})
	if err != nil {
		t.Fatalf("run_shell: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if parsed["exit_code"].(float64) != 7 {
		t.Fatalf("unexpected exit code: %v", parsed["exit_code"])
	}
	if got := parsed["stdout_tail"].(string); got != "out" {
		t.Fatalf("stdout_tail = %q, want out", got)
	}
	if got := parsed["stderr_tail"].(string); got != "err" {
		t.Fatalf("stderr_tail = %q, want err", got)
	}
	if got := parsed["stdout_bytes"].(float64); got != 3 {
		t.Fatalf("stdout_bytes = %v, want 3", got)
	}
	if got := parsed["stderr_bytes"].(float64); got != 3 {
		t.Fatalf("stderr_bytes = %v, want 3", got)
	}
	if parsed["stdout_tail_truncated"].(bool) || parsed["stderr_tail_truncated"].(bool) {
		t.Fatalf("short output should not be tail-truncated: %+v", parsed)
	}
}

func TestToolkit_RunShellSetsNonInteractiveEnv(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"printf '%s|%s|%s|%s|%s|%s|%s|%s' \"$GIT_EDITOR\" \"$GIT_SEQUENCE_EDITOR\" \"$EDITOR\" \"$VISUAL\" \"$PAGER\" \"$GIT_PAGER\" \"$GH_PAGER\" \"$GIT_TERMINAL_PROMPT\""}`,
	})
	if err != nil {
		t.Fatalf("run_shell: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if got := parsed["output"].(string); got != "true|true|true|true|cat|cat|cat|0" {
		t.Fatalf("unexpected shell env: %q", got)
	}
}

func TestToolkit_RunShellAllowsSafeGitCommands(t *testing.T) {
	kit, root := setupGitRepo(t)
	enableShellExecutionForTest(kit.env)
	kit.env.GitWrapperExecutable = buildWuuForGitWrapper(t)
	kit.SetSessionDir(t.TempDir())

	for _, command := range []string{
		"git status --short",
		"command git status --short",
		"env FOO=bar git status --short",
		"nice git status --short",
		"cd . && git status --short",
	} {
		resp, err := kit.Execute(context.Background(), providers.ToolCall{
			Name:      "bash",
			Arguments: fmt.Sprintf(`{"command":%q,"timeout_seconds":10}`, command),
		})
		if err != nil {
			t.Fatalf("expected safe git shell command %q to run: %v", command, err)
		}
		var parsed struct {
			ExitCode       int                `json:"exit_code"`
			Classification ToolClassification `json:"classification"`
		}
		if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
			t.Fatalf("parse run_shell response for %q: %v\n%s", command, err, resp)
		}
		if parsed.ExitCode != 0 || !parsed.Classification.ReadOnly || parsed.Classification.Risk != ToolRiskLow {
			t.Fatalf("safe git shell response for %q = %+v, want successful low-risk read-only", command, parsed)
		}
	}

	mustWriteFile(t, filepath.Join(root, "hello.txt"), "updated\n")
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"git add hello.txt && git commit -m \"update hello\"","timeout_seconds":10}`,
	})
	if err != nil {
		t.Fatalf("expected explicit git add + commit shell command to run: %v", err)
	}
	var parsed struct {
		ExitCode       int                `json:"exit_code"`
		Classification ToolClassification `json:"classification"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse add+commit response: %v\n%s", err, resp)
	}
	if parsed.ExitCode != 0 || parsed.Classification.ReadOnly || parsed.Classification.Risk != ToolRiskMedium {
		t.Fatalf("git add+commit shell response = %+v, want successful medium-risk write", parsed)
	}

	mustWriteFile(t, filepath.Join(root, "hello.txt"), "updated again\n")
	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"git add hello.txt && git commit -m \"Sweep the whole process cluster row when it's running\" -m \"Body includes \\\"still working\\\" and punctuation; still a message.\"","timeout_seconds":10}`,
	})
	if err != nil {
		t.Fatalf("expected repeated -m commit with escaped quotes to run: %v", err)
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse repeated -m response: %v\n%s", err, resp)
	}
	if parsed.ExitCode != 0 || parsed.Classification.ReadOnly || parsed.Classification.Risk != ToolRiskMedium {
		t.Fatalf("repeated -m shell response = %+v, want successful medium-risk write", parsed)
	}

	mustWriteFile(t, filepath.Join(root, "hello.txt"), "updated from file\n")
	mustWriteFile(t, filepath.Join(root, "commit-message.txt"), "Message from file\n\nBody\n")
	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"git add hello.txt && git commit -F commit-message.txt","timeout_seconds":10}`,
	})
	if err != nil {
		t.Fatalf("expected -F commit to run: %v", err)
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse -F response: %v\n%s", err, resp)
	}
	if parsed.ExitCode != 0 || parsed.Classification.ReadOnly || parsed.Classification.Risk != ToolRiskMedium {
		t.Fatalf("-F shell response = %+v, want successful medium-risk write", parsed)
	}

	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"git commit --amend -m \"Amended shell subject\"","timeout_seconds":10}`,
	})
	if err != nil {
		t.Fatalf("expected --amend -m commit to run: %v", err)
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse --amend response: %v\n%s", err, resp)
	}
	if parsed.ExitCode != 0 || parsed.Classification.ReadOnly || parsed.Classification.Risk != ToolRiskMedium {
		t.Fatalf("--amend shell response = %+v, want successful medium-risk write", parsed)
	}
}

type execCommandFunc func(context.Context, string, ...string) *exec.Cmd

func withRGTestHooks(t *testing.T, lookup func(string) (string, error), cmd execCommandFunc) {
	t.Helper()
	origLookup := rgLookupPath
	origCmd := rgCommand
	rgLookupPath = lookup
	if cmd != nil {
		rgCommand = cmd
	}
	resetRGForTests()
	t.Cleanup(func() {
		rgLookupPath = origLookup
		rgCommand = origCmd
		resetRGForTests()
	})
}

func TestRipgrepCommandsUseExplicitRootAndIgnoreHostConfig(t *testing.T) {
	withRGTestHooks(t, func(string) (string, error) { return "/usr/bin/rg", nil }, exec.CommandContext)

	grepCmd := buildRGGrepCommand(context.Background(), "needle", "", "*.go", grepOptions{})
	if grepCmd == nil {
		t.Fatal("grep command was not built")
	}
	grepArgs := grepCmd.Args[1:]
	if len(grepArgs) == 0 || grepArgs[0] != "--no-config" || grepArgs[len(grepArgs)-1] != "." {
		t.Fatalf("grep args do not bind config and root explicitly: %q", grepArgs)
	}
	separator := slices.Index(grepArgs, "--")
	if separator < 0 || separator+1 >= len(grepArgs) || grepArgs[separator+1] != "needle" {
		t.Fatalf("grep pattern is not protected by --: %q", grepArgs)
	}

	globCmd := buildRGFilesCommand(context.Background(), "*.go")
	if globCmd == nil {
		t.Fatal("glob command was not built")
	}
	globArgs := globCmd.Args[1:]
	if len(globArgs) == 0 || globArgs[0] != "--no-config" || globArgs[len(globArgs)-1] != "." {
		t.Fatalf("glob args do not bind config and root explicitly: %q", globArgs)
	}
}

func TestToolkit_GlobRipgrepIncludesHiddenFiles(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for path, content := range map[string]string{
		".env":          "TOKEN=abc\n",
		"visible.env":   "TOKEN=visible\n",
		"dir/.env.test": "TOKEN=nested\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"*.env*"}`,
	})
	if err != nil {
		t.Fatalf("glob *.env*: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	if !reflect.DeepEqual(parsed.Files, []string{".env", "dir/.env.test", "visible.env"}) {
		t.Fatalf("unexpected hidden glob matches: %+v", parsed.Files)
	}
}

func TestToolkit_GrepRipgrepIncludesHiddenFiles(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for path, content := range map[string]string{
		".env":        "API_KEY=secret\n",
		"visible.env": "API_KEY=visible\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"API_KEY","include":"*.env"}`,
	})
	if err != nil {
		t.Fatalf("grep *.env: %v", err)
	}
	var parsed struct {
		Matches []grepMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	want := []grepMatch{
		{File: ".env", Line: 1, Content: "[REDACTED: sensitive file content]"},
		{File: "visible.env", Line: 1, Content: "[REDACTED: sensitive file content]"},
	}
	if !reflect.DeepEqual(parsed.Matches, want) {
		t.Fatalf("unexpected hidden grep matches: got %+v want %+v", parsed.Matches, want)
	}
	if strings.Contains(resp, "API_KEY=secret") || strings.Contains(resp, "API_KEY=visible") {
		t.Fatalf("grep response leaked sensitive file content: %s", resp)
	}
}

func TestToolkit_SearchResultsIncludeNextSuggestions(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc target() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	grepResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target"}`,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	var grepParsed struct {
		Action                string      `json:"action"`
		Matches               []grepMatch `json:"matches"`
		ContinuationSupported bool        `json:"continuation_supported"`
		Suggestions           []string    `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(grepResp), &grepParsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	if len(grepParsed.Matches) != 1 {
		t.Fatalf("unexpected grep matches: %+v", grepParsed.Matches)
	}
	if grepParsed.Action != "grep" {
		t.Fatalf("grep action = %q, want grep", grepParsed.Action)
	}
	if !grepParsed.ContinuationSupported {
		t.Fatalf("grep did not advertise stable continuation: %+v", grepParsed)
	}
	if len(grepParsed.Suggestions) == 0 || !strings.Contains(strings.Join(grepParsed.Suggestions, " "), "read_file") {
		t.Fatalf("grep response missing read_file suggestion: %+v", grepParsed.Suggestions)
	}

	globResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"*.missing"}`,
	})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var globParsed struct {
		Action                string   `json:"action"`
		Files                 []string `json:"files"`
		ContinuationSupported bool     `json:"continuation_supported"`
		Suggestions           []string `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(globResp), &globParsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	if len(globParsed.Files) != 0 {
		t.Fatalf("unexpected glob matches: %+v", globParsed.Files)
	}
	if globParsed.Action != "glob" {
		t.Fatalf("glob action = %q, want glob", globParsed.Action)
	}
	if !globParsed.ContinuationSupported {
		t.Fatalf("glob did not advertise stable continuation: %+v", globParsed)
	}
	if len(globParsed.Suggestions) == 0 || !strings.Contains(strings.Join(globParsed.Suggestions, " "), "broader glob") {
		t.Fatalf("empty glob response missing broaden suggestion: %+v", globParsed.Suggestions)
	}
	for _, mode := range []string{"files_with_matches", "count"} {
		resp, err := kit.Execute(context.Background(), providers.ToolCall{
			Name:      "grep",
			Arguments: `{"pattern":"target","output_mode":"` + mode + `"}`,
		})
		if err != nil {
			t.Fatalf("grep %s: %v", mode, err)
		}
		var parsed struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
			t.Fatalf("parse grep %s response: %v", mode, err)
		}
		if parsed.Action != "grep" {
			t.Fatalf("grep %s action = %q, want grep", mode, parsed.Action)
		}
	}
	records := kit.ToolTelemetry()
	gotActions := make([]string, 0, len(records))
	for _, record := range records {
		gotActions = append(gotActions, record.Name+":"+record.ResultAction)
	}
	wantActions := []string{"grep:grep", "glob:glob", "grep:grep", "grep:grep"}
	if !reflect.DeepEqual(gotActions, wantActions) {
		t.Fatalf("search telemetry missing result actions: %+v", records)
	}
}

func TestToolkit_SearchCursorReusesMaterializedResult(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < globPageSize+20; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%04d.go", i)), []byte("package p\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sessionDir := t.TempDir()
	kit.SetSessionDir(sessionDir)

	first, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"*.go"}`,
	})
	if err != nil {
		t.Fatalf("first glob: %v", err)
	}
	var firstParsed struct {
		Files   []string `json:"files"`
		Total   int      `json:"total"`
		HasMore bool     `json:"has_more"`
		Page    struct {
			Offset int `json:"offset"`
			Next   struct {
				Offset           int    `json:"offset"`
				ExpectedRevision string `json:"expected_revision"`
			} `json:"next"`
		} `json:"page"`
	}
	if err := json.Unmarshal([]byte(first), &firstParsed); err != nil {
		t.Fatalf("parse first glob response: %v", err)
	}
	if firstParsed.Total != globPageSize+20 || !firstParsed.HasMore || firstParsed.Page.Next.Offset != globPageSize {
		t.Fatalf("first glob page metadata = %+v", firstParsed)
	}
	cursors, _ := filepath.Glob(filepath.Join(sessionDir, "tool-results", "search-cursors", "*.json"))
	if len(cursors) != 1 {
		t.Fatalf("expected one materialized search cursor, got %d", len(cursors))
	}

	second, err := kit.Execute(context.Background(), providers.ToolCall{
		Name: "glob",
		Arguments: mustMarshalMap(map[string]any{
			"pattern":           "*.go",
			"offset":            firstParsed.Page.Next.Offset,
			"expected_revision": firstParsed.Page.Next.ExpectedRevision,
		}),
	})
	if err != nil {
		t.Fatalf("second glob: %v", err)
	}
	var secondParsed struct {
		Files   []string `json:"files"`
		Total   int      `json:"total"`
		HasMore bool     `json:"has_more"`
		Offset  int      `json:"offset"`
	}
	if err := json.Unmarshal([]byte(second), &secondParsed); err != nil {
		t.Fatalf("parse second glob response: %v", err)
	}
	if secondParsed.Offset != globPageSize || secondParsed.Total != globPageSize+20 || len(secondParsed.Files) != 20 || secondParsed.HasMore {
		t.Fatalf("second glob page metadata = %+v", secondParsed)
	}
}

func TestToolkit_GrepLargeContentReturnsValidBudgetedJSON(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var content strings.Builder
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&content, "target-%03d %s\n", i, strings.Repeat("x", 500))
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target"}`,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if len(resp) > maxGrepOutputBytes {
		t.Fatalf("grep response length = %d, want <= %d", len(resp), maxGrepOutputBytes)
	}
	var parsed struct {
		Total              int         `json:"total"`
		Truncated          bool        `json:"truncated"`
		Matches            []grepMatch `json:"matches"`
		OmittedMatchCount  int         `json:"omitted_match_count"`
		ReturnedMatchCount int         `json:"returned_match_count"`
		Suggestions        []string    `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("grep response must stay valid JSON after budgeting: %v\n%s", err, resp)
	}
	if !parsed.Truncated || parsed.OmittedMatchCount == 0 || parsed.ReturnedMatchCount != len(parsed.Matches) || parsed.ReturnedMatchCount >= parsed.Total {
		t.Fatalf("unexpected budgeted grep metadata: %+v", parsed)
	}
	if len(parsed.Suggestions) == 0 || !strings.Contains(strings.Join(parsed.Suggestions, " "), "narrow") {
		t.Fatalf("budgeted grep response missing narrowing suggestion: %+v", parsed.Suggestions)
	}
}

func TestGrepRipgrepOversizedRecordDoesNotDeadlock(t *testing.T) {
	if lookupRG() == "" {
		t.Skip("ripgrep is not available")
	}

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "large.txt"), "needle"+strings.Repeat("x", 2*maxRGRecordBytes))
	mustWriteFile(t, filepath.Join(root, "normal.txt"), "needle after oversized record\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	matches, err := grepWithRipgrep(ctx, nil, root, "needle", root, "", grepOptions{}, 0)
	if err != nil {
		t.Fatalf("grep oversized record: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("grep oversized matches = %+v, want placeholder and normal match", matches)
	}
	if matches[0].File != "large.txt" || !matches[0].ContentTruncated || !strings.Contains(matches[0].Content, "match omitted") {
		t.Fatalf("oversized match placeholder = %+v", matches[0])
	}
	if matches[1].File != "normal.txt" || matches[1].Line != 1 || matches[1].ContentTruncated {
		t.Fatalf("normal match after oversized record = %+v", matches[1])
	}
	result, err := grepContentResultJSON("needle", matches, 0, "revision")
	if err != nil {
		t.Fatalf("build oversized grep result: %v", err)
	}
	var envelope struct {
		ContentTruncated bool        `json:"content_truncated"`
		Matches          []grepMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		t.Fatalf("parse oversized grep result: %v", err)
	}
	if !envelope.ContentTruncated || len(envelope.Matches) != 2 || !envelope.Matches[0].ContentTruncated {
		t.Fatalf("oversized grep envelope = %+v", envelope)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("grep oversized record took %s; child process likely blocked on a full output pipe", elapsed)
	}
}

func TestGrepRipgrepSkipsOversizedContextRecord(t *testing.T) {
	if lookupRG() == "" {
		t.Skip("ripgrep is not available")
	}

	root := t.TempDir()
	content := "needle first\n" + strings.Repeat("x", 2*maxRGRecordBytes) + "\nneedle last\n"
	mustWriteFile(t, filepath.Join(root, "context.txt"), content)

	matches, err := grepWithRipgrep(context.Background(), nil, root, "needle", root, "", grepOptions{context: 1}, 0)
	if err != nil {
		t.Fatalf("grep oversized context record: %v", err)
	}
	if len(matches) != 2 || matches[0].Line != 1 || matches[1].Line != 3 {
		t.Fatalf("matches around oversized context = %+v, want lines 1 and 3", matches)
	}
	for _, match := range matches {
		if match.ContentTruncated {
			t.Fatalf("oversized context should not create a match placeholder: %+v", matches)
		}
	}
}

func TestToolkit_GlobRipgrepFirst(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	files := map[string]string{
		"src/app/main.ts": "export const main = true\n",
		"src/lib/util.ts": "export const util = true\n",
		"src/lib/util.js": "export const util = true\n",
		"README.md":       "# readme\n",
	}
	for path, content := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"*.md"}`,
	})
	if err != nil {
		t.Fatalf("glob *.md: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	if len(parsed.Files) != 1 || parsed.Files[0] != "README.md" {
		t.Fatalf("unexpected matches for *.md: %+v", parsed.Files)
	}

	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"src/**/*.ts"}`,
	})
	if err != nil {
		t.Fatalf("glob src/**/*.ts: %v", err)
	}
	parsed.Files = nil
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	want := []string{"src/app/main.ts", "src/lib/util.ts"}
	if !reflect.DeepEqual(parsed.Files, want) {
		t.Fatalf("unexpected matches for src/**/*.ts: got %+v want %+v", parsed.Files, want)
	}
}

func TestToolkit_GlobFallbackWithoutRG(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	withRGTestHooks(t, func(string) (string, error) { return "", exec.ErrNotFound }, nil)

	for path, content := range map[string]string{
		"src/app/main.ts": "main\n",
		"src/app/main.js": "main\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"src/**/*.ts"}`,
	})
	if err != nil {
		t.Fatalf("glob fallback: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	if !reflect.DeepEqual(parsed.Files, []string{"src/app/main.ts"}) {
		t.Fatalf("unexpected fallback matches: %+v", parsed.Files)
	}
}

func TestToolkit_GlobFallbackIncludesHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	withRGTestHooks(t, func(string) (string, error) { return "", exec.ErrNotFound }, nil)

	for path, content := range map[string]string{
		".hidden/config/app.yaml": "name: hidden\n",
		"visible/config/app.yaml": "name: visible\n",
		".git/config/app.yaml":    "name: skipped\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "glob",
		Arguments: `{"pattern":"**/*.yaml"}`,
	})
	if err != nil {
		t.Fatalf("glob hidden fallback: %v", err)
	}
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse glob response: %v", err)
	}
	want := []string{".hidden/config/app.yaml", "visible/config/app.yaml"}
	if !reflect.DeepEqual(parsed.Files, want) {
		t.Fatalf("unexpected hidden fallback matches: got %+v want %+v", parsed.Files, want)
	}
}

func TestToolkit_GrepFallbackIncludesHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	withRGTestHooks(t, func(string) (string, error) { return "", exec.ErrNotFound }, nil)

	for path, content := range map[string]string{
		".hidden/app.env":    "API_KEY=hidden\n",
		"visible/app.env":    "API_KEY=visible\n",
		"node_modules/x.env": "API_KEY=skipped\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"API_KEY","include":"**/*.env"}`,
	})
	if err != nil {
		t.Fatalf("grep hidden fallback: %v", err)
	}
	var parsed struct {
		Matches []grepMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	want := []grepMatch{
		{File: ".hidden/app.env", Line: 1, Content: "[REDACTED: sensitive file content]"},
		{File: "visible/app.env", Line: 1, Content: "[REDACTED: sensitive file content]"},
	}
	if !reflect.DeepEqual(parsed.Matches, want) {
		t.Fatalf("unexpected hidden grep fallback matches: got %+v want %+v", parsed.Matches, want)
	}
	if strings.Contains(resp, "API_KEY=hidden") || strings.Contains(resp, "API_KEY=visible") {
		t.Fatalf("grep fallback response leaked sensitive file content: %s", resp)
	}
}

func TestToolkit_GrepIncludeMatchesRelativePaths_Ripgrep(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	files := map[string]string{
		"internal/a.go":   "package internal\nvar target = true\n",
		"internal/a.txt":  "target\n",
		"src/app/main.ts": "const target = true;\n",
		"src/app/util.js": "const target = true;\n",
	}
	for path, content := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target","include":"internal/*.go"}`,
	})
	if err != nil {
		t.Fatalf("grep internal/*.go: %v", err)
	}
	var parsed struct {
		Matches []grepMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	if len(parsed.Matches) != 1 || parsed.Matches[0].File != "internal/a.go" {
		t.Fatalf("unexpected matches for internal/*.go: %+v", parsed.Matches)
	}

	resp, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"target","include":"src/**/*.ts"}`,
	})
	if err != nil {
		t.Fatalf("grep src/**/*.ts: %v", err)
	}
	parsed.Matches = nil
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse grep response: %v", err)
	}
	if len(parsed.Matches) != 1 || parsed.Matches[0].File != "src/app/main.ts" {
		t.Fatalf("unexpected matches for src/**/*.ts: %+v", parsed.Matches)
	}
}

func TestToolkit_RetiredMemoryToolsAreNotRegistered(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	definitions := definitionNames(kit.Definitions())
	for _, name := range []string{"read_memory", "write_memory"} {
		if definitions[name] {
			t.Fatalf("retired tool %q must not appear in Definitions", name)
		}
		if _, ok := kit.ToolInfo(name); ok {
			t.Fatalf("retired tool %q must not appear in the registry", name)
		}
		if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
			t.Fatalf("executing retired tool %q should fail as unknown, got %v", name, err)
		}
	}
}
