package providers

import (
	"errors"
	"fmt"
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

func TestSSEReaderLargeEvents(t *testing.T) {
	chunk := strings.Repeat("x", 768*1024)
	for _, tc := range []struct {
		name string
		wire string
		want string
	}{
		{name: "single line", wire: "data: " + chunk + chunk + "\n\n", want: chunk + chunk},
		{name: "multiple lines", wire: "data: " + chunk + "\ndata: " + chunk + "\n\n", want: chunk + "\n" + chunk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := NewSSEReader(strings.NewReader(tc.wire+"data: [DONE]\n\n"), nil)
			if !reader.Scan() || reader.Event().Data != tc.want {
				t.Fatalf("large event: got %d bytes, err = %v", len(reader.Event().Data), reader.Err())
			}
			if !reader.Scan() || reader.Event().Data != "[DONE]" {
				t.Fatalf("terminal event lost: %v", reader.Err())
			}
			if reader.Scan() || reader.Err() != nil {
				t.Fatalf("unexpected event or error: %v", reader.Err())
			}
		})
	}
}

func TestSSEReaderSizeBoundaries(t *testing.T) {
	payload := strings.Repeat("x", maxSSEEventSize)
	for _, tc := range []struct {
		name  string
		parts []string
		valid bool
	}{
		{name: "single line at limit", parts: []string{"data: ", payload, "\n\n"}, valid: true},
		{name: "multiple lines at limit", parts: []string{"data: ", payload[:len(payload)/2], "\ndata: ", payload[len(payload)/2+1:], "\n\n"}, valid: true},
		{name: "single line over limit", parts: []string{"data: ", payload, "x\n\n"}},
		{name: "line exceeds scanner buffer", parts: []string{"data: ", payload, strings.Repeat("x", 64), "\n\n"}},
		{name: "multiple lines over limit", parts: []string{"data: ", payload[:len(payload)/2], "\ndata: ", payload[len(payload)/2:], "\n\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var parts []io.Reader
			for _, part := range tc.parts {
				parts = append(parts, strings.NewReader(part))
			}
			parts = append(parts, strings.NewReader("data: [DONE]\n\n"))
			reader := NewSSEReader(io.MultiReader(parts...), nil)
			if tc.valid {
				if !reader.Scan() || len(reader.Event().Data) != maxSSEEventSize {
					t.Fatalf("boundary event: got %d bytes, err = %v", len(reader.Event().Data), reader.Err())
				}
				if !reader.Scan() || reader.Event().Data != "[DONE]" {
					t.Fatalf("terminal event lost: %v", reader.Err())
				}
				if reader.Scan() || reader.Err() != nil {
					t.Fatalf("unexpected event or error: %v", reader.Err())
				}
				return
			}
			if reader.Scan() {
				t.Fatal("emitted an oversized event")
			}
			err := fmt.Errorf("stream request failed: read stream: %w", reader.Err())
			var limit *SSESizeLimitError
			if !errors.As(err, &limit) || limit.Limit != maxSSEEventSize || IsRetryable(err) {
				t.Fatalf("oversized event: %v", err)
			}
			if reader.Scan() {
				t.Fatal("continued after an oversized event")
			}
		})
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
