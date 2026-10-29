package executionworker

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/executionenv"
)

// Exercise worker shutdown over its actual process transport while a tool
// callback is in flight. Serve must cancel and drain it before closing actors.
func TestWorkerProcessCloseDrainsInFlightActor(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_EXECUTION_TEST_WORKER", "1")
	client := executionenv.NewClient([]string{os.Args[0], "-test.run=^TestWorkerTransportProcess$"}, os.Environ())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := client.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: t.TempDir(), Session: "drain"})
	if err != nil {
		t.Fatal(err)
	}
	entered, stopped := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.CallWithHandler(ctx, "run_code", executionenv.CodeRequest{Actor: "worker", PermissionMode: "unconfined", Program: codemode.RunRequest{Code: `await tools.wait({})`, Tools: []codemode.ToolDefinition{{Name: "wait"}}}}, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			close(entered)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker callback did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("worker failed graceful close: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("worker close did not drain")
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("callback not canceled")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("in-flight call did not end")
	}
}
