package providers

import "fmt"

// MaxStreamEventBytes bounds a decoded SSE data payload or WebSocket message.
// Large tool arguments and image-bearing events need more than a token-sized
// buffer, but an unbounded upstream response must not exhaust local memory.
const MaxStreamEventBytes = 16 << 20

// StreamEventTooLargeError identifies a local receive limit, not a context-token
// overflow or a transient transport failure. Replaying cannot raise the limit.
type StreamEventTooLargeError struct {
	Transport  string
	LimitBytes int
}

func (e *StreamEventTooLargeError) Error() string {
	return fmt.Sprintf("%s response event exceeds local receive limit of %d bytes", e.Transport, e.LimitBytes)
}
