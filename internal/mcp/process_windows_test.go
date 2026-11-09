//go:build windows

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const windowsProcessTreeRoleEnv = "WUU_MCP_PROCESS_TREE_TEST_ROLE"

func TestWindowsMCPProcessTreeHelper(t *testing.T) {
	switch os.Getenv(windowsProcessTreeRoleEnv) {
	case "server":
		if err := os.Setenv(windowsProcessTreeRoleEnv, "descendant"); err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		child := exec.Command(executable, "-test.run=^TestWindowsMCPProcessTreeHelper$")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		ready, err := json.Marshal(Response{
			JSONRPC: "2.0",
			Method:  "test/ready",
			Params:  json.RawMessage(`{"pid":` + strconv.Itoa(child.Process.Pid) + `}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stdout.Write(append(ready, '\n')); err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "descendant":
		select {}
	}
}

func TestStdioTransportCloseTerminatesWindowsDescendants(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewStdioTransportWithEnv(executable, []string{"-test.run=^TestWindowsMCPProcessTreeHelper$"}, map[string]string{
		windowsProcessTreeRoleEnv: "server",
	})
	if err != nil {
		t.Fatalf("NewStdioTransportWithEnv: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready, err := transport.Receive(ctx)
	if err != nil {
		_ = transport.Close()
		t.Fatalf("receive ready message: %v", err)
	}
	var payload struct {
		PID uint32 `json:"pid"`
	}
	if ready.Method != "test/ready" {
		_ = transport.Close()
		t.Fatalf("unexpected ready message: %+v", ready)
	}
	if err := json.Unmarshal(ready.Params, &payload); err != nil {
		_ = transport.Close()
		t.Fatalf("decode ready PID: %v", err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, payload.PID)
	if err != nil {
		_ = transport.Close()
		t.Fatalf("open descendant process: %v", err)
	}
	defer func() {
		_ = windows.TerminateProcess(process, 1)
		_ = windows.CloseHandle(process)
	}()
	if !transport.processGroup.assigned {
		_ = transport.Close()
		t.Skip("Windows Job Object containment is unavailable in this environment")
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	result, err := windows.WaitForSingleObject(process, 5000)
	if err != nil {
		t.Fatalf("wait for descendant: %v", err)
	}
	if result != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant process %d remained alive after transport close", payload.PID)
	}
}
