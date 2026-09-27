package providers

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// SSEEvent contains the fields consumed by model streaming protocols.
type SSEEvent struct {
	Event string
	Data  string
}

// SSEReader delivers only blank-line-terminated events. An EOF cannot commit
// a pending event: even valid JSON may be only part of a multi-line payload.
// Protocol-specific completion and retry decisions remain with the caller.
type SSEReader struct {
	scanner *bufio.Scanner
	onLine  func()
	event   SSEEvent
	err     error
}

// NewSSEReader bounds both individual lines and accumulated event data.
// onLine observes heartbeats as well as data, preserving idle timeout behavior.
func NewSSEReader(r io.Reader, onLine func()) *SSEReader {
	scanner := bufio.NewScanner(r)
	// Leave room for the data field prefix, its terminator, and a skipped LF.
	scanner.Buffer(make([]byte, 0, 64*1024), MaxStreamEventBytes+len("data: ")+2)
	skipLF := false
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		skipped := 0
		if skipLF && len(data) > 0 {
			skipLF = false
			if data[0] == '\n' {
				data = data[1:]
				skipped = 1
			}
		}
		if atEOF && len(data) > 0 && !bytes.ContainsAny(data, "\r\n") {
			return 0, nil, NewIncompleteStreamError("stream closed during SSE line")
		}
		// Consume CR immediately so a terminal event does not wait for the
		// next network read. Skip an optional LF even when it arrives later.
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			skipLF = data[i] == '\r'
			return skipped + i + 1, data[:i], nil
		}
		return skipped, nil, nil
	})
	return &SSEReader{scanner: scanner, onLine: onLine}
}

func (r *SSEReader) Scan() bool {
	if r.err != nil {
		return false
	}
	var event SSEEvent
	var data strings.Builder
	hasData := false
	size := 0
	for r.scanner.Scan() {
		if r.onLine != nil {
			r.onLine()
		}
		line := r.scanner.Text()
		if line == "" {
			if hasData {
				event.Data = data.String()
				r.event = event
				return true
			}
			event = SSEEvent{}
			size = 0
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event.Event = value
		case "data":
			size += len(value)
			if hasData {
				size++
			}
			if size > MaxStreamEventBytes {
				r.err = &StreamEventTooLargeError{Transport: "SSE", LimitBytes: MaxStreamEventBytes}
				return false
			}
			if hasData {
				data.WriteByte('\n')
			}
			hasData = true
			data.WriteString(value)
		}
	}
	r.err = r.scanner.Err()
	if errors.Is(r.err, bufio.ErrTooLong) {
		r.err = &StreamEventTooLargeError{Transport: "SSE", LimitBytes: MaxStreamEventBytes}
	}
	if r.err == nil && (hasData || event.Event != "") {
		r.err = NewIncompleteStreamError("stream closed before SSE event delimiter")
	}
	return false
}

func (r *SSEReader) Event() SSEEvent { return r.event }
func (r *SSEReader) Err() error      { return r.err }
