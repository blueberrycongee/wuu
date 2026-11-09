package runtime

import (
	"bytes"
	"io"
)

// Compare a canonical encoding without retaining another complete request.
// Native callers accept legacy field aliases that external SDKs do not, so only
// byte-identical requests may bypass the authority's usual normalization.
type authorityWireMatch struct{ remaining []byte }

func (w *authorityWireMatch) Write(p []byte) (int, error) {
	if len(p) > len(w.remaining) || !bytes.Equal(p, w.remaining[:len(p)]) {
		return 0, io.ErrShortWrite
	}
	w.remaining = w.remaining[len(p):]
	return len(p), nil
}
