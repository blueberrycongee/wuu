package executionenv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
			if mode == "drop-commit" {
				caps = append(caps, ReadObservationCommitCapability)
			}
			payload = map[string]any{"version": ProtocolVersion, "capabilities": caps}
		case "read/prepare":
			_ = os.WriteFile(os.Getenv("WUU_OBSERVATION_PREPARE"), []byte("sent"), 0600)
			payload = map[string]any{"receipt": "worker-receipt", "result": map[string]any{"structured_content": map[string]any{"text": "text\n", "total_lines": 1}}}
		case "read/commit":
			_ = os.WriteFile(os.Getenv("WUU_OBSERVATION_COMMIT"), []byte("accepted"), 0600)
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
}
