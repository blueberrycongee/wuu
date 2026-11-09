package host

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

func TestSessionLineCompressionAndLegacyFallback(t *testing.T) {
	line, _ := json.Marshal(map[string]any{"id": "workspace", "result": string(bytes.Repeat([]byte("history"), 100000))})
	msg := wire.E2EMsg{T: wire.E2ERPC, Seq: 7, Line: line}
	for _, enabled := range []bool{false, true} {
		data, err := encodeSessionMessage(msg, enabled)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := wire.DecodeE2E(data)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Seq != 7 {
			t.Fatal("sequence changed")
		}
		if !enabled {
			if !bytes.Equal(decoded.Line, line) || decoded.LineGzip != "" {
				t.Fatal("legacy encoding changed")
			}
			continue
		}
		if len(data) >= len(line)/2 || len(decoded.Line) != 0 {
			t.Fatal("large line was not reduced")
		}
		compressed, err := base64.RawURLEncoding.DecodeString(decoded.LineGzip)
		if err != nil {
			t.Fatal(err)
		}
		z, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(z)
		if err != nil || !bytes.Equal(raw, line) {
			t.Fatal("line did not round trip", err)
		}
	}
	data, err := encodeSessionMessage(wire.E2EMsg{T: wire.E2ERPC, Line: json.RawMessage(`{"delta":"next"}`)}, true)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := wire.DecodeE2E(data)
	if decoded.LineGzip != "" || string(decoded.Line) != `{"delta":"next"}` {
		t.Fatal("small deltas should stay raw")
	}
}
