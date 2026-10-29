package executionenv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Environment pins one profile to a session. Opening it is lazy; changing user
// defaults cannot move an existing session's tools into another filesystem.
type Environment struct {
	processes *process.Manager
	mu        sync.Mutex
	profile   Profile
	identity  string
	session   string
	stateDir  string
	client    *Client
	closed    bool
	prepared  bool
}

func NewEnvironment(profile Profile, identity, session, stateDir string) *Environment {
	return &Environment{profile: profile, identity: identity, session: session, stateDir: stateDir}
}

func (e *Environment) Execute(ctx context.Context, request ToolRequest) (toolresult.Result, error) {
	client, err := e.connect(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	data, err := client.Call(ctx, "execute", request)
	if err != nil {
		return toolresult.Result{}, err
	}
	var result toolresult.Result
	if err = json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode execution result: %w", err)
	}
	return result, nil
}

// ErrReadObservationCommitUnknown means a worker may have applied the
// versioned invalidation before its acknowledgement was lost. It must not be
// replayed against a newly started worker.
var ErrReadObservationCommitUnknown = errors.New("remote read observation commit outcome unknown")

func (e *Environment) PrepareUnobservedRead(ctx context.Context, request ToolRequest) (PreparedRead, error) {
	if request.Call.Name != "read_file" {
		return PreparedRead{}, errors.New("prepared remote continuation requires read_file")
	}
	client, err := e.connect(ctx)
	if err != nil {
		return PreparedRead{}, err
	}
	if !client.observationCommit {
		return PreparedRead{}, errors.New("execution worker does not support read observation commit")
	}
	data, err := client.Call(ctx, "read/prepare", request)
	if err != nil {
		return PreparedRead{}, err
	}
	var response struct {
		Receipt string            `json:"receipt"`
		Result  toolresult.Result `json:"result"`
	}
	if err = json.Unmarshal(data, &response); err != nil || response.Receipt == "" {
		return PreparedRead{}, errors.New("invalid prepared remote read receipt")
	}
	receipt := struct {
		Receipt                   string `json:"receipt"`
		Actor                     string `json:"actor"`
		PermissionMode            string `json:"permission_mode"`
		GitAttributionEnabled     bool   `json:"git_attribution_enabled,omitempty"`
		ShellEarlyHandoffDisabled bool   `json:"shell_early_handoff_disabled,omitempty"`
	}{response.Receipt, request.Actor, request.PermissionMode, request.GitAttributionEnabled, request.ShellEarlyHandoffDisabled}
	return PreparedRead{PermissionMode: request.PermissionMode, Result: response.Result, Commit: func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		result, err := client.Call(ctx, "read/commit", receipt)
		if err != nil {
			return false, errors.Join(ErrReadObservationCommitUnknown, err)
		}
		var ack struct {
			Committed bool `json:"committed"`
			Changed   bool `json:"changed"`
		}
		if json.Unmarshal(result, &ack) != nil || !ack.Committed {
			return false, ErrReadObservationCommitUnknown
		}
		return ack.Changed, nil
	}, Abort: func(ctx context.Context) error {
		_, err := client.Call(ctx, "read/abort", receipt)
		return err
	}}, nil
}

// ExecuteUnobservedRead requires an explicitly negotiated worker capability;
// older workers must not silently record the delegated read as model-visible.
func (e *Environment) ExecuteUnobservedRead(ctx context.Context, request ToolRequest) (toolresult.Result, error) {
	if request.Call.Name != "read_file" {
		return toolresult.Result{}, errors.New("unobserved continuation requires read_file")
	}
	client, err := e.connect(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	if !client.unobservedRead {
		return toolresult.Result{}, errors.New("execution worker does not support unobserved reads")
	}
	request.SuppressReadObservation = true
	data, err := client.Call(ctx, "execute", request)
	if err != nil {
		return toolresult.Result{}, err
	}
	var result toolresult.Result
	if err = json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode execution result: %w", err)
	}
	return result, nil
}

