package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/shellpath"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// CommandActionGate retains an exact command until the host dispatches it.
// Observing a process never conveys permission to send new input to it.
type CommandActionGate func(context.Context, *Toolkit, providers.ToolCall) (toolresult.Result, error)

func (t *Toolkit) SetCommandActionGate(gate CommandActionGate) { t.commandActionGate = gate }

type CommandAction struct {
	CallID            string          `json:"call_id"`
	Operation         string          `json:"operation"`
	Arguments         json.RawMessage `json:"arguments"`
	CWD               string          `json:"cwd"`
	ResolvedCommand   string          `json:"resolved_command,omitempty"`
	EnvironmentDigest string          `json:"environment_digest"`
	PermissionMode    string          `json:"permission_mode"`
	SessionID         string          `json:"session_id"`
	ActorID           string          `json:"actor_id"`
	ProcessIdentity   string          `json:"process_identity,omitempty"`
}
type commandActionContextKey struct{}
type commandEnvironmentContextKey struct{}

func commandEnvironment(ctx context.Context) []string {
	if frozen, ok := ctx.Value(commandEnvironmentContextKey{}).([]string); ok {
		return append([]string(nil), frozen...)
	}
	return shellCommandEnv(os.Environ())
}

func commandWorkingDirectory(ctx context.Context, requested string) string {
	if frozen, ok := ctx.Value(commandActionContextKey{}).(CommandAction); ok {
		return frozen.CWD
	}
	return requested
}

func commandMutation(call providers.ToolCall) bool {
	if call.Name == "bash" || call.Name == "process_start" {
		return true
	}
	if call.Name != "process" {
		return false
	}
	var args processArgs
	if decodeArgs(call.Arguments, &args) != nil {
		return true
	}
	return args.Action != processActionList && args.Action != processActionRead
}

func (t *Toolkit) commandCall(call providers.ToolCall) (providers.ToolCall, Tool, error) {
	if call.Name == "process_start" {
		call.Name = "bash"
	}
	tool := t.LookupTool(call.Name)
	if tool == nil || t.IsToolDisabled(call.Name) {
		return call, nil, errors.New("reviewed command tool unavailable")
	}
	return call, tool, nil
}

