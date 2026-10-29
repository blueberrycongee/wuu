package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ProcessScopeConfig contains host resources and authority only. Constructing a
// scope does not register tools, connect MCP, start Code Mode or create processes.
type ProcessScopeConfig struct {
	Manager                       *proc.Manager
	ExecutionEnvironment          executionenv.Executor
	GitAttributionEnabled         bool
	RootDir, StateDir, SessionDir string
	ThreadID, ActorID, ActorPath  string
	PermissionMode                string
	Boundary                      WorkspaceBoundary
	FileScopeRoots                []string
	ProtectedPaths                []string
	Authorizer                    Authorizer
	SandboxProvider               processsandbox.Provider
	BeforePermission              func(context.Context, ToolInfo, providers.ToolCall) error
	Reviewer                      Reviewer
	ApproveForMe                  bool
	ExecutionRoot                 func(context.Context) (string, error)
	CommandPrefix                 func() (string, error)
	Disabled                      bool
}

type ProcessScope struct{ config ProcessScopeConfig }

func NewProcessScope(config ProcessScopeConfig) *ProcessScope {
	config.FileScopeRoots = append([]string(nil), config.FileScopeRoots...)
	config.ProtectedPaths = append([]string(nil), config.ProtectedPaths...)
	config.Boundary.Guards = append([]Guard(nil), config.Boundary.Guards...)
	return &ProcessScope{config: config}
}

// Bind is called by host dispatch with authenticated execution metadata. A
// conversation-bound scope cannot be borrowed by another thread or actor.
func (s *ProcessScope) Bind(thread, actor, cwd string) (*ProcessScope, error) {
	if s == nil {
		return nil, errors.New("host process scope unavailable")
	}
	c := s.config
	if thread == "" || actor == "" || cwd == "" || (c.ThreadID != "" && c.ThreadID != thread) || (c.ActorID != "" && c.ActorID != actor) || filepath.Clean(c.RootDir) != filepath.Clean(cwd) {
		return nil, errors.New("host process scope does not match authenticated execution")
	}
	c.ThreadID, c.ActorID = thread, actor
	return NewProcessScope(c), nil
}

// ProcessEnvironment reports the captured location without provisioning it.
type ProcessEnvironment struct {
	Backend  string `json:"backend"`
	Root     string `json:"root"`
	Identity string `json:"identity,omitempty"`
}

func (s *ProcessScope) Environment() (ProcessEnvironment, error) {
	if s == nil {
		return ProcessEnvironment{}, errors.New("host process scope unavailable")
	}
	if s.config.ExecutionEnvironment == nil {
		return ProcessEnvironment{Backend: "local", Root: s.config.RootDir}, nil
	}
	remote, ok := s.config.ExecutionEnvironment.(executionenv.ProcessExecutor)
	if !ok {
		return ProcessEnvironment{}, errors.New("selected execution environment does not support plugin processes")
	}
	location := ProcessEnvironment{Backend: remote.Backend(), Root: remote.Root()}
	if identity, ok := remote.(interface{ Identity() string }); ok {
		location.Identity = identity.Identity()
	}
	return location, nil
}

