package executionenv

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestOldUnobservedWorkerHelper(t *testing.T) {
	if os.Getenv("WUU_OLD_WORKER_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req Request
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		if req.Method == "execute" || req.Method == "run_code" {
			_ = os.WriteFile(os.Getenv("WUU_OLD_WORKER_RECEIPT"), []byte("unexpected"), 0600)
		}
		hello, _ := json.Marshal(map[string]any{"version": ProtocolVersion, "capabilities": []string{ProcessStartCapability}})
		_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Data: hello})
	}
	os.Exit(0)
}
func TestUnobservedReadRejectsOlderWorkerBeforeDispatch(t *testing.T) {
	t.Setenv("WUU_OLD_WORKER_HELPER", "1")
	receipt := filepath.Join(t.TempDir(), "execute")
	t.Setenv("WUU_OLD_WORKER_RECEIPT", receipt)
	env := NewEnvironment(Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestOldUnobservedWorkerHelper$"}, Workspace: t.TempDir()}, "old-worker", "thread", t.TempDir())
	defer env.Close()
	_, err := env.ExecuteUnobservedRead(context.Background(), ToolRequest{Actor: "actor", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"test.txt"}`}})
	require.ErrorContains(t, err, "does not support unobserved reads")
	_, err = os.Stat(receipt)
	require.True(t, os.IsNotExist(err), "unsupported worker must never receive the read request")
}

func TestStatelessCodeRejectsOlderWorkerBeforeDispatch(t *testing.T) {
	t.Setenv("WUU_OLD_WORKER_HELPER", "1")
	receipt := filepath.Join(t.TempDir(), "run-code")
	t.Setenv("WUU_OLD_WORKER_RECEIPT", receipt)
	env := NewEnvironment(Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestOldUnobservedWorkerHelper$"}, Workspace: t.TempDir()}, "old-worker", "thread", t.TempDir())
	defer env.Close()
	_, err := env.RunCode(context.Background(), CodeRequest{DisableState: true}, nil)
	require.ErrorContains(t, err, "does not support stateless Code Mode")
	_, err = os.Stat(receipt)
	require.True(t, os.IsNotExist(err), "unsupported worker must not run the program")
	_, err = env.RunCode(context.Background(), CodeRequest{}, nil)
	require.NoError(t, err, "ordinary Code Mode remains compatible with old workers")
	_, err = os.Stat(receipt)
	require.NoError(t, err, "ordinary Code Mode is dispatched")
}