func (t *Toolkit) PrepareCommandAction(ctx context.Context, call providers.ToolCall) (CommandAction, error) {
	if err := ctx.Err(); err != nil {
		return CommandAction{}, err
	}
	if !commandMutation(call) || t.env.ExecutionEnvironment != nil || !t.LocalFileActionCallbacks() {
		return CommandAction{}, errors.New("Personal command approval requires local execution with fenced authorization and no extension hooks")
	}
	checked, tool, err := t.commandCall(call)
	if err != nil {
		return CommandAction{}, err
	}
	if validator, ok := tool.(InputValidatingTool); ok {
		if err = validator.ValidateInput(call.Arguments); err != nil {
			return CommandAction{}, err
		}
	}
	info := buildToolInfoForArgs(tool, t.toolExposure(checked.Name), checked.Arguments)
	if reason := approvefor.HardDenyReason(approvefor.Request{SessionID: t.SessionID(), PermissionMode: "standard", Tool: approvefor.Tool{Name: info.Name, Kind: string(info.Kind), Risk: string(info.Risk), ReadOnly: info.ReadOnly, Destructive: info.Destructive}, Arguments: checked.Arguments, CWD: t.RootDir(), ReferencedPaths: reviewShellPaths(checked, t.RootDir())}); reason != "" {
		return CommandAction{}, errors.New(reason)
	}
	cwd := t.RootDir()
	identity := ""
	resolvedCommand := ""
	if checked.Name == "bash" {
		var args bashArgs
		if err = decodeArgs(call.Arguments, &args); err != nil {
			return CommandAction{}, err
		}
		if err = args.validate(); err != nil {
			return CommandAction{}, err
		}
		cwd, err = resolveShellWorkingDir(ctx, t.env, args.CWD)
		if err != nil {
			return CommandAction{}, err
		}
		resolvedCommand = args.Command
		if call.Name == "bash" && !args.RunInBackground && bashCommandLooksLikeVerification(args.Command) {
			resolved, err := resolveRunTestCommand(cwd, args.Command)
			if err != nil {
				return CommandAction{}, err
			}
			resolvedCommand = resolved.Command
		}
		if (args.RunInBackground || call.Name == "process_start") && len(t.env.ProtectedPaths) > 0 {
			return CommandAction{}, errors.New("protected read/write policy does not support background processes")
		}
	} else {
		var args processArgs
		if err = decodeArgs(call.Arguments, &args); err != nil {
			return CommandAction{}, err
		}
		if args.Action == processActionWrite {
			raw, _ := json.Marshal(bashArgs{Command: args.Input})
			input := providers.ToolCall{Name: "bash", Arguments: string(raw)}
			if reason := approvefor.HardDenyReason(approvefor.Request{PermissionMode: "standard", Tool: approvefor.Tool{Name: "bash", Kind: "shell"}, Arguments: input.Arguments, CWD: t.RootDir(), ReferencedPaths: reviewShellPaths(input, t.RootDir())}); reason != "" {
				return CommandAction{}, errors.New(reason)
			}
		}
		if len(t.env.ProtectedPaths) > 0 {
			return CommandAction{}, errors.New("managed processes cannot prove protected read/write enforcement")
		}
		manager, err := t.env.ProcessManager()
		if err != nil {
			return CommandAction{}, err
		}
		process, err := manager.Get(args.ProcessID)
		if err != nil {
			return CommandAction{}, err
		}
		if process.RootThreadID != processRootThreadID(t.env) || process.OwnerID != defaultProcessOwnerID(t.env) || !processIsLive(process.Status) {
			return CommandAction{}, errors.New("reviewed process is not live in this execution")
		}
		if args.Action == processActionWrite && !manager.InputAvailable(process.ID) {
			return CommandAction{}, errors.New("reviewed process input is unavailable")
		}
		cwd = process.CWD
		raw, _ := json.Marshal(struct {
			ID, Generation, Start, Owner, Thread string
			PID                                  int
		}{process.ID, process.HostGenerationID, process.ProcessStartTime, process.OwnerID, process.RootThreadID, process.PID})
		identity = sha256Hex(raw)
	}
	// Bind resolved paths as well as the lexical directory; a replaced symlink
	// cannot redirect a pending command into another workspace.
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return CommandAction{}, err
	}
	env := commandEnvironment(ctx)
	sort.Strings(env)
	prefix := ""
	if t.env.gitAttributionEnabled() {
		prefix, err = t.env.gitAttributionShellPrefix()
		if err != nil {
			return CommandAction{}, err
		}
	}
	interpreter, err := shellpath.LoginBash()
	if err != nil {
		return CommandAction{}, err
	}
	interpreter.Path, err = exec.LookPath(interpreter.Path)
	if err != nil {
		return CommandAction{}, err
	}
	binding, err := json.Marshal(struct {
		Environment               []string
		Interpreter               shellpath.Shell
		Root, Prefix              string
		Enforce, AllowMutations   bool
		FileRoots, ProtectedPaths []string
	}{env, interpreter, t.RootDir(), prefix, t.boundary.Enforce, t.boundary.AllowMutations, t.env.FileScopeRoots, t.env.ProtectedPaths})
	if err != nil {
		return CommandAction{}, err
	}
	return CommandAction{CallID: call.ID, Operation: call.Name, Arguments: json.RawMessage(call.Arguments), CWD: cwd, ResolvedCommand: resolvedCommand, EnvironmentDigest: sha256Hex(binding), PermissionMode: t.env.PermissionMode, SessionID: t.SessionID(), ActorID: t.env.AgentID, ProcessIdentity: identity}, nil
}

