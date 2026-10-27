package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Temporary, opt-in evidence for native listing waits. Remove after a blocked
// process sample and these phases identify the wait; this is not a timeout/fix.
// A private OS-temp sink avoids adding WUU_HOME I/O to the suspected read path.
// Never write arguments, paths, content, error text, or provider wire payloads.
// Synchronous, non-fsync writes can perturb timings; corroborate with a sample.
const listFilesTimingEventLimit = 16384

type listFilesTimingRecorderKey struct{}
type listFilesTimingContextKey struct{}

type listFilesTimingRecorder struct {
	mu        sync.Mutex
	writer    io.Writer
	pid       int
	limit     int
	count     int
	reserved  int
	next      uint64
	failed    bool
	exhausted bool
}

type listFilesTiming struct {
	recorder   *listFilesTimingRecorder
	ctx        context.Context
	id         uint64
	callHash   string
	started    time.Time
	stopCancel func() bool
	cancelDone chan struct{}
	// The recorder mutex also protects cancellation and budget state.
	finished bool
	canceled bool
	dropped  uint64
}

type listFilesTimingSpan struct {
	trace   *listFilesTiming
	phase   string
	index   int
	started time.Time
}

type listFilesTimingEvent struct {
	PID          int       `json:"pid"`
	Trace        uint64    `json:"trace"`
	CallHash     string    `json:"call_hash,omitempty"`
	At           time.Time `json:"at"`
	ElapsedUS    int64     `json:"elapsed_us"`
	Event        string    `json:"event"`
	Phase        string    `json:"phase"`
	Index        int       `json:"index"`
	DurationUS   int64     `json:"duration_us,omitempty"`
	Outcome      string    `json:"outcome,omitempty"`
	Canceled     bool      `json:"canceled,omitempty"`
	ContextState string    `json:"context_state,omitempty"`
	Dropped      uint64    `json:"dropped,omitempty"`
}

var defaultListFilesTiming struct {
	once     sync.Once
	recorder *listFilesTimingRecorder
}

func newListFilesTimingRecorder() *listFilesTimingRecorder {
	pid := os.Getpid()
	file, err := os.CreateTemp("", fmt.Sprintf("wuu-list-files-timing-%d-*.jsonl", pid))
	if err != nil {
		return nil
	}
	return &listFilesTimingRecorder{writer: file, pid: pid, limit: listFilesTimingEventLimit}
}

func beginListFilesTiming(ctx context.Context, toolName, callID string) (context.Context, *listFilesTiming, bool) {
	if toolName != "list_files" {
		return ctx, nil, false
	}
	value := os.Getenv("WUU_LIST_FILES_TIMING")
	if value != "1" && !strings.EqualFold(value, "true") {
		return ctx, nil, false
	}
	if trace := listFilesTimingFromContext(ctx, toolName); trace != nil {
		return ctx, trace, false
	}
	r, _ := ctx.Value(listFilesTimingRecorderKey{}).(*listFilesTimingRecorder)
	if r == nil {
		defaultListFilesTiming.once.Do(func() { defaultListFilesTiming.recorder = newListFilesTimingRecorder() })
		r = defaultListFilesTiming.recorder
	}
	if r == nil {
		return ctx, nil, false
	}
	r.mu.Lock()
	// Reserve cancellation and call-end records before admission, plus one
	// process-wide truncation marker even when the next call cannot start.
	// Every admitted phase similarly reserves its own end, so truncation alone
	// cannot masquerade as an in-flight operation.
	if r.failed || r.exhausted || r.count+r.reserved+4 > r.limit {
		r.limitLocked()
		r.mu.Unlock()
		return ctx, nil, false
	}
	r.next++
	trace := &listFilesTiming{recorder: r, ctx: ctx, id: r.next, started: time.Now(), cancelDone: make(chan struct{})}
	if callID != "" {
		sum := sha256.Sum256([]byte(callID))
		trace.callHash = hex.EncodeToString(sum[:8])
	}
	r.reserved += 2
	trace.writeLocked("start", "call", -1, time.Time{}, nil, false)
	r.mu.Unlock()
	// This observes cancellation only. Filesystem work stays on its original
	// goroutine and must return before the tool returns. finish drains this
	// observer, including when cancellation races the tool's return.
	trace.stopCancel = context.AfterFunc(ctx, func() {
		r.mu.Lock()
		trace.cancelLocked()
		r.mu.Unlock()
		close(trace.cancelDone)
	})
	return context.WithValue(ctx, listFilesTimingContextKey{}, trace), trace, true
}