func (e *Environment) connect(ctx context.Context) (*Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("execution environment is closed")
	}
	if e.client != nil {
		if !e.client.Failed() {
			return e.client, nil
		}
		_ = e.client.Close()
		e.client = nil
	}
	if err := e.profile.Validate(); err != nil {
		return nil, err
	}
	argv, err := e.prepare(ctx)
	if err != nil {
		return nil, err
	}
	e.prepared = true
	env := os.Environ()
	profileJSON, _ := json.Marshal(e.profile)
	env = append(env, "WUU_EXECUTION_PROFILE="+string(profileJSON), "WUU_EXECUTION_ID="+e.identity, "WUU_EXECUTION_SESSION="+e.session, "WUU_EXECUTION_STATE="+filepath.Join(e.stateDir, e.identity))
	client := NewClient(argv, env)
	client.closeGrace = e.profile.shutdownTimeout()
	tokenDir := filepath.Join(e.stateDir, e.identity, e.session)
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		return nil, err
	}
	tokenPath := filepath.Join(tokenDir, "transport-token")
	token, err := os.ReadFile(tokenPath)
	if errors.Is(err, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return nil, err
		}
		token = []byte(hex.EncodeToString(secret))
		err = securefs.WriteFileAtomic(tokenPath, token)
	}
	if err != nil {
		return nil, err
	}
	client.token = string(token)
	if e.processes == nil {
		e.processes = process.NewRemoteManager(e.Root(), e)
	}
	{
		client.event = func(raw json.RawMessage) {
			var event process.Event
			if json.Unmarshal(raw, &event) == nil {
				e.processes.PublishRemoteEvent(event)
			}
		}
	}
	values := make(map[string]string)
	for _, name := range e.profile.ForwardEnv {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		} else {
			_ = client.Close()
			return nil, fmt.Errorf("forwarded environment variable %s is not set", name)
		}
	}
	_, markerErr := os.Stat(filepath.Join(e.stateDir, e.identity, "provisioned"))
	requireExisting := e.profile.Persistent && markerErr == nil && (e.profile.Backend == "ssh" || e.profile.Backend == "singularity")
	data, err := client.Call(ctx, "initialize", Init{RequireExistingWorkspace: requireExisting, RemoveWorkspaceOnExit: e.profile.Backend == "ssh" && !e.profile.Persistent && !e.profile.Shared, Identity: e.identity, Environment: values, Version: ProtocolVersion, Root: e.Root(), Session: e.session})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	var hello struct {
		Version      int      `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err = json.Unmarshal(data, &hello); err != nil || hello.Version != ProtocolVersion {
		_ = client.Close()
		return nil, fmt.Errorf("execution worker protocol mismatch: expected version %d, received %d; update the environment worker", ProtocolVersion, hello.Version)
	}
	client.processStart = slices.Contains(hello.Capabilities, ProcessStartCapability)
	client.unobservedRead = slices.Contains(hello.Capabilities, UnobservedReadCapability)
	client.observationCommit = slices.Contains(hello.Capabilities, ReadObservationCommitCapability)
	if err := securefs.WriteFileAtomic(filepath.Join(e.stateDir, e.identity, "provisioned"), []byte("1\n")); err != nil {
		_ = client.Close()
		return nil, err
	}
	e.client = client
	return client, nil
}

func (e *Environment) prepare(ctx context.Context) ([]string, error) {
	p := e.profile
	switch p.Backend {
	case "docker":
		if err := e.prepareDocker(ctx); err != nil {
			return nil, err
		}
		args := []string{"docker", "exec", "-i"}
		for _, name := range p.ForwardEnv {
			args = append(args, "--env", name)
		}
		return append(args, e.identity, p.WorkerExecutable(), "execution-connect", "--socket", e.identity+"-"+e.session, "--idle-seconds", strconv.Itoa(p.LifetimeSeconds)), nil
	case "ssh":
		args := []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=15"}
		if p.Port != 0 {
			args = append(args, "-p", strconv.Itoa(p.Port))
		}
		if p.KnownHostsFile != "" {
			args = append(args, "-o", "UserKnownHostsFile="+p.KnownHostsFile)
		}
		if p.IdentityFile != "" {
			args = append(args, "-i", p.IdentityFile)
		}
		return append(args, "--", p.Host, shellQuote(p.WorkerExecutable())+" execution-connect --socket "+shellQuote(e.identity+"-"+e.session)+" --idle-seconds "+strconv.Itoa(p.LifetimeSeconds)), nil
	case "singularity":
		binary, err := exec.LookPath("apptainer")
		if err != nil {
			binary, err = exec.LookPath("singularity")
		}
		if err != nil {
			return nil, errors.New("install Apptainer or Singularity to use this environment")
		}
		dir := filepath.Join(e.stateDir, e.identity)
		if p.Persistent && p.HostWorkspace == "" {
			if _, err := os.Stat(filepath.Join(dir, "provisioned")); err == nil {
				if _, err := os.Stat(filepath.Join(dir, "workspace")); err != nil {
					return nil, fmt.Errorf("retained workspace is unavailable: %w", err)
				}
			}
		}
		for _, name := range []string{"workspace", "home"} {
			if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
				return nil, err
			}
		}
		workspace := filepath.Join(dir, "workspace")
		if p.HostWorkspace != "" {
			workspace = p.HostWorkspace
		}
		mount := workspace + ":" + p.WorkingDirectory()
		if p.MountReadOnly {
			mount += ":ro"
		}
		args := []string{binary, "exec", "--containall", "--cleanenv", "--writable-tmpfs", "--home", filepath.Join(dir, "home"), "--bind", mount, "--pwd", p.WorkingDirectory()}
		if p.CPUs > 0 {
			args = append(args, "--cpus", strconv.FormatFloat(p.CPUs, 'f', -1, 64))
		}
		if p.MemoryMB > 0 {
			args = append(args, "--memory", strconv.Itoa(p.MemoryMB)+"M")
		}
		if p.Network == "none" {
			args = append(args, "--net", "--network", "none")
		}
		return append(args, p.Image, p.WorkerExecutable(), "execution-connect", "--socket", e.identity+"-"+e.session, "--idle-seconds", strconv.Itoa(p.LifetimeSeconds)), nil
	case "modal", "daytona", "vercel_sandbox":
		if len(p.Command) > 0 {
			return append([]string(nil), p.Command...), nil
		}
		python := p.Python
		if python == "" {
			python = "python3"
		}
		return []string{python, "-u", "-c", cloudAdapter}, nil
	default:
		return append([]string(nil), p.Command...), nil
	}
}

func (e *Environment) prepareDocker(ctx context.Context) error {
	// Shared conversations and independent host processes use one provisioning
	// transaction for the same environment identity.
	lock, err := storelock.Acquire(filepath.Join(e.stateDir, e.identity))
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := ctx.Err(); err != nil {
		return err
	}

	inspect := exec.CommandContext(ctx, "docker", "inspect", "--format", `{{index .Config.Labels "wuu.execution.identity"}} {{.State.Running}}`, e.identity)
	data, err := inspect.Output()
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) != 2 || fields[0] != e.identity {
			return errors.New("container identity does not match execution environment")
		}
		if fields[1] != "true" {
			if out, err := exec.CommandContext(ctx, "docker", "start", e.identity).CombinedOutput(); err != nil {
				return fmt.Errorf("start execution container: %w: %s", err, out)
			}
		}
		return nil
	}
	// A failed inspect must not be treated as a missing container while the daemon is offline.
	if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("execution container service is unavailable: %w: %s", err, out)
	}
	if e.profile.Persistent {
		if _, err := os.Stat(filepath.Join(e.stateDir, e.identity, "provisioned")); err == nil {
			return errors.New("retained execution container is missing; select a new profile to create a new environment")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	args := []string{"run", "--detach", "--init", "--name", e.identity, "--label", "wuu.execution.identity=" + e.identity, "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=512"}
	p := e.profile
	user := p.User
	if user == "" && p.HostWorkspace != "" && runtime.GOOS == "linux" {
		user = strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	}
	if user != "" {
		args = append(args, "--user", user)
	}
	args = append(args, "--env", "HOME=/tmp/wuu-home")
	if p.HostWorkspace != "" {
		mount := "type=bind,source=" + p.HostWorkspace + ",target=" + p.WorkingDirectory()
		if p.MountReadOnly {
			mount += ",readonly"
		}
		args = append(args, "--mount", mount)
	}
	if p.Network == "none" {
		args = append(args, "--network=none")
	}
	if p.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(p.CPUs, 'f', -1, 64))
	}
	if p.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(p.MemoryMB)+"m")
	}
	args = append(args, "--workdir", p.WorkingDirectory(), "--entrypoint", "/bin/sh", p.Image, "-c", `mkdir -p -m 700 "$HOME" && exec sleep infinity`)
	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("create execution container: %w: %s", err, out)
	}
	return nil
}

func (e *Environment) Close() error {
	e.mu.Lock()
	e.closed = true
	client := e.client
	e.mu.Unlock()
	if client != nil {
		if err := client.Close(); err != nil {
			// A failed shutdown is not permission to destroy its recovery state
			// or workspace. Keep the environment available for explicit recovery.
			return err
		}
	}
	if e.profile.Backend == "docker" && !e.profile.Persistent && !e.profile.Shared && e.prepared {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "rm", "--force", e.identity).CombinedOutput(); err != nil {
			return fmt.Errorf("remove execution container: %w: %s", err, out)
		}
	}
	if e.profile.Backend == "singularity" && !e.profile.Persistent && !e.profile.Shared && e.prepared {
		return os.RemoveAll(filepath.Join(e.stateDir, e.identity))
	}
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func (e *Environment) Root() string {
	if e.profile.Backend == "ssh" {
		return path.Join(e.profile.WorkingDirectory(), e.identity)
	}
	return e.profile.WorkingDirectory()
}
func (e *Environment) Backend() string { return e.profile.Backend }

func (e *Environment) Download(ctx context.Context, token string, size int64, out io.Writer) error {
	if size < 0 || size > 256*1024*1024 {
		return errors.New("invalid artifact size")
	}
	client, err := e.connect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = client.Call(closeCtx, "export/close", map[string]string{"token": token})
	}()
	var offset int64
	for {
		data, err := client.Call(ctx, "export/read", map[string]any{"token": token, "offset": offset})
		if err != nil {
			return err
		}
		var chunk struct {
			Data []byte `json:"data"`
			EOF  bool   `json:"eof"`
		}
		if err = json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		if offset+int64(len(chunk.Data)) > size {
			return errors.New("artifact exceeds declared size")
		}
		if _, err = out.Write(chunk.Data); err != nil {
			return err
		}
		offset += int64(len(chunk.Data))
		if chunk.EOF {
			if offset != size {
				return errors.New("artifact transfer was truncated")
			}
			return nil
		}
		if len(chunk.Data) == 0 {
			return errors.New("empty artifact transfer chunk")
		}
	}
}

func (e *Environment) RunCode(ctx context.Context, request CodeRequest, executor toolctx.NestedExecutor) (codemode.RunResult, error) {
	client, err := e.connect(ctx)
	if err != nil {
		return codemode.RunResult{}, err
	}
	data, err := client.CallWithHandler(ctx, "run_code", request, func(callCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var call providers.ToolCall
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		result, err := executor.Invoke(callCtx, call)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
	if err != nil {
		return codemode.RunResult{}, err
	}
	var result codemode.RunResult
	err = json.Unmarshal(data, &result)
	return result, err
}

func (e *Environment) ProcessManager() *process.Manager {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.processes == nil {
		e.processes = process.NewRemoteManager(e.Root(), e)
	}
	return e.processes
}

func (e *Environment) Request(ctx context.Context, method string, params, result any) error {
	// Inspecting an unused conversation must not provision a paid environment.
	if method == "list" {
		if _, err := os.Stat(filepath.Join(e.stateDir, e.identity, "provisioned")); errors.Is(err, os.ErrNotExist) {
			return json.Unmarshal([]byte("[]"), result)
		} else if err != nil {
			return err
		}
	}
	client, err := e.connect(ctx)
	if err != nil {
		return err
	}
	data, err := client.Call(ctx, "process/"+method, params)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

// StartProcess submits once to the captured environment. A failed transport can
// leave an uncertain result; it is never retried or substituted with local work.
func (e *Environment) StartProcess(ctx context.Context, request ProcessRequest) (*process.Process, error) {
	client, err := e.connect(ctx)
	if err != nil {
		return nil, err
	}
	if !client.processStart {
		return nil, errors.New("execution worker does not support plugin process starts; update the environment worker")
	}
	request.OperationID = "operation-" + rand.Text()
	data, callErr := client.Call(ctx, "process/start", request)
	if len(data) == 0 && callErr != nil {
		return nil, fmt.Errorf("remote process operation %s in environment %s did not return a receipt; outcome may be unknown, inspect process records before another start: %w", request.OperationID, e.identity, callErr)
	}
	var started process.Process
	if err = json.Unmarshal(data, &started); err != nil || started.ID == "" || started.OperationID != request.OperationID || started.RootThreadID != e.session || started.OwnerID != request.Actor {
		return nil, fmt.Errorf("remote process operation %s in environment %s returned an invalid receipt; outcome unknown", request.OperationID, e.identity)
	}
	if err = errors.Join(callErr, ctx.Err()); err != nil {
		// A cancelled call can race the worker's success response. Retain that
		// response until cleanup targets the original process, never a replacement.
		_, stopErr := e.ProcessManager().Stop(started.ID)
		return nil, errors.Join(err, stopErr)
	}
	return &started, nil
}

// Identity is the immutable binding selected when this conversation was opened.
// Reading it, Root or Backend does not connect to or provision the environment.
func (e *Environment) Identity() string { return e.identity }
