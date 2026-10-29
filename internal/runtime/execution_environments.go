package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func (s *Session) executionEnvironmentManager() *executionenv.Manager {
	s.threadProcessMu.Lock()
	defer s.threadProcessMu.Unlock()
	if s.executionEnvironments == nil {
		s.executionEnvironments = executionenv.NewManager()
	}
	return s.executionEnvironments
}

func (s *Session) configureExecutionEnvironment(kit *tools.Toolkit, id, artifactDir string) error {
	executor, err := s.selectedExecutionEnvironment(id, artifactDir)
	if err != nil {
		return err
	}
	kit.SetExecutionEnvironment(executor)
	return nil
}

func (s *Session) selectedExecutionEnvironment(id, artifactDir string) (executionenv.Executor, error) {
	environment, _, err := s.executionEnvironmentManager().Open(s.WuuHome, id, artifactDir, executionenv.Config{})
	if err != nil {
		return nil, fmt.Errorf("select execution environment: %w", err)
	}
	if environment == nil {
		return nil, nil
	}
	return &environmentToolExecutor{environment: environment, publish: NewArtifactPublisher(s.artifactClient), session: id, stateDir: s.StateDir, artifactDir: artifactDir}, nil
}

// Artifacts cross the execution boundary as immutable byte snapshots. Worker
// paths are never interpreted as host paths.
type environmentToolExecutor struct {
	environment                    *executionenv.Environment
	publish                        tools.ArtifactPublisher
	session, stateDir, artifactDir string
}

func (e *environmentToolExecutor) Root() string     { return e.environment.Root() }
func (e *environmentToolExecutor) Backend() string  { return e.environment.Backend() }
func (e *environmentToolExecutor) Identity() string { return e.environment.Identity() }
func (e *environmentToolExecutor) StartProcess(ctx context.Context, request executionenv.ProcessRequest) (*process.Process, error) {
	return e.environment.StartProcess(ctx, request)
}

func (e *environmentToolExecutor) Execute(ctx context.Context, request executionenv.ToolRequest) (toolresult.Result, error) {
	result, err := e.environment.Execute(ctx, request)
	if err != nil || result.IsError || request.Call.Name != "present_artifact" {
		return result, err
	}
	var descriptor struct {
		Token string `json:"execution_export"`
		Name  string `json:"name"`
		Size  int64  `json:"size"`
	}
	if err = json.Unmarshal([]byte(result.TextProjection()), &descriptor); err != nil {
		return toolresult.Result{}, err
	}
	if descriptor.Token == "" || filepath.Base(descriptor.Name) != descriptor.Name || descriptor.Name == "." {
		return toolresult.Result{}, errors.New("invalid artifact transfer descriptor")
	}
	if err = os.MkdirAll(e.artifactDir, 0700); err != nil {
		return toolresult.Result{}, err
	}
	staging, err := os.MkdirTemp(e.artifactDir, "environment-artifact-*")
	if err != nil {
		return toolresult.Result{}, err
	}
	defer os.RemoveAll(staging)
	path := filepath.Join(staging, descriptor.Name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return toolresult.Result{}, err
	}
	err = e.environment.Download(ctx, descriptor.Token, descriptor.Size, file)
	closeErr := file.Close()
	if err != nil {
		return toolresult.Result{}, err
	}
	if closeErr != nil {
		return toolresult.Result{}, closeErr
	}
	part, err := e.publish(ctx, tools.ArtifactPublishRequest{Path: path, ThreadID: e.session, StateDir: e.stateDir, CWD: e.Root(), CallID: request.Call.ID})
	return toolresult.Result{Content: []toolresult.ContentPart{part}}, err
}

func (e *environmentToolExecutor) RecoverReadBatch(ctx context.Context, identity executionenv.ReadBatchIdentity) (*executionenv.PreparedReadBatch, error) {
	return e.environment.RecoverReadBatch(ctx, identity)
}

func (e *environmentToolExecutor) PrepareNativeRead(ctx context.Context, request executionenv.ToolRequest) (executionenv.PreparedRead, error) {
	return e.environment.PrepareNativeRead(ctx, request)
}

func (e *environmentToolExecutor) PrepareUnobservedRead(ctx context.Context, request executionenv.ToolRequest) (executionenv.PreparedRead, error) {
	return e.environment.PrepareUnobservedRead(ctx, request)
}

func (e *environmentToolExecutor) PrepareUnobservedReadOnWorker(ctx context.Context, request executionenv.ToolRequest, expected string) (executionenv.PreparedRead, error) {
	return e.environment.PrepareUnobservedReadOnWorker(ctx, request, expected)
}

func (e *environmentToolExecutor) ExecuteUnobservedRead(ctx context.Context, request executionenv.ToolRequest) (toolresult.Result, error) {
	return e.environment.ExecuteUnobservedRead(ctx, request)
}

func (e *environmentToolExecutor) ExecuteUnobservedReadOnWorker(ctx context.Context, request executionenv.ToolRequest, expected string) (toolresult.Result, string, error) {
	return e.environment.ExecuteUnobservedReadOnWorker(ctx, request, expected)
}

func (e *environmentToolExecutor) RunCode(ctx context.Context, request executionenv.CodeRequest, executor toolctx.NestedExecutor) (codemode.RunResult, error) {
	return e.environment.RunCode(ctx, request, executor)
}

func (e *environmentToolExecutor) ProcessManager() *process.Manager {
	return e.environment.ProcessManager()
}

// PinExecutionEnvironment records the choice when a conversation is created.
// Older conversations without a record retain local execution.
func (s *Session) PinExecutionEnvironment(id, parentID string) error {
	stateDir, err := s.executionStateDir()
	if err != nil {
		return err
	}
	cfg := s.executionEnvironmentConfig
	if parentID != "" {
		_, selection, err := s.executionEnvironmentManager().Open(s.WuuHome, parentID, statepath.SessionArtifactDir(stateDir, parentID), executionenv.Config{})
		if err != nil {
			return err
		}
		cfg = executionenv.Config{Default: selection.Name}
		if selection.Profile != nil {
			cfg.Profiles = map[string]executionenv.Profile{selection.Name: *selection.Profile}
		}
	}
	_, _, err = s.executionEnvironmentManager().Open(s.WuuHome, id, statepath.SessionArtifactDir(stateDir, id), cfg)
	return err
}

func (s *Session) RemoteProcesses(id string) (*process.Manager, error) {
	stateDir, err := s.executionStateDir()
	if err != nil {
		return nil, err
	}
	environment, _, err := s.executionEnvironmentManager().Open(s.WuuHome, id, statepath.SessionArtifactDir(stateDir, id), executionenv.Config{})
	if err != nil || environment == nil {
		return nil, err
	}
	return environment.ProcessManager(), nil
}

func (s *Session) executionStateDir() (string, error) {
	if s.StateDir != "" {
		return s.StateDir, nil
	}
	home, err := statepath.Home("")
	if err != nil {
		return "", err
	}
	return resolveWorkspaceStateDir(home, s.WorkspaceID, s.RootDir)
}
