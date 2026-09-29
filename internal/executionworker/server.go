// Package executionworker runs workspace tools behind a private transport.
package executionworker

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type actor struct{ kit *tools.Toolkit }

type Server struct {
	code         *codemode.Service
	token        string
	requireToken bool
	mu           sync.Mutex
	init         *executionenv.Init
	actors       map[string]*actor
	processes    *process.Manager
	exports      map[string]exportFile
}

func New() *Server {
	return &Server{actors: make(map[string]*actor), code: codemode.NewService(codemode.ServiceConfig{})}
}

func (s *Server) Handle(ctx context.Context, method string, data json.RawMessage) (json.RawMessage, error) {
	if strings.HasPrefix(method, "process/") {
		return s.process(ctx, method, data)
	}
	switch method {
	case "export/read", "export/close":
		return s.export(method, data)
	case "initialize":
		s.mu.Lock()
		defer s.mu.Unlock()

		var cfg executionenv.Init
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		if cfg.Version != executionenv.ProtocolVersion {
			return nil, errors.New("execution worker protocol mismatch; update the environment worker")
		}
		if !filepath.IsAbs(cfg.Root) || cfg.Session == "" || filepath.Base(cfg.Session) != cfg.Session || cfg.Session == "." || cfg.Session == ".." {
			return nil, errors.New("invalid execution worker identity or workspace")
		}
		if cfg.RequireExistingWorkspace {
			if _, err := os.Stat(cfg.Root); err != nil {
				return nil, fmt.Errorf("retained workspace is unavailable: %w", err)
			}
		}
		if err := os.MkdirAll(cfg.Root, 0700); err != nil {
			return nil, err
		}
		root, err := filepath.EvalSymlinks(cfg.Root)
		if err != nil {
			return nil, err
		}
		cfg.Root = root
		if s.init != nil {
			if s.init.Session != cfg.Session || s.init.Root != cfg.Root {
				return nil, errors.New("execution worker identity mismatch")
			}
			return json.Marshal(map[string]any{"version": executionenv.ProtocolVersion, "root": cfg.Root})
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		if cfg.Identity != "" && (filepath.Base(cfg.Identity) != cfg.Identity || cfg.Identity == "." || cfg.Identity == "..") {
			return nil, errors.New("invalid environment identity")
		}
		runtimeDir := filepath.Join(home, ".wuu-execution", cfg.Identity, cfg.Session)
		manager, err := process.NewManager(cfg.Root, filepath.Join(runtimeDir, "runtime"))
		if err != nil {
			return nil, err
		}
		for name, value := range cfg.Environment {
			if err := os.Setenv(name, value); err != nil {
				return nil, err
			}
		}
		s.init, s.processes = &cfg, manager
		return json.Marshal(map[string]any{"version": executionenv.ProtocolVersion, "root": cfg.Root})
	case "run_code":
		var request executionenv.CodeRequest
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, err
		}
		a, err := s.actor(request.Actor, request.PermissionMode, request.GitAttributionEnabled)
		if err != nil {
			return nil, err
		}
		executor, ok := toolctx.Nested(ctx)
		if !ok {
			return nil, errors.New("missing parent tool execution scope")
		}
		request.Program.TimeoutMS = request.TimeoutMS
		result, err := a.kit.RunEnvironmentCode(ctx, s.code, request.Program, executor)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "execute":
		var request executionenv.ToolRequest
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, err
		}
		if !executionenv.WorkspaceTool(request.Call.Name) {
			return nil, fmt.Errorf("tool %q is not an execution environment tool", request.Call.Name)
		}
		a, err := s.actor(request.Actor, request.PermissionMode, request.GitAttributionEnabled)
		if err != nil {
			return nil, err
		}

		result, err := a.kit.ExecuteEnvironmentResult(ctx, request.Call)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	default:
		return nil, fmt.Errorf("unknown execution worker method %q", method)
	}
}

func (s *Server) actor(id, mode string, attribution bool) (*actor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.init == nil {
		return nil, errors.New("execution worker is not initialized")
	}
	if id == "" {
		return nil, errors.New("execution actor is required")
	}
	key := id + "\x00" + mode + "\x00" + strconv.FormatBool(attribution)
	if a := s.actors[key]; a != nil {
		return a, nil
	}
	kit, err := tools.New(s.init.Root)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "standard", "":
		kit.SetBoundary(tools.StandardBoundary())
	case "read_only":
		kit.SetBoundary(tools.ReadOnlyBoundary())
	case "unconfined":
		kit.SetBoundary(tools.UnconfinedBoundary())
	default:
		return nil, errors.New("invalid execution permission mode")
	}
	kit.SetPermissionMode(mode)
	kit.SetSessionID(s.init.Session)
	actorPath := id
	if id == s.init.Session {
		actorPath = agentthread.RootPath
	}
	kit.SetAgentIdentity(id, actorPath)
	kit.SetProcessManager(s.processes)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	state := filepath.Join(home, ".wuu-execution", s.init.Identity, s.init.Session)
	kit.SetStateDir(state)
	kit.SetSessionDir(filepath.Join(state, "artifacts"))
	kit.SetFileScopeRoots([]string{s.init.Root})
	kit.SetGitAttributionEnabled(attribution)
	kit.SetArtifactPublisher(s.publish)
	a := &actor{kit: kit}
	s.actors[key] = a
	return a, nil
}