func listFilesTimingFromContext(ctx context.Context, toolName string) *listFilesTiming {
	if toolName != "list_files" {
		return nil
	}
	trace, _ := ctx.Value(listFilesTimingContextKey{}).(*listFilesTiming)
	return trace
}

func (t *listFilesTiming) start(phase string, index int) listFilesTimingSpan {
	if t == nil {
		return listFilesTimingSpan{}
	}
	r := t.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed || r.exhausted || r.count+r.reserved+3 > r.limit {
		t.dropped++
		r.limitLocked()
		return listFilesTimingSpan{}
	}
	r.reserved++
	started := time.Now()
	t.writeLocked("start", phase, index, time.Time{}, nil, false)
	return listFilesTimingSpan{trace: t, phase: phase, index: index, started: started}
}

func (s listFilesTimingSpan) end(err error) {
	s.endResult(err, false)
}

func (s listFilesTimingSpan) endResult(err error, isError bool) {
	if s.trace == nil {
		return
	}
	r := s.trace.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reserved--
	s.trace.writeLocked("end", s.phase, s.index, s.started, err, isError)
}

func (t *listFilesTiming) cancelLocked() {
	if t.canceled || t.finished {
		return
	}
	t.canceled = true
	t.recorder.reserved--
	t.writeLocked("cancel", "call", -1, time.Time{}, t.ctx.Err(), false)
}

func (t *listFilesTiming) finish(ctx context.Context, err error) {
	t.finishResult(ctx, err, false)
}

func (t *listFilesTiming) finishResult(ctx context.Context, err error, isError bool) {
	if t == nil {
		return
	}
	if !t.stopCancel() {
		<-t.cancelDone
	}
	r := t.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		t.cancelLocked()
	}
	if !t.canceled {
		r.reserved--
	}
	r.reserved--
	t.writeLocked("end", "call", -1, t.started, err, isError)
	t.finished = true
}

// The sink is bounded by event count with fixed-size metadata fields. A write
// failure disables further output, never changes a tool result, and is never
// sent to stderr (the desktop may interpret stderr as a user-facing error).
func (t *listFilesTiming) writeLocked(event, phase string, index int, started time.Time, err error, isError bool) {
	r := t.recorder
	if r.failed {
		return
	}
	at := time.Now()
	contextErr := t.ctx.Err()
	record := listFilesTimingEvent{
		PID: r.pid, Trace: t.id, CallHash: t.callHash, At: at.UTC(), ElapsedUS: at.Sub(t.started).Microseconds(),
		Event: event, Phase: phase, Index: index, Canceled: contextErr != nil,
	}
	if !started.IsZero() {
		record.DurationUS = at.Sub(started).Microseconds()
	}
	switch contextErr {
	case context.Canceled:
		record.ContextState = "canceled"
	case context.DeadlineExceeded:
		record.ContextState = "deadline"
	}
	if event == "end" {
		record.Outcome = "ok"
		if err != nil || isError {
			record.Outcome = "error"
		}
		if errors.Is(err, context.Canceled) {
			record.Outcome = "canceled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			record.Outcome = "deadline"
		}
	}
	if phase == "call" && event == "end" {
		record.Dropped = t.dropped
	}
	r.writeLocked(record)
}

func (r *listFilesTimingRecorder) limitLocked() {
	if r.failed || r.exhausted {
		return
	}
	r.exhausted = true
	r.writeLocked(listFilesTimingEvent{PID: r.pid, At: time.Now().UTC(), Event: "limit", Phase: "process", Index: -1})
}

func (r *listFilesTimingRecorder) writeLocked(record listFilesTimingEvent) {
	data, _ := json.Marshal(record)
	data = append(data, '\n')
	n, writeErr := r.writer.Write(data)
	r.count++
	if writeErr != nil || n != len(data) {
		r.failed = true
	}
}
