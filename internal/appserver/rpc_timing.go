package appserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// RPC timing is metadata-only and opt-in. In particular, never enable provider
// wire logging for latency diagnosis: it includes private request/response text.
type rpcTimingRecorder struct {
	epoch            time.Time
	pid              int
	logf             func(string, ...any)
	responseMu       sync.Mutex
	responses        []rpcTimingEvent
	droppedResponses uint64
}

type rpcRequestTiming struct {
	recorder    *rpcTimingRecorder
	started     time.Time
	method      string
	requestHash string
	threadHash  string
	// Only a synchronous critical-section caller owns this buffer. Never attach
	// a buffered copy to a queued turn or launch it in another goroutine.
	events *[]rpcTimingEvent
}

type rpcTimingEvent struct {
	PID         int     `json:"pid"`
	RequestHash string  `json:"request_hash"`
	ThreadHash  string  `json:"thread_hash,omitempty"`
	Method      string  `json:"method,omitempty"`
	Phase       string  `json:"phase"`
	AtMS        float64 `json:"at_ms"`
	ElapsedMS   float64 `json:"elapsed_ms,omitempty"`
	DurationMS  float64 `json:"duration_ms,omitempty"`
	Outcome     string  `json:"outcome,omitempty"`
	Dropped     uint64  `json:"dropped,omitempty"`
}

func newRPCTimingRecorder() *rpcTimingRecorder {
	value := os.Getenv("WUU_RPC_TIMING")
	if value != "1" && !strings.EqualFold(value, "true") {
		return nil
	}
	return &rpcTimingRecorder{epoch: time.Now(), pid: os.Getpid(), logf: providers.DebugLogf}
}

func (r *rpcTimingRecorder) now() time.Time {
	if r == nil {
		return time.Time{}
	}
	return time.Now()
}

// Hash the exact JSON ID bytes. A stdio client can correlate with
// SHA256(JSON.stringify(id)).slice(0, 16); PID disambiguates restarted cores.
func rpcTimingHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:8])
}

func (r *rpcTimingRecorder) begin(req Request, started, readAt time.Time) *rpcRequestTiming {
	if r == nil {
		return nil
	}
	method := "other"
	switch req.Method {
	case MethodInitialize, MethodThreadStart, MethodThreadResume, MethodThreadList,
		MethodTurnStart, MethodTurnQueue, MethodTurnSteer, MethodTurnInterrupt,
		MethodConfigRead, MethodConfigModelUpdate, MethodConfigGeneralUpdate,
		MethodEngineList, MethodConfigCodexModels, MethodPluginPackageInstall,
		MethodPluginPackageRemove, MethodExtensionCatalogRefresh:
		method = req.Method
	}
	timing := &rpcRequestTiming{recorder: r, started: started, method: method, requestHash: rpcTimingHash(req.ID)}
	if !readAt.IsZero() {
		timing.emit("stdin_read", readAt, time.Time{})
	}
	timing.emit("handler_start", started, time.Time{})
	return timing
}

func (t *rpcRequestTiming) forThread(id string) *rpcRequestTiming {
	if t == nil {
		return nil
	}
	copy := *t
	copy.threadHash = rpcTimingHash([]byte(id))
	return &copy
}

func (t *rpcRequestTiming) now() time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.recorder.now()
}

func (t *rpcRequestTiming) buffered() *rpcRequestTiming {
	if t == nil {
		return nil
	}
	copy := *t
	copy.events = new([]rpcTimingEvent)
	return &copy
}

// Call only after releasing the locks whose timing was captured. A local
// buffer keeps diagnostic file IO out of application critical sections.
func (t *rpcRequestTiming) flush() {
	if t == nil || t.events == nil {
		return
	}
	events := *t.events
	*t.events = nil
	for _, event := range events {
		t.recorder.write(event)
	}
}

func (t *rpcRequestTiming) mark(phase string) { t.span(phase, time.Time{}) }

func (t *rpcRequestTiming) span(phase string, started time.Time) {
	if t != nil {
		t.emit(phase, t.recorder.now(), started)
	}
}

func (t *rpcRequestTiming) emit(phase string, at, spanStart time.Time) {
	event := rpcTimingEvent{
		PID: t.recorder.pid, RequestHash: t.requestHash, ThreadHash: t.threadHash,
		Method: t.method, Phase: phase, AtMS: float64(at.Sub(t.recorder.epoch)) / float64(time.Millisecond),
		ElapsedMS: max(0, float64(at.Sub(t.started))/float64(time.Millisecond)),
	}
	if !spanStart.IsZero() {
		event.DurationMS = float64(at.Sub(spanStart)) / float64(time.Millisecond)
	}
	if t.events != nil {
		*t.events = append(*t.events, event)
	} else {
		t.recorder.write(event)
	}
}

func (r *rpcTimingRecorder) responseWritten(id json.RawMessage, started time.Time, err error) {
	if r == nil {
		return
	}
	at := r.now()
	outcome := "written"
	if err != nil {
		outcome = "write_error"
	}
	event := rpcTimingEvent{PID: r.pid, RequestHash: rpcTimingHash(id), Phase: "response_written",
		AtMS:       float64(at.Sub(r.epoch)) / float64(time.Millisecond),
		DurationMS: float64(at.Sub(started)) / float64(time.Millisecond), Outcome: outcome}
	// Callers may still hold application locks. Capture metadata only here.
	r.responseMu.Lock()
	if len(r.responses) < rpcTimingResponseLimit {
		r.responses = append(r.responses, event)
	} else {
		r.droppedResponses++
	}
	r.responseMu.Unlock()
}

// A generic response can be written with a caller's application lock held.
// Drain only at handler/background-task return or Close, after those locks are
// released. No current-request pointer is shared; each event owns its ID hash.
// Bound metadata even if an asynchronous transport has no new handler boundary.
const rpcTimingResponseLimit = 128

func (r *rpcTimingRecorder) flushResponses() {
	if r == nil {
		return
	}
	r.responseMu.Lock()
	events, dropped := r.responses, r.droppedResponses
	r.responses, r.droppedResponses = nil, 0
	r.responseMu.Unlock()
	for _, event := range events {
		r.write(event)
	}
	if dropped != 0 {
		r.write(rpcTimingEvent{PID: r.pid, Phase: "response_records_dropped", Dropped: dropped,
			AtMS: float64(r.now().Sub(r.epoch)) / float64(time.Millisecond)})
	}
}

func (r *rpcTimingRecorder) write(event rpcTimingEvent) {
	data, _ := json.Marshal(event) // Fixed metadata fields cannot fail to encode.
	r.logf("rpc_timing %s", data)
}
