package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
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
