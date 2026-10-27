package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Diagnostics must not add filesystem work, allocations, or changed results to
// ordinary tool calls. Only the explicit opt-in may create a private sink.
func TestListFilesTimingDisabled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ctx := context.Background()
	for _, value := range []string{"", "0", "yes"} {
		t.Setenv("WUU_LIST_FILES_TIMING", value)
		if got := testing.AllocsPerRun(100, func() {
			next, trace, owner := beginListFilesTiming(ctx, "list_files", "private-call")
			if next != ctx || trace != nil || owner {
				t.Fatal("disabled timing changed context")
			}
			span := trace.start("stat", -1)
			span.end(nil)
		}); got != 0 {
			t.Fatalf("disabled timing allocated %v times", got)
		}
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("disabled timing touched diagnostic directory: %v, %v", entries, err)
	}
}

func timingTestContext(t *testing.T, limit int) (context.Context, *listFilesTimingRecorder, *bytes.Buffer) {
	t.Helper()
	t.Setenv("WUU_LIST_FILES_TIMING", "1")
	var output bytes.Buffer
	r := &listFilesTimingRecorder{writer: &output, pid: os.Getpid(), limit: limit}
	return context.WithValue(context.Background(), listFilesTimingRecorderKey{}, r), r, &output
}

func timingTestEvents(t *testing.T, r *listFilesTimingRecorder, output *bytes.Buffer) []listFilesTimingEvent {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var events []listFilesTimingEvent
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var event listFilesTimingEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("invalid diagnostic JSON: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func TestListFilesTimingRedactedPhasesAndErrors(t *testing.T) {
	ctx, recorder, output := timingTestContext(t, 512)
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "private-directory", "private-file.txt"), "private file contents")
	call := providers.ToolCall{ID: "private-call-id", Name: "list_files", Arguments: `{"path":"private-directory"}`}
	if _, err := kit.Execute(ctx, call); err != nil {
		t.Fatal(err)
	}
	call.Arguments = `{"path":"private-missing-directory"}`
	if _, err := kit.Execute(ctx, call); err == nil {
		t.Fatal("missing directory unexpectedly succeeded")
	}
	events := timingTestEvents(t, recorder, output)
	for _, secret := range []string{root, "private-directory", "private-file", "private-missing", "private file contents", "private-call-id", call.Arguments} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("diagnostic leaked %q", secret)
		}
	}
	starts := map[string]bool{}
	ends := map[string]bool{}
	traces := map[uint64]bool{}
	for _, event := range events {
		if event.PID != os.Getpid() || event.Trace == 0 || event.CallHash == "" || event.At.IsZero() {
			t.Fatalf("missing correlation/timestamp: %+v", event)
		}
		key := fmt.Sprintf("%d:%s:%d", event.Trace, event.Phase, event.Index)
		if event.Event == "start" {
			starts[key] = true
		}
		if event.Event == "end" {
			ends[key] = true
		}
		if event.Phase == "call" && event.Event == "end" {
			traces[event.Trace] = event.Outcome == "error"
		}
	}
	for key := range starts {
		if !ends[key] {
			t.Errorf("missing phase end: %s", key)
		}
	}
	if len(traces) != 2 || !traces[2] || traces[1] {
		t.Fatalf("wrong call-end outcomes: %v", traces)
	}
	for _, phase := range []string{"permission", "resolve_path", "credential_root_symlinks", "exec_path", "stat", "read_dir", "entry_display_path", "entry_info"} {
		found := false
		for _, event := range events {
			found = found || event.Phase == phase
		}
		if !found {
			t.Errorf("missing actual phase %q", phase)
		}
	}
}

// The start must be visible while the real permission hook is blocked; a
// cancellation marker must not manufacture a successful return or abandon it.
type timingCancellationWriter struct {
	io.Writer
	observed chan struct{}
}

func (w timingCancellationWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if bytes.Contains(data, []byte(`"event":"cancel"`)) {
		close(w.observed)
	}
	return n, err
}

