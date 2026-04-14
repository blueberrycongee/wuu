package tools

import (
	"context"
	"encoding/json"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ---------------------------------------------------------------------------
// start_process
// ---------------------------------------------------------------------------

type StartProcessTool struct{ env *Env }

func NewStartProcessTool(env *Env) *StartProcessTool { return &StartProcessTool{env: env} }

func (t *StartProcessTool) Name() string            { return "start_process" }
func (t *StartProcessTool) IsReadOnly() bool         { return false }
func (t *StartProcessTool) IsConcurrencySafe() bool  { return false }

func (t *StartProcessTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "start_process", Description: "Start a managed background OS process in the workspace.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "owner_kind": map[string]any{"type": "string", "enum": []string{"main_agent", "subagent"}}, "owner_id": map[string]any{"type": "string"}, "lifecycle": map[string]any{"type": "string", "enum": []string{"session", "managed"}}}, "required": []string{"command", "owner_kind"}},
	}
}

func (t *StartProcessTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct{ Command, CWD, OwnerKind, OwnerID, Lifecycle string }
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	p, startErr := m.Start(context.WithoutCancel(ctx), proc.StartOptions{Command: args.Command, CWD: args.CWD, OwnerKind: proc.OwnerKind(args.OwnerKind), OwnerID: args.OwnerID, Lifecycle: proc.Lifecycle(args.Lifecycle)})
	out, _ := json.Marshal(p)
	if startErr != nil {
		return string(out), startErr
	}
	return string(out), nil
}

// ---------------------------------------------------------------------------
// list_processes
// ---------------------------------------------------------------------------

type ListProcessesTool struct{ env *Env }

func NewListProcessesTool(env *Env) *ListProcessesTool { return &ListProcessesTool{env: env} }

func (t *ListProcessesTool) Name() string            { return "list_processes" }
func (t *ListProcessesTool) IsReadOnly() bool         { return true }
func (t *ListProcessesTool) IsConcurrencySafe() bool  { return true }

func (t *ListProcessesTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "list_processes", Description: "List wuu-managed background OS processes.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (t *ListProcessesTool) Execute(_ context.Context, _ string) (string, error) {
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	ps, err := m.List()
	if err != nil {
		return "", err
	}
	return mustJSON(ps)
}

// ---------------------------------------------------------------------------
// stop_process
// ---------------------------------------------------------------------------

type StopProcessTool struct{ env *Env }

func NewStopProcessTool(env *Env) *StopProcessTool { return &StopProcessTool{env: env} }

func (t *StopProcessTool) Name() string            { return "stop_process" }
func (t *StopProcessTool) IsReadOnly() bool         { return false }
func (t *StopProcessTool) IsConcurrencySafe() bool  { return true }

func (t *StopProcessTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "stop_process", Description: "Stop a background process by process group, graceful then kill.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"process_id": map[string]any{"type": "string"}}, "required": []string{"process_id"}},
	}
}

func (t *StopProcessTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		ProcessID string `json:"process_id"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	p, err := m.Stop(args.ProcessID)
	if err != nil {
		return "", err
	}
	return mustJSON(p)
}

// ---------------------------------------------------------------------------
// read_process_output
// ---------------------------------------------------------------------------

type ReadProcessOutputTool struct{ env *Env }

func NewReadProcessOutputTool(env *Env) *ReadProcessOutputTool {
	return &ReadProcessOutputTool{env: env}
}

func (t *ReadProcessOutputTool) Name() string            { return "read_process_output" }
func (t *ReadProcessOutputTool) IsReadOnly() bool         { return true }
func (t *ReadProcessOutputTool) IsConcurrencySafe() bool  { return true }

func (t *ReadProcessOutputTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "read_process_output", Description: "Read recent output from a background process log.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"process_id": map[string]any{"type": "string"}, "max_bytes": map[string]any{"type": "integer"}}, "required": []string{"process_id"}},
	}
}

func (t *ReadProcessOutputTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		ProcessID string `json:"process_id"`
		MaxBytes  int    `json:"max_bytes"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	m, err := t.env.ProcessManager()
	if err != nil {
		return "", err
	}
	out, tr, err := m.ReadOutput(args.ProcessID, args.MaxBytes)
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{"process_id": args.ProcessID, "output": out, "truncated": tr})
}