func (t *Toolkit) ValidateCommandAction(ctx context.Context, a CommandAction) error {
	current, err := t.PrepareCommandAction(ctx, providers.ToolCall{ID: a.CallID, Name: a.Operation, Arguments: string(a.Arguments)})
	if err != nil {
		return err
	}
	before, _ := json.Marshal(a)
	after, _ := json.Marshal(current)
	if !bytes.Equal(before, after) {
		return errors.New("personal_action_command_changed")
	}
	return nil
}

func (t *Toolkit) ExecuteCommandAction(ctx context.Context, a CommandAction) (toolresult.Result, error) {
	if err := t.ValidateCommandAction(ctx, a); err != nil {
		return toolresult.Result{}, err
	}
	call := providers.ToolCall{ID: a.CallID, Name: a.Operation, Arguments: string(a.Arguments)}
	checked, tool, err := t.commandCall(call)
	if err != nil {
		return toolresult.Result{}, err
	}
	request := t.authorizationRequest(buildToolInfoForArgs(tool, t.toolExposure(checked.Name), checked.Arguments), checked)
	execute := func(ctx context.Context) (toolresult.Result, error) {
		ctx = context.WithValue(ctx, commandEnvironmentContextKey{}, commandEnvironment(ctx))
		if err := t.ValidateCommandAction(ctx, a); err != nil {
			return toolresult.Result{}, err
		}
		ctx = context.WithValue(ctx, commandActionContextKey{}, a)
		if a.Operation != "process_start" {
			return t.ExecuteResult(ctx, call)
		}
		if err := t.checkPermission(ctx, request.Tool, checked); err != nil {
			return toolresult.Result{}, err
		}
		scope, err := t.PluginProcessScope()
		if err != nil {
			return toolresult.Result{}, err
		}
		var args bashArgs
		if err = json.Unmarshal(a.Arguments, &args); err != nil {
			return toolresult.Result{}, err
		}
		started, _, err := scope.startAuthorized(ctx, args.Command, a.CWD, "")
		if err != nil {
			return toolresult.Result{}, err
		}
		raw, err := json.Marshal(redactProcess(t.env, *started))
		return toolresult.FromText(string(raw)), err
	}
	if t.authorizer == nil {
		return execute(ctx)
	}
	authorizer, ok := t.authorizer.(AtomicAuthorizer)
	if !ok || !authorizer.SupportsAtomicAuthorization() {
		return toolresult.Result{}, errors.New("selected command authorization effect fence unavailable")
	}
	var result toolresult.Result
	entered := false
	err = authorizer.WithAuthorization(ctx, request, func(effectCtx context.Context) error {
		if entered || effectCtx == nil {
			return errors.New("invalid command authorization effect callback")
		}
		entered = true
		var err error
		result, err = execute(context.WithValue(effectCtx, effectAuthorizationContextKey{}, effectAuthorization{t, request}))
		return err
	})
	if err == nil && !entered {
		err = errors.New("selected command authorization effect fence unavailable")
	}
	return result, err
}

func (t *Toolkit) reviewedProcessStart(ctx context.Context, command, cwd, operationID string) (*proc.Process, *proc.Manager, error) {
	raw, err := json.Marshal(bashArgs{Command: command, CWD: cwd, RunInBackground: true})
	if err != nil {
		return nil, nil, err
	}
	if operationID == "" {
		operationID = rand.Text()
	}
	result, err := t.commandActionGate(ctx, t, providers.ToolCall{ID: operationID, Name: "process_start", Arguments: string(raw)})
	if err != nil {
		return nil, nil, err
	}
	if result.IsError {
		return nil, nil, errors.New("reviewed process start failed")
	}
	var started proc.Process
	if err = json.Unmarshal([]byte(result.TextProjection()), &started); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(started.ID) == "" {
		return nil, nil, errors.New("reviewed process receipt omitted process identity")
	}
	manager, err := t.env.ProcessManager()
	return &started, manager, err
}