func TestListFilesTimingObservesBlockedPermissionAndCancellation(t *testing.T) {
	ctx, recorder, output := timingTestContext(t, 512)
	cancelObserved := make(chan struct{})
	recorder.writer = timingCancellationWriter{Writer: output, observed: cancelObserved}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	kit.SetPermissionRequestHook(func(context.Context, *Toolkit, ToolInfo, providers.ToolCall) error {
		close(entered)
		<-release
		return nil
	})
	go func() {
		_, err := kit.Execute(ctx, providers.ToolCall{ID: "blocked-call", Name: "list_files", Arguments: `{}`})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("permission hook did not start")
	}
	found := false
	for _, event := range timingTestEvents(t, recorder, output) {
		found = found || event.Phase == "permission_hook" && event.Event == "start"
		if event.Phase == "permission_hook" && event.Event == "end" {
			t.Fatal("blocked hook already has an end")
		}
	}
	if !found {
		t.Fatal("blocked hook start was not persisted")
	}
	cancel()
	select {
	case <-cancelObserved:
	case <-time.After(10 * time.Second):
		close(release)
		<-done
		t.Fatal("cancellation was not recorded while the hook remained blocked")
	}
	select {
	case <-done:
		t.Fatal("diagnostics abandoned the blocked hook")
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call returned %v", err)
	}
	canceled, ended := 0, false
	for _, event := range timingTestEvents(t, recorder, output) {
		if event.Event == "cancel" {
			canceled++
		}
		if event.Phase == "call" && event.Event == "end" {
			ended = event.Canceled && event.Outcome == "canceled"
		}
	}
	if canceled != 1 || !ended {
		t.Fatalf("cancellation/end semantics: canceled=%d ended=%v", canceled, ended)
	}
}

func TestListFilesTimingBoundReservesTerminalRecords(t *testing.T) {
	ctx, recorder, output := timingTestContext(t, 32)
	ctx, trace, owner := beginListFilesTiming(ctx, "list_files", "bounded-call")
	if trace == nil || !owner {
		t.Fatal("enabled trace did not start")
	}
	for i := range 100 {
		span := trace.start("entry_info", i)
		span.end(errors.New("private error details"))
	}
	trace.finish(ctx, errors.New("private final error"))
	events := timingTestEvents(t, recorder, output)
	if len(events) > 32 || recorder.reserved != 0 {
		t.Fatalf("unbounded trace or unreleased terminal budget: %d events, %d reserved", len(events), recorder.reserved)
	}
	limited := false
	for _, event := range events {
		limited = limited || event.Event == "limit"
	}
	last := events[len(events)-1]
	if !limited || last.Event != "end" || last.Phase != "call" || last.Dropped == 0 || last.Outcome != "error" {
		t.Fatalf("missing bounded/error terminal evidence: %+v", events)
	}
	if strings.Contains(output.String(), "private") {
		t.Fatal("error text leaked")
	}
}

func TestListFilesTimingConcurrentCorrelation(t *testing.T) {
	ctx, recorder, output := timingTestContext(t, 512)
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := kit.Execute(ctx, providers.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "list_files", Arguments: `{}`})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	calls := map[uint64]string{}
	for _, event := range timingTestEvents(t, recorder, output) {
		if before, ok := calls[event.Trace]; ok && before != event.CallHash {
			t.Fatal("concurrent call correlation changed")
		}
		calls[event.Trace] = event.CallHash
	}
	if len(calls) != 8 {
		t.Fatalf("got %d correlated calls", len(calls))
	}
}

func TestListFilesTimingPrivateTempSink(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	recorder := newListFilesTimingRecorder()
	if recorder == nil {
		t.Fatal("could not create diagnostic sink")
	}
	file := recorder.writer.(*os.File)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0o600 || filepath.Dir(file.Name()) != os.TempDir() {
		t.Fatalf("sink is not private OS temp file: %v %v", info, err)
	}
	other := newListFilesTimingRecorder()
	if other == nil {
		t.Fatal("could not create second diagnostic sink")
	}
	otherFile := other.writer.(*os.File)
	defer otherFile.Close()
	if otherFile.Name() == file.Name() {
		t.Fatal("diagnostics reused/overwrote a sink")
	}
}

type timingFailWriter struct{}

func (timingFailWriter) Write([]byte) (int, error) { return 0, errors.New("diagnostic sink failed") }

func TestListFilesTimingSinkFailureDoesNotChangeToolResult(t *testing.T) {
	ctx, recorder, _ := timingTestContext(t, 128)
	recorder.writer = timingFailWriter{}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(kit.RootDir(), "visible.txt"), "data")
	result, err := kit.Execute(ctx, providers.ToolCall{Name: "list_files", Arguments: `{}`})
	if err != nil || !strings.Contains(result, "visible.txt") {
		t.Fatalf("diagnostic failure changed listing: %s, %v", result, err)
	}
}