func (s *Server) Close() error {
	_ = s.code.Close()
	s.mu.Lock()
	manager := s.processes
	for token, file := range s.exports {
		_ = file.file.Close()
		_ = os.Remove(file.file.Name())
		delete(s.exports, token)
	}
	s.mu.Unlock()
	if manager == nil {
		return nil
	}
	err := manager.CleanupSession()
	if s.init != nil && s.init.RemoveWorkspaceOnExit {
		err = errors.Join(err, os.RemoveAll(s.init.Root))
	}
	return err
}

// Serve accepts independent request IDs and addressed cancellation. EOF cancels
// active calls and stops owned background processes before the worker exits.
func Serve(parent context.Context, in io.Reader, out io.Writer) error {
	server := New()
	defer server.Close()
	return serveConnection(parent, in, out, server)
}

func serveConnection(parent context.Context, in io.Reader, out io.Writer, server *Server) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var mu, outputMu sync.Mutex
	pending := make(map[string]context.CancelFunc)
	replies := make(map[string]chan executionenv.Response)
	var running sync.WaitGroup
	defer func() { cancel(); running.Wait() }()
	var subscribe sync.Once
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), executionenv.MaxFrameBytes)
	encode := func(response executionenv.Response) {
		outputMu.Lock()
		defer outputMu.Unlock()
		_ = json.NewEncoder(out).Encode(response)
	}
	for scanner.Scan() {
		var request executionenv.Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			cancel()
			running.Wait()
			return err
		}
		server.mu.Lock()
		if server.token == "" && request.Method == "initialize" && len(request.Token) >= 32 {
			server.token = request.Token
		}
		authorized := (!server.requireToken && server.token == "") || (len(server.token) > 0 && subtle.ConstantTimeCompare([]byte(server.token), []byte(request.Token)) == 1)
		server.mu.Unlock()
		if !authorized {
			encode(executionenv.Response{ID: request.ID, Error: "execution transport authentication failed"})
			continue
		}
		if request.Method == "callback" {
			var response executionenv.Response
			if err := json.Unmarshal(request.Data, &response); err != nil {
				return err
			}
			mu.Lock()
			reply := replies[request.ID]
			delete(replies, request.ID)
			mu.Unlock()
			if reply != nil {
				reply <- response
			}
			continue
		}
		mu.Lock()
		if request.Method == "cancel" {
			if stop := pending[request.ID]; stop != nil {
				stop()
			}
			mu.Unlock()
			continue
		}
		if request.ID == "" || pending[request.ID] != nil {
			mu.Unlock()
			encode(executionenv.Response{ID: request.ID, Error: "missing or duplicate request id"})
			continue
		}
		callCtx, stop := context.WithCancel(ctx)
		pending[request.ID] = stop
		mu.Unlock()
		running.Add(1)
		go func(req executionenv.Request) {
			defer running.Done()
			defer stop()
			bridge := callbackExecutor{invoke: func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
				id := rand.Text()
				reply := make(chan executionenv.Response, 1)
				mu.Lock()
				replies[id] = reply
				mu.Unlock()
				defer func() { mu.Lock(); delete(replies, id); mu.Unlock() }()
				data, _ := json.Marshal(call)
				encode(executionenv.Response{ID: id, ParentID: req.ID, Method: "tool", Data: data})
				select {
				case response := <-reply:
					if response.Error != "" {
						return toolresult.Result{}, errors.New(response.Error)
					}
					var result toolresult.Result
					err := json.Unmarshal(response.Data, &result)
					return result, err
				case <-ctx.Done():
					return toolresult.Result{}, ctx.Err()
				}
			}}
			data, err := server.Handle(toolctx.WithNestedExecutor(callCtx, bridge), req.Method, req.Data)
			if req.Method == "initialize" && err == nil {
				subscribe.Do(func() {
					events := make(chan process.Event, 64)
					server.mu.Lock()
					manager := server.processes
					server.mu.Unlock()
					manager.Subscribe(events)
					go func() {
						defer manager.Unsubscribe(events)
						for {
							select {
							case event := <-events:
								raw, _ := json.Marshal(event)
								encode(executionenv.Response{Method: "process", Data: raw})
							case <-ctx.Done():
								return
							}
						}
					}()
				})
			}
			response := executionenv.Response{ID: req.ID, Data: data}
			if err != nil {
				response.Error = err.Error()
			}
			mu.Lock()
			delete(pending, req.ID)
			mu.Unlock()
			encode(response)
		}(request)
	}
	cancel()
	running.Wait()
	return scanner.Err()
}

type callbackExecutor struct {
	invoke func(context.Context, providers.ToolCall) (toolresult.Result, error)
}

func (e callbackExecutor) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	return e.invoke(ctx, call)
}
