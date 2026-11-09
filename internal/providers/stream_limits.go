package providers

import (
	"crypto/sha256"
	"fmt"
)

// MaxStreamEventBytes bounds a decoded SSE data payload or WebSocket message.
// Large tool arguments and image-bearing events need more than a token-sized
// buffer, but an unbounded upstream response must not exhaust local memory.
const MaxStreamEventBytes = 16 << 20

// JSON formatting never needs an unbounded whitespace run. String values are
// excluded so file contents, generated fixtures, and escaped code stay lossless.
const maxToolArgumentWhitespaceBytes = 16 << 10

// ToolArgumentWhitespaceError retains structural evidence without argument text.
// Offsets are byte offsets in the normalized arguments, not provider tokens.
type ToolArgumentWhitespaceError struct {
	ArgumentBytes int
	ChunkOffset   int
	RunBytes      int
}

func (*ToolArgumentWhitespaceError) Error() string {
	return fmt.Sprintf("tool arguments exceeded %d consecutive JSON whitespace bytes; generation stopped before tool admission", maxToolArgumentWhitespaceBytes)
}

type toolArgumentWhitespace struct {
	openCalls                      int
	interleaved, inString, escaped bool
	run                            int
	bytes                          int
	callID, itemID, name           string
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
			s.callID, s.itemID, s.name = event.ToolCall.ID, event.ToolCall.ProviderItemID, event.ToolCall.Name
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
			err := completed.scan(event.ToolCall.Arguments)
			if err != nil {
				s.run, s.bytes = completed.run, completed.bytes
				s.callID, s.itemID, s.name = event.ToolCall.ID, event.ToolCall.ProviderItemID, event.ToolCall.Name
			}
			return err
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
		s.bytes++
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
				return &ToolArgumentWhitespaceError{ArgumentBytes: s.bytes, ChunkOffset: i, RunBytes: s.run}
			}
		default:
			s.run = 0
			s.inString = ch == '"'
		}
	}
	return nil
}

// logFailure captures only counters and a fingerprint, never tool arguments.
func (s *toolArgumentWhitespace) logFailure(event StreamEvent, before toolArgumentWhitespace, operationID, attemptID string) {
	chunk := event.Content
	if event.Type != EventToolUseDelta {
		before.inString, before.escaped, before.run = false, false, 0
	}
	if event.Type != EventToolUseDelta && event.ToolCall != nil {
		chunk = event.ToolCall.Arguments
	}
	var spaces, tabs, newlines, returns, quotes, slashes int
	for i := 0; i < len(chunk); i++ {
		switch chunk[i] {
		case ' ':
			spaces++
		case '\t':
			tabs++
		case '\n':
			newlines++
		case '\r':
			returns++
		case '"':
			quotes++
		case '\\':
			slashes++
		}
	}
	DebugLogf("tool_argument_whitespace operation=%q attempt=%q call=%q item=%q name=%q event=%q open_calls=%d interleaved=%v in_string_before=%v escaped_before=%v run_before=%d run=%d scanned_bytes=%d chunk_bytes=%d spaces=%d tabs=%d newlines=%d returns=%d quotes=%d backslashes=%d chunk_sha256=%x",
		operationID, attemptID, s.callID, s.itemID, s.name, event.Type, s.openCalls, s.interleaved, before.inString, before.escaped, before.run, s.run, s.bytes, len(chunk), spaces, tabs, newlines, returns, quotes, slashes, sha256.Sum256([]byte(chunk)))
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
