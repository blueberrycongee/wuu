package host

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"

	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

// Each line has its own dictionary so replay and reconnect never depend on
// compression state from a previous connection. Small streaming deltas stay raw.
func encodeSessionMessage(msg wire.E2EMsg, compress bool) ([]byte, error) {
	if compress && msg.T == wire.E2ERPC && len(msg.Line) >= 4096 && len(msg.Line) <= maxAppLineBytes {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err := z.Write(msg.Line); err != nil {
			return nil, err
		}
		if err := z.Close(); err != nil {
			return nil, err
		}
		if base64.RawURLEncoding.EncodedLen(buf.Len())+32 < len(msg.Line) {
			msg.LineGzip = base64.RawURLEncoding.EncodeToString(buf.Bytes())
			msg.Line = nil
		}
	}
	return wire.EncodeE2E(msg)
}
