package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestRPCTimingDisabledIsAllocationFree(t *testing.T) {
	t.Setenv("WUU_RPC_TIMING", "")
	if got := newRPCTimingRecorder(); got != nil {
		t.Fatal("RPC timing must be opt-in")
	}
	var recorder *rpcTimingRecorder
	var timing *rpcRequestTiming
	if allocations := testing.AllocsPerRun(100, func() {
		start := recorder.now()
		if !start.IsZero() {
			panic("disabled diagnostics sampled a clock")
		}
		timing = recorder.begin(Request{Method: MethodTurnQueue}, start, time.Time{})
		timing = timing.forThread("private-thread")
		timing = timing.buffered()
		timing.mark("queue_persist_begin")
		timing.span("queue_persist", start)
		recorder.responseWritten(json.RawMessage(`"private-request"`), start, nil)
		timing.flush()
		recorder.flushResponses()
	}); allocations != 0 {
		t.Fatalf("disabled diagnostics allocated: %v", allocations)
	}
	if timing != nil {
		t.Fatal("disabled diagnostics retained request state")
	}
}

func TestRPCTimingQueueProtocolIsCorrelatedAndPrivate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			t.Setenv("WUU_RPC_TIMING", fmt.Sprint(enabled))
			rt := newTestRuntime(t, &fakeClient{})
			out := &lockedBuffer{}
			server := New(rt, out)
			t.Cleanup(server.Close)
			logs := &lockedBuffer{}
			const threadID, requestID, prompt = "private-thread-marker", "private-request-marker", "private-prompt-token-secret"
			if enabled {
				if server.rpcTiming == nil {
					t.Fatal("opt-in timing was not enabled")
				}
				server.rpcTiming.logf = func(format string, args ...any) {
					if server.queuedTurnMu.TryLock() {
						server.queuedTurnMu.Unlock()
					} else {
						t.Error("diagnostic IO occurred while the queue mutex was held")
					}
					if th := server.thread(threadID); th != nil {
						if th.mu.TryLock() {
							th.mu.Unlock()
						} else {
							t.Error("diagnostic IO occurred while the thread mutex was held")
						}
					}
					fmt.Fprintf(logs, format+"\n", args...)
				}
			} else if server.rpcTiming != nil {
				t.Fatal("disabled timing installed a recorder")
			}
			if _, err := rt.Sessions.CreateWithMetadata(threadID, rt.RootDir); err != nil {
				t.Fatal(err)
			}
			wire := fmt.Sprintf(`{"id":%q,"method":"turn/queue","params":{"thread_id":%q,"prompt":%q,"client_id":"private-client-marker","hold":true}}`+"\n", requestID, threadID, prompt)
			if err := runStdioScanner(context.Background(), server, strings.NewReader(wire)); err != nil {
				t.Fatal(err)
			}
			response := responseByID(t, parseOutput(t, out.String()), requestID)
			if response["error"] != nil {
				t.Fatalf("timing changed queue acceptance: %+v", response)
			}
			held, err := rt.Sessions.LoadHeldUserWork(threadID)
			if err != nil || len(held) != 1 {
				t.Fatalf("timing changed durable acceptance: %+v, %v", held, err)
			}
			if !enabled {
				if logs.String() != "" {
					t.Fatal("disabled diagnostics emitted records")
				}
				return
			}
			for _, secret := range []string{threadID, requestID, prompt, "private-client-marker", rt.RootDir} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("RPC timing leaked private input %q", secret)
				}
			}
			var phases []string
			var events []rpcTimingEvent
			var correlation string
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var event rpcTimingEvent
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "rpc_timing ")), &event); err != nil {
					t.Fatal(err)
				}
				if event.PID <= 0 || len(event.RequestHash) != 16 || event.AtMS < 0 || event.DurationMS < 0 {
					t.Fatalf("invalid diagnostic envelope: %+v", event)
				}
				if correlation == "" {
					correlation = event.RequestHash
				} else if event.RequestHash != correlation {
					t.Fatalf("request correlation changed: %+v", event)
				}
				events = append(events, event)
			}
			sort.Slice(events, func(i, j int) bool { return events[i].AtMS < events[j].AtMS })
			for _, event := range events {
				phases = append(phases, event.Phase)
			}
			position := -1
			for _, want := range []string{"stdin_read", "handler_start", "queue_persist_begin", "queue_persist", "response_written", "handler_end"} {
				found := -1
				for index := position + 1; index < len(phases); index++ {
					if phases[index] == want {
						found = index
						break
					}
				}
				if found == -1 {
					t.Fatalf("missing or reordered phase %s: %v", want, phases)
				}
				position = found
			}
		})
	}
}

func TestRPCTimingUnknownMethodDoesNotExposeCallerText(t *testing.T) {
	t.Setenv("WUU_RPC_TIMING", "1")
	recorder := newRPCTimingRecorder()
	logs := &lockedBuffer{}
	recorder.logf = func(format string, args ...any) { fmt.Fprintf(logs, format+"\n", args...) }
	recorder.begin(Request{ID: json.RawMessage(`"secret-id"`), Method: "secret-method-text"}, recorder.now(), time.Time{}).mark("handler_end")
	if strings.Contains(logs.String(), "secret") || !strings.Contains(logs.String(), `"method":"other"`) {
		t.Fatalf("unknown method privacy boundary failed: %s", logs.String())
	}
}

