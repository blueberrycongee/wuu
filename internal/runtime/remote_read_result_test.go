package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestSanitizeRemoteReadResultRejectsOtherContent(t *testing.T) {
	data, _ := json.Marshal(map[string]any{"text": "source\n", "path": "file.txt", "start_line": 1, "num_lines": 1, "total_lines": 1})
	view, _ := json.Marshal(map[string]any{"action": "read", "path": "file.txt", "start_line": 1, "num_lines": 1, "total_lines": 1})
	native := toolresult.Result{Content: []toolresult.ContentPart{{Type: "text", Text: string(view)}}, StructuredContent: data}
	out, err := sanitizeRemoteReadResult(native)
	require.NoError(t, err)
	require.Empty(t, out.Content)
	require.JSONEq(t, `{"text":"source\n","path":"file.txt","start_line":1,"num_lines":1,"total_lines":1}`, string(out.StructuredContent))
	mixed := native
	mixed.Content = append(append([]toolresult.ContentPart{}, native.Content...), toolresult.ContentPart{Type: "image", MIMEType: "image/png", Data: "aGVsbG8="})
	_, err = sanitizeRemoteReadResult(mixed)
	require.Error(t, err)
	invalid := native
	invalid.Content = []toolresult.ContentPart{{Type: "image", MIMEType: "image/png", Data: "aGVsbG8="}}
	_, err = sanitizeRemoteReadResult(invalid)
	require.Error(t, err)
	bad := native
	bad.Content = []toolresult.ContentPart{{Type: "text", Text: `{"action":"read","path":"other","start_line":1,"num_lines":1,"total_lines":1}`}}
	_, err = sanitizeRemoteReadResult(bad)
	require.Error(t, err)
	huge := native
	huge.StructuredContent = json.RawMessage(`{"text":"` + strings.Repeat("x", 256*1024+1) + `","path":"file.txt","start_line":1,"num_lines":1,"total_lines":1}`)
	_, err = sanitizeRemoteReadResult(huge)
	require.Error(t, err)
}

func TestRemoteProcessScopeUsesCapturedExecutor(t *testing.T) {
	original := newRuntimeProcessEnvironment(t, "original")
	replacement := newRuntimeProcessEnvironment(t, "replacement")
	scope := tools.NewProcessScope(tools.ProcessScopeConfig{RootDir: "/host", ThreadID: "thread", ActorID: "actor", ActorPath: "/root/actor", ExecutionEnvironment: replacement})
	// Simulate the selected environment changing after the host checked it but
	// before it built the current permission scope.
	_, err := scope.WithExecutionEnvironment(original, "actor", "/root/other")
	require.Error(t, err)
	scope, err = scope.WithExecutionEnvironment(original, "actor", "/root/actor")
	require.NoError(t, err)
	bound, err := scope.Bind("thread", "actor", "/host")
	require.NoError(t, err)
	location, err := bound.Environment()
	require.NoError(t, err)
	require.Equal(t, "original", location.Identity)
	require.Equal(t, original.Root(), location.Root)
	_, err = scope.Bind("thread", "other", "/host")
	require.Error(t, err)
}

func TestRemoteReadSnapshotRequiresConsistentWorkerResult(t *testing.T) {
	digest := strings.Repeat("a", 64)
	view, _ := json.Marshal(map[string]any{"action": "read", "path": "a.txt", "start_line": 1, "num_lines": 2, "total_lines": 2, "content_sha256": digest})
	data, _ := json.Marshal(map[string]any{"text": "one\ntwo\n", "path": "a.txt", "start_line": 1, "num_lines": 2, "total_lines": 2, "content_sha256": digest})
	result := toolresult.Result{Content: []toolresult.ContentPart{{Type: "text", Text: string(view)}}, StructuredContent: data}
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}
	got, total, err := remoteReadSnapshot(result, call)
	require.NoError(t, err)
	require.Equal(t, digest, got)
	require.Equal(t, 2, total)
	call.Arguments = `{"path":"a.txt","expected_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`
	_, _, err = remoteReadSnapshot(result, call)
	require.Error(t, err)
	call.Arguments = `{"path":"other.txt"}`
	_, _, err = remoteReadSnapshot(result, call)
	require.Error(t, err)
	call.Arguments = `{"path":"a.txt","offset":2}`
	_, _, err = remoteReadSnapshot(result, call)
	require.Error(t, err)
	bad := result
	bad.StructuredContent = json.RawMessage(`{"text":"one\n","path":"a.txt","start_line":1,"num_lines":2,"total_lines":2,"content_sha256":"` + digest + `"}`)
	call.Arguments = `{"path":"a.txt"}`
	_, _, err = remoteReadSnapshot(bad, call)
	require.Error(t, err)
}
