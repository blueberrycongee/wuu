package executionenv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestObservationWorkerHelper(t *testing.T) {
	mode := os.Getenv("WUU_OBSERVATION_HELPER")
	if mode == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request Request
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var payload any
		switch request.Method {
		case "initialize":
			caps := []string{UnobservedReadCapability}
			if mode == "drop-commit" || mode == "cancel-after-send" || mode == "malformed-prepare" {
				caps = append(caps, ReadObservationCommitCapability)
			}
			payload = map[string]any{"version": ProtocolVersion, "capabilities": caps, "worker_instance": "fixture-instance"}
		case "read/prepare":
			_ = os.WriteFile(os.Getenv("WUU_OBSERVATION_PREPARE"), []byte("sent"), 0600)
			payload = map[string]any{"receipt": "worker-receipt", "result": map[string]any{"structured_content": map[string]any{"text": "text\n", "total_lines": 1}}}
		case "read/batch-prepare":
			payload = map[string]any{} // reserved but malformed acknowledgement
		case "read/batch-abort":
			if mode == "malformed-abort" {
				payload = map[string]any{}
				break
			}
			_ = os.WriteFile(os.Getenv("WUU_OBSERVATION_ABORT"), []byte("aborted"), 0600)
			payload = map[string]any{"aborted": true}
		case "read/batch-release":
			payload = map[string]any{"released": true}
		case "read/commit":
			if mode == "cancel-after-send" {
				_ = os.WriteFile(os.Getenv("WUU_OBSERVATION_ENTERED"), []byte("entered"), 0600)
				if !scanner.Scan() {
					os.Exit(2)
				}
				var cancel Request
				if json.Unmarshal(scanner.Bytes(), &cancel) != nil || cancel.Method != "cancel" || cancel.ID != request.ID {
					os.Exit(3)
				}
			}
			file, err := os.OpenFile(os.Getenv("WUU_OBSERVATION_COMMIT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err == nil {
				_, _ = file.WriteString("accepted\n")
				_ = file.Close()
			}
			os.Exit(0) // commit reached the worker; acknowledgement is irretrievably lost
		default:
			payload = map[string]any{}
		}
		data, _ := json.Marshal(payload)
		_ = json.NewEncoder(os.Stdout).Encode(Response{ID: request.ID, Data: data})
	}
	os.Exit(0)
}

func TestObservationOldWorkerFailsBeforePrepare(t *testing.T) {
	t.Setenv("WUU_OBSERVATION_HELPER", "old")
	marker := filepath.Join(t.TempDir(), "prepared")
	t.Setenv("WUU_OBSERVATION_PREPARE", marker)
	env := NewEnvironment(Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestObservationWorkerHelper$"}, Workspace: t.TempDir()}, "old", "thread", t.TempDir())
	defer env.Close()
	_, err := env.PrepareUnobservedRead(context.Background(), ToolRequest{Actor: "actor", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"test"}`}})
	require.ErrorContains(t, err, "does not support read observation commit")
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err))
}
func TestObservationLostCommitAcknowledgementIsUnknown(t *testing.T) {
	t.Setenv("WUU_OBSERVATION_HELPER", "drop-commit")
	marker := filepath.Join(t.TempDir(), "committed")
	t.Setenv("WUU_OBSERVATION_COMMIT", marker)
	t.Setenv("WUU_OBSERVATION_PREPARE", filepath.Join(t.TempDir(), "prepared"))
	env := NewEnvironment(Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestObservationWorkerHelper$"}, Workspace: t.TempDir()}, "uncertain", "thread", t.TempDir())
	defer env.Close()
	pending, err := env.PrepareUnobservedRead(context.Background(), ToolRequest{Actor: "actor", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"test"}`}})
	require.NoError(t, err)
	_, err = pending.Commit(context.Background())
	require.True(t, errors.Is(err, ErrReadObservationCommitUnknown))
	require.FileExists(t, marker)
	_, err = pending.Commit(context.Background())
	require.True(t, errors.Is(err, ErrReadObservationCommitUnknown))
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "accepted\n", string(data), "an uncertain commit must not be sent to a new worker")
}

func TestObservationCancellationAfterSendIsUnknown(t *testing.T) {
	t.Setenv("WUU_OBSERVATION_HELPER", "cancel-after-send")
	entered := filepath.Join(t.TempDir(), "entered")
	committed := filepath.Join(t.TempDir(), "committed")
	t.Setenv("WUU_OBSERVATION_ENTERED", entered)
	t.Setenv("WUU_OBSERVATION_COMMIT", committed)
	t.Setenv("WUU_OBSERVATION_PREPARE", filepath.Join(t.TempDir(), "prepared"))
	env := NewEnvironment(Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestObservationWorkerHelper$"}, Workspace: t.TempDir()}, "cancel", "thread", t.TempDir())
	defer env.Close()
	pending, err := env.PrepareUnobservedRead(context.Background(), ToolRequest{Actor: "actor", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"test"}`}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := pending.Commit(ctx); done <- e }()
	require.Eventually(t, func() bool { _, e := os.Stat(entered); return e == nil }, 3*time.Second, 10*time.Millisecond)
	cancel()
	err = <-done
	require.True(t, errors.Is(err, ErrReadObservationCommitUnknown))
	require.ErrorIs(t, err, context.Canceled)
	require.Eventually(t, func() bool { _, e := os.Stat(committed); return e == nil }, 3*time.Second, 10*time.Millisecond, "worker may commit after cancellation was requested")
}

func TestObservationAbortRequiresWorkerAcknowledgement(t *testing.T) {
	t.Setenv("WUU_OBSERVATION_HELPER", "malformed-abort")
	client := NewClient([]string{os.Args[0], "-test.run=^TestObservationWorkerHelper$"}, os.Environ())
	defer client.Close()
	batch := &PreparedReadBatch{Identity: ReadBatchIdentity{OperationID: "op", WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}, client: client}
	require.Error(t, batch.Abort(context.Background()), "empty success frame does not confirm an abort fence")
}

func TestObservationMalformedPreparationAcknowledgementIsFenced(t *testing.T) {
	t.Setenv("WUU_OBSERVATION_HELPER", "malformed-prepare")
	marker := filepath.Join(t.TempDir(), "abort")
	t.Setenv("WUU_OBSERVATION_ABORT", marker)
	t.Setenv("WUU_OBSERVATION_PREPARE", filepath.Join(t.TempDir(), "prepared"))
	env := NewEnvironment(Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestObservationWorkerHelper$"}, Workspace: t.TempDir()}, "env", "thread", t.TempDir())
	defer env.Close()
	pending, err := env.PrepareUnobservedRead(context.Background(), ToolRequest{Actor: "actor", Call: providers.ToolCall{Name: "read_file", Arguments: `{}`}})
	require.NoError(t, err)
	_, err = PrepareReadBatch(context.Background(), []PreparedRead{pending})
	require.ErrorContains(t, err, "invalid remote batch preparation")
	require.FileExists(t, marker)
}