func TestRPCTimingStartAndInterruptKeepTheirOwnCorrelation(t *testing.T) {
	t.Setenv("WUU_RPC_TIMING", "1")
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	attachTestModelGeneration(t, rt)
	out, logs := &lockedBuffer{}, &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(client.release) })
	server.rpcTiming.logf = func(format string, args ...any) { fmt.Fprintf(logs, format+"\n", args...) }
	sess, err := rt.Sessions.CreateWithMetadata("timed-running-thread", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	start := fmt.Sprintf(`{"id":"start","method":"turn/start","params":{"thread_id":%q,"prompt":"synthetic input"}}`, sess.ID)
	if err := server.handleLine(context.Background(), []byte(start)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed start did not launch the provider")
	}
	stop := fmt.Sprintf(`{"id":"stop","method":"turn/interrupt","params":{"thread_id":%q}}`, sess.ID)
	if err := server.handleLine(context.Background(), []byte(stop)); err != nil {
		t.Fatal(err)
	}
	require.Eventually(t, func() bool { return !threadIsRunning(server.thread(sess.ID)) }, 5*time.Second, 5*time.Millisecond)
	for _, id := range []string{"start", "stop"} {
		if response := responseByID(t, parseOutput(t, out.String()), id); response["error"] != nil {
			t.Fatalf("timing changed %s: %+v", id, response)
		}
	}
	expected := map[string]string{
		"admission_begin": "start", "admission_user_persist": "start", "admission_committed": "start", "turn_launch_committed": "start",
		"interrupt_state": "stop", "interrupt_hold_inputs": "stop", "interrupt_cancel_called": "stop", "interrupt_stop_processes": "stop",
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var event rpcTimingEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "rpc_timing ")), &event); err != nil {
			t.Fatal(err)
		}
		if requestID, ok := expected[event.Phase]; ok {
			id, _ := json.Marshal(requestID)
			if event.RequestHash != rpcTimingHash(id) || event.ThreadHash != rpcTimingHash([]byte(sess.ID)) {
				t.Fatalf("phase attached to another request: %+v", event)
			}
			delete(expected, event.Phase)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing admission or cancellation evidence: %v", expected)
	}
}

func TestRPCTimingDefaultSinkDoesNotNeedWireLogging(t *testing.T) {
	if os.Getenv("WUU_RPC_TIMING_SINK_TEST") == "1" {
		if providers.WireEnabled() {
			t.Fatal("wire logging must remain disabled")
		}
		// Runtime startup initializes this log unconditionally. Exercise the
		// real default sink in a subprocess because its lifetime is process-wide.
		providers.InitDebugLog(os.Getenv("WUU_RPC_TIMING_TEST_LOG_DIR"))
		recorder := newRPCTimingRecorder()
		if recorder == nil {
			t.Fatal("timing opt-in missing")
		}
		recorder.begin(Request{ID: json.RawMessage(`"private-id"`), Method: MethodThreadResume}, recorder.now(), time.Time{}).mark("handler_end")
		return
	}
	dir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestRPCTimingDefaultSinkDoesNotNeedWireLogging$")
	command.Env = append(os.Environ(), "WUU_RPC_TIMING=1", "WUU_DEBUG_WIRE=", "WUU_RPC_TIMING_SINK_TEST=1", "WUU_RPC_TIMING_TEST_LOG_DIR="+dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("timing sink subprocess: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `rpc_timing {`) || !strings.Contains(string(data), `"phase":"handler_end"`) || strings.Contains(string(data), "private-id") {
		t.Fatalf("default metadata log did not preserve the diagnostic privacy contract: %s", data)
	}
}

func TestRPCTimingResponseLogsAfterRendererLockReleased(t *testing.T) {
	t.Setenv("WUU_RPC_TIMING", "1")
	server := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(server.Close)
	logs := &lockedBuffer{}
	server.rpcTiming.logf = func(format string, args ...any) {
		if !server.rendererClients.mu.TryLock() {
			t.Error("diagnostic IO occurred while the renderer scope mutex was held")
		} else {
			server.rendererClients.mu.Unlock()
		}
		fmt.Fprintf(logs, format+"\n", args...)
	}
	// The closed renderer branch writes its response with the renderer mutex held.
	server.rendererClients.closed = true
	wire := fmt.Sprintf(`{"id":"renderer","method":%q,"params":{}}`, MethodPluginDesktopActivation)
	if err := server.handleLine(context.Background(), []byte(wire)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"phase":"response_written"`) {
		t.Fatal("safe-boundary response record was lost")
	}
}

func TestRPCTimingResponseBufferIsBoundedAndKeepsCorrelation(t *testing.T) {
	t.Setenv("WUU_RPC_TIMING", "1")
	recorder := newRPCTimingRecorder()
	logs := &lockedBuffer{}
	recorder.logf = func(format string, args ...any) { fmt.Fprintf(logs, format+"\n", args...) }
	started := recorder.now()
	for i := 0; i < rpcTimingResponseLimit+3; i++ {
		id, _ := json.Marshal(i)
		recorder.responseWritten(id, started, nil)
	}
	if logs.String() != "" {
		t.Fatal("response capture performed diagnostic IO")
	}
	if len(recorder.responses) != rpcTimingResponseLimit {
		t.Fatal("response metadata was not bounded")
	}
	recorder.flushResponses()
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != rpcTimingResponseLimit+1 {
		t.Fatalf("unexpected flushed record count: %d", len(lines))
	}
	for i, line := range lines {
		var event rpcTimingEvent
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "rpc_timing ")), &event))
		if i < rpcTimingResponseLimit {
			id, _ := json.Marshal(i)
			require.Equal(t, rpcTimingHash(id), event.RequestHash)
		} else {
			require.Equal(t, "response_records_dropped", event.Phase)
			require.Equal(t, uint64(3), event.Dropped)
		}
	}
}
