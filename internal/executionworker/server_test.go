package executionworker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestWorkerFilesystemAndPermissionBoundary(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	s := New()
	defer s.Close()
	initData, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "test-session"})
	if _, err := s.Handle(context.Background(), "initialize", initData); err != nil {
		t.Fatal(err)
	}
	call := func(name, args, mode string) (toolresult.Result, error) {
		data, _ := json.Marshal(executionenv.ToolRequest{Call: providers.ToolCall{Name: name, Arguments: args}, PermissionMode: mode, Actor: "root"})
		raw, err := s.Handle(context.Background(), "execute", data)
		var result toolresult.Result
		if err == nil {
			err = json.Unmarshal(raw, &result)
		}
		return result, err
	}
	if _, err := call("write_file", `{"path":"hello.txt","content":"remote contents"}`, "standard"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "hello.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := call("read_file", `{"path":"hello.txt"}`, "read_only"); err != nil {
		t.Fatal(err)
	}
	if _, err := call("write_file", `{"path":"denied.txt","content":"no"}`, "read_only"); err == nil {
		t.Fatal("read-only write succeeded")
	}
	if _, err := call("read_file", `{"path":"../outside.txt"}`, "standard"); err == nil {
		t.Fatal("file escaped worker workspace")
	}
	if _, err := call("notes", `{"action":"read"}`, "standard"); err == nil {
		t.Fatal("host control tool accepted by worker")
	}
}

func TestWorkerRejectsProtocolMismatch(t *testing.T) {
	s := New()
	defer s.Close()
	data, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion + 1, Root: t.TempDir(), Session: "test"})
	if _, err := s.Handle(context.Background(), "initialize", data); err == nil {
		t.Fatal("protocol mismatch accepted")
	}
}
