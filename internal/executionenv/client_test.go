package executionenv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestShutdownBudgetWorker(t *testing.T) {
	if os.Getenv("WUU_SHUTDOWN_BUDGET_WORKER") != "1" {
		return
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req Request
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		data, _ := json.Marshal(map[string]any{"version": ProtocolVersion})
		_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Data: data})
	}
	delay, _ := strconv.Atoi(os.Getenv("WUU_SHUTDOWN_DELAY_MS"))
	time.Sleep(time.Duration(delay) * time.Millisecond)
	if err := os.WriteFile(os.Getenv("WUU_SHUTDOWN_RECEIPT"), []byte("cleanup completed"), 0600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestCommandEnvironmentShutdownBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		budget, delay int
		wantTimeout   bool
	}{
		// An SDK provider can need more than the native worker's stop/reap
		// window; the command profile must not truncate its cleanup.
		{name: "default_allows_delegated_cleanup", delay: 8100},
		{name: "explicit_budget_allows_cleanup", budget: 3000, delay: 100},
		{name: "explicit_budget_bounds_stuck_cleanup", budget: 50, delay: 5000, wantTimeout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := filepath.Join(t.TempDir(), "closed")
			t.Setenv("WUU_SHUTDOWN_BUDGET_WORKER", "1")
			t.Setenv("WUU_SHUTDOWN_DELAY_MS", strconv.Itoa(tc.delay))
			t.Setenv("WUU_SHUTDOWN_RECEIPT", receipt)
			profile := Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestShutdownBudgetWorker$"}, Workspace: t.TempDir(), ShutdownTimeoutMS: tc.budget}
			environment := NewEnvironment(profile, "shutdown-budget", "thread", t.TempDir())
			defer environment.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := environment.connect(ctx); err != nil {
				t.Fatal(err)
			}
			err := environment.Close()
			if (err != nil) != tc.wantTimeout {
				t.Fatalf("shutdown result=%v, want timeout=%v", err, tc.wantTimeout)
			}
			_, receiptErr := os.Stat(receipt)
			if tc.wantTimeout && !errors.Is(receiptErr, os.ErrNotExist) {
				t.Fatalf("timed out worker wrote a completed receipt: %v", receiptErr)
			}
			if !tc.wantTimeout && receiptErr != nil {
				t.Fatalf("worker cleanup was truncated: %v", receiptErr)
			}
		})
	}
}

func TestEnvironmentCleanupFailurePreservesWorkspace(t *testing.T) {
	c := testClient(t)
	stateDir := t.TempDir()
	workspace := filepath.Join(stateDir, "failed-cleanup", "workspace")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "recovery.txt")
	if err := os.WriteFile(marker, []byte("retain after failed shutdown"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "disconnect", nil); err == nil {
		t.Fatal("fixture did not fail its transport")
	}
	environment := &Environment{client: c, profile: Profile{Backend: "singularity"}, prepared: true, stateDir: stateDir, identity: "failed-cleanup"}
	if err := environment.Close(); err == nil {
		t.Fatal("environment hid failed transport cleanup")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("environment deleted workspace after failed transport cleanup: %v", err)
	}
}

func TestTransportWorker(t *testing.T) {
	if os.Getenv("WUU_ENV_TEST_WORKER") != "1" {
		return
	}
	held := ""
	processStart := false
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req Request
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		if req.Method == "process/start" {
			if string(req.Data) != "null" {
				var input ProcessRequest
				_ = json.Unmarshal(req.Data, &input)
				data, _ := json.Marshal(map[string]string{"id": "foreign-process", "operation_id": input.OperationID, "owner_id": input.Actor, "root_thread_id": "foreign-thread"})
				_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Data: data})
				continue
			}
			held = req.ID
			processStart = true
			_ = json.NewEncoder(os.Stdout).Encode(Response{Method: "process", Data: json.RawMessage(`{"started":true}`)})
			continue
		}
		if req.Method == "run_code" && string(req.Data) == `{"hold":true}` {
			held = req.ID
			_ = json.NewEncoder(os.Stdout).Encode(Response{Method: "process", Data: json.RawMessage(`{"ready":true}`)})
			continue
		}
		if req.Method == "execute" && held != "" {
			_ = json.NewEncoder(os.Stdout).Encode(Response{ID: held, Error: "cancelled"})
			held = ""
		}
		if req.Method == "run_code" && held != "" {
			_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Error: "new program overtook cancelled execution"})
			continue
		}
		if req.Method == "disconnect" {
			os.Exit(3)
		}
		if req.Method == "wait" {
			continue
		}
		if req.Method == "cancel" {
			if req.ID == held && processStart {
				_ = json.NewEncoder(os.Stdout).Encode(Response{ID: held, Data: json.RawMessage(`{"id":"proc-accepted","operation_id":"operation-known"}`)})
				held = ""
				continue
			}
			if req.ID == held {
				continue
			}
			_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Error: "cancelled"})
			continue
		}
		_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Data: json.RawMessage(`{"ok":true}`)})
	}
	os.Exit(0)
}