func TestListFilesTimingCallAdmissionLimitIsVisible(t *testing.T) {
	ctx, recorder, output := timingTestContext(t, 10)
	for range 20 {
		next, trace, owner := beginListFilesTiming(ctx, "list_files", "bounded-call")
		if owner {
			trace.finish(next, nil)
		}
	}
	events := timingTestEvents(t, recorder, output)
	limits := 0
	for _, event := range events {
		if event.Event == "limit" && event.Phase == "process" {
			limits++
		}
	}
	if limits != 1 || len(events) > 10 || recorder.reserved != 0 {
		t.Fatalf("call admission silently exhausted its budget: %+v", events)
	}
}

type timingEnvironmentExecutor struct {
	entered chan struct{}
	release chan struct{}
}

func (e timingEnvironmentExecutor) Execute(context.Context, executionenv.ToolRequest) (toolresult.Result, error) {
	close(e.entered)
	<-e.release
	return toolresult.FromText("private environment result"), nil
}

type timingSelectedListTool struct {
	fakeListTool
	entered chan struct{}
	release chan struct{}
}

func (t timingSelectedListTool) Execute(context.Context, string) (string, error) {
	close(t.entered)
	<-t.release
	return "private selected-tool result", nil
}

func timingAssertOpenPhase(t *testing.T, events []listFilesTimingEvent, phase string) {
	t.Helper()
	started := false
	for _, event := range events {
		if event.Phase == phase {
			started = started || event.Event == "start"
			if event.Event == "end" {
				t.Fatalf("blocked %s already has an end", phase)
			}
		}
	}
	if !started {
		t.Errorf("blocked %s has no persisted start", phase)
	}
}

func TestListFilesTimingIdentifiesBlockedDispatch(t *testing.T) {
	for _, phase := range []string{"environment_execute", "tool_execute"} {
		t.Run(phase, func(t *testing.T) {
			ctx, recorder, output := timingTestContext(t, 512)
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var selected Tool = timingSelectedListTool{entered: entered, release: release}
			if phase == "environment_execute" {
				// Dispatch is determined by the non-nil selected executor, not an
				// environment's advertised local/remote type or the native tool name.
				kit.SetExecutionEnvironment(timingEnvironmentExecutor{entered: entered, release: release})
				selected = NewListFilesTool(kit.env)
			}
			go func() {
				_, err := kit.executeKnownToolResult(ctx, providers.ToolCall{ID: "private-dispatch-call", Name: "list_files", Arguments: `{}`}, selected)
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("selected executor did not start")
			}
			timingAssertOpenPhase(t, timingTestEvents(t, recorder, output), phase)
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			ended := false
			for _, event := range timingTestEvents(t, recorder, output) {
				ended = ended || event.Phase == phase && event.Event == "end"
				if event.Phase == "stat" || event.Phase == "read_dir" {
					t.Fatal("selected dispatch was misreported as native filesystem execution")
				}
			}
			if !ended || strings.Contains(output.String(), "private") {
				t.Fatal("dispatch end missing or private result leaked")
			}
		})
	}
}

type timingPhaseWriter struct {
	io.Writer
	phase    string
	observed chan struct{}
}

func (w timingPhaseWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if bytes.Contains(data, []byte(`"event":"start"`)) && bytes.Contains(data, []byte(`"phase":"`+w.phase+`"`)) {
		close(w.observed)
	}
	return n, err
}

func TestListFilesTimingIncludesBlockedTelemetry(t *testing.T) {
	ctx, recorder, output := timingTestContext(t, 512)
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	observed, done := make(chan struct{}), make(chan error, 1)
	recorder.writer = timingPhaseWriter{Writer: output, phase: "telemetry", observed: observed}
	kit.env.toolTelemetry.mu.Lock()
	go func() {
		_, err := kit.executeKnownToolResult(ctx, providers.ToolCall{ID: "private-telemetry-call", Name: "list_files", Arguments: `{}`}, fakeListTool{text: "private result"})
		done <- err
	}()
	select {
	case <-observed:
	case <-time.After(10 * time.Second):
		kit.env.toolTelemetry.mu.Unlock()
		<-done
		t.Fatal("telemetry start was not persisted before the blocked record lock")
	}
	timingAssertOpenPhase(t, timingTestEvents(t, recorder, output), "telemetry")
	select {
	case <-done:
		t.Fatal("diagnostic abandoned telemetry recording")
	default:
	}
	kit.env.toolTelemetry.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	events := timingTestEvents(t, recorder, output)
	last := events[len(events)-1]
	if last.Phase != "call" || last.Event != "end" {
		t.Fatal("measured interval did not include telemetry")
	}
	if len(kit.ToolTelemetry()) != 1 {
		t.Fatal("diagnostic changed telemetry recording")
	}
}