func (s *ProcessScope) Start(ctx context.Context, command, cwd string) (*proc.Process, *proc.Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if s == nil {
		return nil, nil, errors.New("host process scope unavailable")
	}
	c := s.config
	if (c.Manager == nil && c.ExecutionEnvironment == nil) || c.RootDir == "" || c.ThreadID == "" || c.ThreadID == "session-pending" || c.ActorID == "" {
		return nil, nil, errors.New("process start requires host manager, workspace, thread and actor")
	}
	// Detached process records do not yet retain immutable protected-path
	// authority across recovery. Never admit a process that could lose it.
	if len(c.ProtectedPaths) > 0 {
		return nil, nil, errors.New("protected read/write policy does not support background processes")
	}
	if c.Disabled {
		return nil, nil, errors.New("process execution is disabled in this conversation")
	}
	if c.Authorizer == nil {
		return nil, nil, errors.New("process authorization provider is unavailable")
	}
	location, err := s.Environment()
	if err != nil {
		return nil, nil, err
	}
	args := bashArgs{Command: command, CWD: cwd, RunInBackground: true}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, nil, err
	}
	call := providers.ToolCall{Name: "bash", Arguments: string(raw)}
	classification := (&BashTool{}).Classify(call.Arguments)
	info := ToolInfo{Name: "bash", Kind: ToolKindShell, Exposure: ToolExposureDirect, Risk: classification.Risk, ReadOnly: classification.ReadOnly, ConcurrencySafe: classification.ConcurrencySafe, Destructive: classification.Destructive, Reason: classification.Reason}
	if c.BeforePermission != nil {
		if err := c.BeforePermission(ctx, info, call); err != nil {
			return nil, nil, err
		}
	}
	decision, err := c.Authorizer.Authorize(ctx, AuthorizationRequest{SessionID: c.ThreadID, ActorID: c.ActorID, CWD: location.Root, PermissionMode: c.PermissionMode, Tool: info, Arguments: call.Arguments})
	if err != nil {
		return nil, nil, authorizationDenied(info.Name, "authorization provider unavailable")
	}
	if strings.TrimSpace(decision.Outcome) != "allow" {
		return nil, nil, authorizationDenied(info.Name, decision.Reason)
	}
	if err := c.Boundary.Check(info, call); err != nil {
		return nil, nil, err
	}
	if err := reviewAuthorizedToolCall(ctx, reviewAuthority{SessionID: c.ThreadID, PermissionMode: c.PermissionMode, CWD: location.Root, Enabled: c.ApproveForMe, Reviewer: c.Reviewer}, info, call); err != nil {
		return nil, nil, err
	}
	return s.startAuthorized(ctx, command, cwd, "")
}

// startAuthorized is shared with the execution worker only after its native
// permission gate. The host path above still requires a selected authorizer.
func (s *ProcessScope) startAuthorized(ctx context.Context, command, cwd, operationID string) (*proc.Process, *proc.Manager, error) {
	c := s.config
	var err error
	if c.ExecutionEnvironment != nil {
		// The worker owns path resolution, environment and sandbox construction.
		// Never apply host paths or inherited host secrets to a remote command.
		remote := c.ExecutionEnvironment.(executionenv.ProcessExecutor)
		manager := remote.ProcessManager()
		if manager == nil {
			return nil, nil, errors.New("selected execution environment process manager is unavailable")
		}
		started, err := remote.StartProcess(ctx, executionenv.ProcessRequest{Command: command, CWD: cwd, PermissionMode: c.PermissionMode, Actor: c.ActorID, GitAttributionEnabled: c.GitAttributionEnabled})
		return started, manager, err
	}
	execRoot := c.RootDir
	if c.ExecutionRoot != nil {
		execRoot, err = c.ExecutionRoot(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	policy, tempDir, err := (processSandboxScope{root: c.RootDir, stateDir: c.StateDir, sessionDir: c.SessionDir, sessionID: c.ThreadID, execRoot: execRoot, roots: c.FileScopeRoots, protectedPaths: c.ProtectedPaths, unconfined: !c.Boundary.Enforce, allowMutations: c.Boundary.AllowMutations, boundaryConfigured: true, provider: c.SandboxProvider}).policy(processsandbox.Supported())
	if err != nil {
		return nil, nil, fmt.Errorf("prepare filesystem process sandbox: %w", err)
	}
	prefix := ""
	if c.CommandPrefix != nil {
		prefix, err = c.CommandPrefix()
		if err != nil {
			return nil, nil, err
		}
	}
	env := shellCommandEnv(os.Environ())
	if tempDir != "" {
		env = replaceCommandEnv(env, "TMPDIR", tempDir)
	}
	owner := proc.OwnerMainAgent
	if c.ActorPath != "" && c.ActorPath != agentthread.RootPath {
		owner = proc.OwnerSubagent
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	started, err := c.Manager.Start(ctx, proc.StartOptions{OperationID: operationID, Command: command, CommandPrefix: prefix, CWD: cwd, WorkspaceRoot: c.RootDir, OwnerKind: owner, OwnerID: c.ActorID, RootThreadID: c.ThreadID, Lifecycle: proc.LifecycleManaged, CompletionMode: proc.CompletionModeDetached, AllowOutsideWorkspace: !c.Boundary.Enforce, SandboxPolicy: policy, SandboxProvider: c.SandboxProvider, Env: env})
	return started, c.Manager, err
}
