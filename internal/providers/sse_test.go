package providers

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestSSEReaderFragmentedEvents(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(newline, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			wire := strings.Join([]string{": heartbeat", "", "event: delta", `data:{"text":`, `data: "你好"}`, "", "data: [DONE]", "", ""}, newline)
			lines := 0
			reader := NewSSEReader(iotest.OneByteReader(strings.NewReader(wire)), func() { lines++ })
			if !reader.Scan() || reader.Event().Event != "delta" || reader.Event().Data != "{\"text\":\n\"你好\"}" {
				t.Fatalf("event = %+v, err = %v", reader.Event(), reader.Err())
			}
			if !reader.Scan() || reader.Event().Data != "[DONE]" || reader.Event().Event != "" {
				t.Fatalf("terminal = %+v, err = %v", reader.Event(), reader.Err())
			}
			if reader.Scan() || reader.Err() != nil {
				t.Fatalf("unexpected event or error: %v", reader.Err())
			}
			if lines != 8 {
				t.Fatalf("heartbeat/data activity = %d, want 8", lines)
			}
		})
	}
}

func TestSSEReaderRejectsEveryTruncatedEventPrefix(t *testing.T) {
	wire := "event: delta\ndata: {\"text\":\"你好\"}\n\n"
	for cut := 1; cut < len(wire); cut++ {
		reader := NewSSEReader(iotest.OneByteReader(strings.NewReader(wire[:cut])), nil)
		if reader.Scan() {
			t.Fatalf("cut %d emitted incomplete event: %+v", cut, reader.Event())
		}
		if !IsRetryable(reader.Err()) {
			t.Fatalf("cut %d: error %v is not retryable", cut, reader.Err())
		}
	}
}

func TestSSEReaderPreservesReadFailureAfterCompleteEvent(t *testing.T) {
	failure := errors.New("connection reset by peer")
	reader := NewSSEReader(io.MultiReader(strings.NewReader("data: ok\n\ndata: {\"partial\":"), iotest.ErrReader(failure)), nil)
	if !reader.Scan() || reader.Event().Data != "ok" {
		t.Fatal("lost complete event")
	}
	if reader.Scan() || !errors.Is(reader.Err(), failure) {
		t.Fatalf("error = %v", reader.Err())
	}
}

func TestSSEReaderEventSizeBoundary(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		for _, multiline := range []bool{false, true} {
			for _, extra := range []int{0, 1} {
				payload := strings.Repeat("x", MaxStreamEventBytes+extra)
				wire := "data: " + payload + newline + newline
				if multiline {
					mid := len(payload) / 2
					payload = payload[:mid] + "\n" + payload[mid+1:]
					wire = "data: " + payload[:mid] + newline + "data: " + payload[mid+1:] + newline + newline
				}
				reader := NewSSEReader(strings.NewReader(wire+"data: next"+newline+newline), nil)
				if extra == 0 {
					if !reader.Scan() || reader.Event().Data != payload {
						t.Fatalf("boundary event (newline=%q multiline=%v): %v", newline, multiline, reader.Err())
					}
					if !reader.Scan() || reader.Event().Data != "next" || reader.Scan() || reader.Err() != nil {
						t.Fatalf("event after boundary payload: %v", reader.Err())
					}
					continue
				}
				var oversized *StreamEventTooLargeError
				if reader.Scan() || !errors.As(reader.Err(), &oversized) || IsRetryable(reader.Err()) {
					t.Fatalf("oversized event (newline=%q multiline=%v): %v", newline, multiline, reader.Err())
				}
				if oversized.LimitBytes != MaxStreamEventBytes || reader.Scan() {
					t.Fatalf("invalid limit or continued after oversized event: %+v", oversized)
				}
			}
		}
	}
}

func TestSSEReaderBoundsUnterminatedLine(t *testing.T) {
	bytesRead := 0
	reader := NewSSEReader(sseReadFunc(func(p []byte) (int, error) {
		if bytesRead+len(p) > MaxStreamEventBytes+64 {
			t.Fatal("unbounded read of unterminated SSE line")
		}
		for i := range p {
			p[i] = 'x'
		}
		bytesRead += len(p)
		return len(p), nil
	}), nil)
	var oversized *StreamEventTooLargeError
	if reader.Scan() || !errors.As(reader.Err(), &oversized) || IsRetryable(reader.Err()) {
		t.Fatalf("unterminated oversized line: %v", reader.Err())
	}
}

type sseReadFunc func([]byte) (int, error)

func (f sseReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestSSEReaderEmitsBeforeNextRead(t *testing.T) {
	for _, delimiter := range []string{"\n\n", "\r\n\r\n", "\r\r"} {
		reads := 0
		reader := NewSSEReader(sseReadFunc(func(p []byte) (int, error) {
			reads++
			if reads > 1 {
				t.Fatal("complete event waited for another network read")
			}
			return copy(p, "data: [DONE]"+delimiter), nil
		}), nil)
		if !reader.Scan() || reader.Event().Data != "[DONE]" {
			t.Fatalf("event=%+v err=%v", reader.Event(), reader.Err())
		}
	}
}
