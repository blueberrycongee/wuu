package providers

import "fmt"

// MaxStreamEventBytes bounds a decoded SSE data payload or WebSocket message.
// Large tool arguments and image-bearing events need more than a token-sized
// buffer, but an unbounded upstream response must not exhaust local memory.
const MaxStreamEventBytes = 16 << 20

// JSON formatting never needs an unbounded whitespace run. String values are
// excluded so file contents, generated fixtures, and escaped code stay lossless.
const maxToolArgumentWhitespaceBytes = 16 << 10

type ToolArgumentWhitespaceError struct{}

func (*ToolArgumentWhitespaceError) Error() string {
	return fmt.Sprintf("tool arguments exceeded %d consecutive JSON whitespace bytes; generation stopped before tool admission", maxToolArgumentWhitespaceBytes)
}

type toolArgumentWhitespace struct {
	openCalls                      int
	interleaved, inString, escaped bool
	run                            int
}

func (s *toolArgumentWhitespace) observe(event StreamEvent) error {
	switch event.Type {
	case EventToolUseStart:
		if s.openCalls == 0 {
			*s = toolArgumentWhitespace{}
		}
		s.openCalls++
		// Delta events do not always identify their owner. Once calls overlap,
		// inspect their finalized arguments instead of combining JSON states.
		s.interleaved = s.interleaved || s.openCalls > 1
		if !s.interleaved && event.ToolCall != nil {
			return s.scan(event.ToolCall.Arguments)
		}
	case EventToolUseDelta:
		if s.openCalls > 0 && !s.interleaved {
			return s.scan(event.Content)
		}
	case EventToolUseEnd:
		if s.openCalls > 0 {
			s.openCalls--
		}
		if event.ToolCall != nil {
			var completed toolArgumentWhitespace
			return completed.scan(event.ToolCall.Arguments)
		}
	case EventLifecycle:
		if event.Lifecycle != nil && event.Lifecycle.ResetPartial {
			*s = toolArgumentWhitespace{}
		}
	}
	return nil
}

func (s *toolArgumentWhitespace) scan(delta string) error {
	for i := 0; i < len(delta); i++ {
		ch := delta[i]
		if s.inString {
			if s.escaped {
				s.escaped = false
			} else if ch == '\\' {
				s.escaped = true
			} else if ch == '"' {
				s.inString = false
			}
			continue
		}
		switch ch {
		case ' ', '\n', '\r', '\t':
			s.run++
			if s.run > maxToolArgumentWhitespaceBytes {
				return &ToolArgumentWhitespaceError{}
			}
		default:
			s.run = 0
			s.inString = ch == '"'
		}
	}
	return nil
}

// StreamEventTooLargeError identifies a local receive limit, not a context-token
// overflow or a transient transport failure. Replaying cannot raise the limit.
type StreamEventTooLargeError struct {
	Transport  string
	LimitBytes int
}

func (e *StreamEventTooLargeError) Error() string {
	return fmt.Sprintf("%s response event exceeds local receive limit of %d bytes", e.Transport, e.LimitBytes)
}