func TestListFilesTimingIncludesResultSettlement(t *testing.T) {
	ctx, recorder, output := timingTestContext(t, 512)
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(t.TempDir())
	result, err := kit.executeKnownToolResult(ctx, providers.ToolCall{ID: "private-settlement-call", Name: "list_files", Arguments: `{}`}, fakeListTool{text: overBudgetText()})
	if err != nil || result.ModelText == nil {
		t.Fatalf("result settlement changed: %v", err)
	}
	records := kit.ToolTelemetry()
	if len(records) != 1 || !records[0].ResultBudgeted || records[0].ResultRef == "" {
		t.Fatalf("over-budget result was not archived: %+v", records)
	}
	if _, err := os.Stat(records[0].ResultRef); err != nil {
		t.Fatal(err)
	}
	ends := map[string]int{}
	for i, event := range timingTestEvents(t, recorder, output) {
		if event.Event == "end" {
			ends[event.Phase] = i
		}
	}
	previous := -1
	for _, phase := range []string{"tool_execute", "result_validate", "result_projection", "result_finalize", "returned_projection", "telemetry", "call"} {
		end, ok := ends[phase]
		if !ok || end <= previous {
			t.Errorf("missing or out-of-order settlement phase %s", phase)
		}
		previous = end
	}
	for _, private := range []string{"private-settlement-call", records[0].ResultRef, kit.env.SessionDir, "word word"} {
		if strings.Contains(output.String(), private) {
			t.Fatalf("settlement diagnostic leaked %q", private)
		}
	}
}

type timingRichFailureListTool struct{ fakeListTool }

func (timingRichFailureListTool) ExecuteResult(context.Context, string) (toolresult.Result, error) {
	return toolresult.Result{IsError: true, Content: toolresult.FromText("private rich failure").Content}, nil
}

type timingRichFailureEnvironment struct{ cancel context.CancelFunc }

func (e timingRichFailureEnvironment) Execute(context.Context, executionenv.ToolRequest) (toolresult.Result, error) {
	if e.cancel != nil {
		e.cancel()
	}
	return timingRichFailureListTool{}.ExecuteResult(context.Background(), "")
}

func TestListFilesTimingRichFailureOutcome(t *testing.T) {
	for _, route := range []string{"tool", "environment", "canceled_environment", "worker"} {
		t.Run(route, func(t *testing.T) {
			ctx, recorder, output := timingTestContext(t, 512)
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			phase := "tool_execute"
			if strings.Contains(route, "environment") {
				environment := timingRichFailureEnvironment{}
				if route == "canceled_environment" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					defer cancel()
					environment.cancel = cancel
				}
				kit.SetExecutionEnvironment(environment)
				phase = "environment_execute"
			}
			call := providers.ToolCall{ID: "private-rich-failure", Name: "list_files", Arguments: `{}`}
			var result toolresult.Result
			if route == "worker" {
				kit.registry = NewRegistry(timingRichFailureListTool{})
				result, err = kit.ExecuteEnvironmentResult(ctx, call)
			} else {
				result, err = kit.executeKnownToolResult(ctx, call, timingRichFailureListTool{})
			}
			if err != nil || !result.IsError {
				t.Fatalf("diagnostic changed rich-failure semantics: %+v, %v", result, err)
			}
			ended := map[string]bool{}
			for _, event := range timingTestEvents(t, recorder, output) {
				if event.Event == "end" && (event.Phase == phase || event.Phase == "call") {
					ended[event.Phase] = true
					if event.Outcome != "error" {
						t.Errorf("rich failure reported as %s: %+v", event.Outcome, event)
					}
					if route == "canceled_environment" && (!event.Canceled || event.ContextState != "canceled") {
						t.Errorf("cancellation observation missing: %+v", event)
					}
				}
			}
			if !ended[phase] || !ended["call"] || strings.Contains(output.String(), "private") {
				t.Fatal("rich failure trace incomplete or leaked private output")
			}
		})
	}
}