func testClient(t *testing.T) *Client {
	t.Helper()
	t.Setenv("WUU_ENV_TEST_WORKER", "1")
	c := NewClient([]string{os.Args[0], "-test.run=^TestTransportWorker$"}, []string{"WUU_ENV_TEST_WORKER=1"})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestTransportCancellationAndNoReplay(t *testing.T) {
	c := testClient(t)
	if _, err := c.Call(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, "wait", nil); err == nil {
		t.Fatal("cancelled call succeeded")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, "wait", nil); err == nil {
		t.Fatal("deadline ignored")
	}
	if _, err := c.Call(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "disconnect", nil); err == nil {
		t.Fatal("connection loss succeeded")
	}
	if _, err := c.Call(context.Background(), "hello", nil); err == nil {
		t.Fatal("failed worker was automatically restarted")
	}
}

func TestConcurrentResponsesStayWithCaller(t *testing.T) {
	c := testClient(t)
	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			data, err := c.Call(context.Background(), "hello", nil)
			if err == nil && string(data) != `{"ok":true}` {
				err = fmt.Errorf("wrong result: %s", data)
			}
			done <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestProgramRetirementKeepsProcessControlAvailable(t *testing.T) {
	c := testClient(t)
	ready := make(chan struct{})
	c.event = func(json.RawMessage) { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "run_code", map[string]bool{"hold": true}); first <- err }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("program never started")
	}
	cancel()
	if err := <-first; err == nil {
		t.Fatal("cancelled program reported success")
	}
	nextCtx, nextCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer nextCancel()
	next := make(chan error, 1)
	go func() { _, err := c.Call(nextCtx, "run_code", nil); next <- err }()
	if _, err := c.Call(nextCtx, "execute", map[string]string{"action": "stop"}); err != nil {
		t.Fatalf("retirement blocked process control: %v", err)
	}
	if err := <-next; err != nil {
		t.Fatalf("program reuse: %v", err)
	}
}

func TestProcessStartCancellationReturnsReceiptForCleanup(t *testing.T) {
	c := testClient(t)
	ready := make(chan struct{})
	c.event = func(json.RawMessage) { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		data json.RawMessage
		err  error
	}
	done := make(chan result, 1)
	go func() { data, err := c.Call(ctx, "process/start", nil); done <- result{data, err} }()
	<-ready
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) || string(got.data) != `{"id":"proc-accepted","operation_id":"operation-known"}` {
		t.Fatalf("cancel lost accepted process receipt: %s %v", got.data, got.err)
	}
}

func TestEnvironmentRejectsWorkerWithoutProcessCapability(t *testing.T) {
	c := testClient(t)
	environment := &Environment{client: c, identity: "pinned"}
	if _, err := environment.StartProcess(context.Background(), ProcessRequest{Command: "must-not-start", Actor: "actor", PermissionMode: "unconfined"}); err == nil {
		t.Fatal("old worker silently accepted process capability")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sequence != 0 {
		t.Fatal("unsupported worker received mutating request")
	}
}

func TestEnvironmentRejectsForeignProcessReceipt(t *testing.T) {
	c := testClient(t)
	c.processStart = true
	environment := &Environment{client: c, identity: "pinned", session: "thread"}
	if _, err := environment.StartProcess(context.Background(), ProcessRequest{Command: "printf remote", Actor: "actor", PermissionMode: "unconfined"}); err == nil {
		t.Fatal("foreign process receipt accepted")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sequence != 1 {
		t.Fatal("host attempted cleanup of a foreign process")
	}
}
