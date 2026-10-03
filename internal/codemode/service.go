package codemode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

//go:embed runtime.mjs
var bootstrap string

//go:embed vendor/quickjs/*
var runtimeAssets embed.FS

// Reconstruct only a complete, integrity-verified embedded interpreter image.
func loadRuntimeWasm(assets fs.FS) ([]byte, error) {
	const chunkSize = 64 * 1024
	manifest, err := fs.ReadFile(assets, "vendor/quickjs/wasm.json")
	if err != nil {
		return nil, fmt.Errorf("read interpreter manifest: %w", err)
	}
	var metadata struct {
		Size   int    `json:"size"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(manifest, &metadata); err != nil {
		return nil, fmt.Errorf("decode interpreter manifest: %w", err)
	}
	if metadata.Size <= 0 || metadata.Size > maxFrameBytes {
		return nil, errors.New("invalid interpreter image size")
	}
	expectedHash, err := hex.DecodeString(metadata.SHA256)
	if err != nil || len(expectedHash) != sha256.Size {
		return nil, errors.New("invalid interpreter image digest")
	}
	count := (metadata.Size + chunkSize - 1) / chunkSize
	chunks, err := fs.Glob(assets, "vendor/quickjs/quickjs.wasm.*")
	if err != nil {
		return nil, err
	}
	if len(chunks) != count {
		return nil, errors.New("interpreter chunk count mismatch")
	}
	image := make([]byte, 0, metadata.Size)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("vendor/quickjs/quickjs.wasm.%03d", index)
		expectedSize := min(chunkSize, metadata.Size-len(image))
		info, err := fs.Stat(assets, name)
		if err != nil {
			return nil, fmt.Errorf("read interpreter chunk: %w", err)
		}
		if info.Size() != int64(expectedSize) {
			return nil, errors.New("interpreter chunk size mismatch")
		}
		chunk, err := fs.ReadFile(assets, name)
		if err != nil {
			return nil, fmt.Errorf("read interpreter chunk: %w", err)
		}
		if len(chunk) != expectedSize {
			return nil, errors.New("interpreter chunk size mismatch")
		}
		image = append(image, chunk...)
	}
	digest := sha256.Sum256(image)
	if hex.EncodeToString(digest[:]) != metadata.SHA256 {
		return nil, errors.New("interpreter image digest mismatch")
	}
	return image, nil
}

// MaxTimeoutMS is the largest millisecond timeout representable by time.Duration.
const MaxTimeoutMS = int64((1<<63 - 1) / time.Millisecond)
const defaultOutputBytes = 1024 * 1024
const maxPendingCalls = 128

// State limits bound retained JSON separately from printed and returned output.
// Bytes count UTF-8 JSON-encoded keys and values, without transport framing.
const (
	MaxStateBytes        = 1024 * 1024
	MaxStateKeys         = 256
	MaxStateScopes       = 64
	MaxServiceStateBytes = 16 * 1024 * 1024
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ServiceConfig struct{ NodeExecutable string }

type RunRequest struct {
	Code      string           `json:"code"`
	Tools     []ToolDefinition `json:"tools"`
	TimeoutMS int              `json:"-"` // Zero adds no deadline; the parent context still applies.
}
type RunOptions struct {
	CWD             string
	Executor        toolctx.NestedExecutor
	Sandbox         *processsandbox.Policy
	SandboxProvider processsandbox.Provider
	MaxOutputBytes  int
	// StateScope is a caller-owned session/actor identity, never program input.
	// Empty disables store/load/remove. Only one program may run in a scope at a time.
	// A canceled predecessor is drained before admission; live overlaps are rejected.
	StateScope string
	// StateOwner is the host conversation whose deletion ends this state lifetime.
	// Actors and execution roots keep distinct scopes under the same owner.
	StateOwner string
}
type RunResult struct {
	Logs  []string
	Value json.RawMessage
	Error string
	Media []toolresult.ContentPart
	// CallSummary is a bounded recovery diagnostic, populated only on failure.
	CallSummary string `json:",omitempty"`
}

// Service owns running programs and bounded JSON state, not a persistent
// JavaScript kernel. Every Run creates a fresh Node process and interpreter;
// failed or interrupted programs are never replayed. State is memory-only and
// isolated by service and caller-supplied scope. Close cancels runs and clears it.
type Service struct {
	config     ServiceConfig
	mu         sync.Mutex
	closed     bool
	closedCh   chan struct{}
	active     map[string]activeRun
	done       sync.WaitGroup
	scopes     map[string]*stateScope
	stateBytes int
}

type stateScope struct {
	owner string
	ctx   context.Context
	done  chan struct{}
	bytes int
	// Keep both keys and values as JSON text so UTF-16 strings survive transport
	// without Go's JSON decoder replacing unpaired surrogate code units.
	values map[string]string
}

type activeRun struct {
	owner  string
	cancel context.CancelFunc
}

func NewService(config ServiceConfig) *Service {
	return &Service{config: config, active: map[string]activeRun{}, scopes: map[string]*stateScope{}, closedCh: make(chan struct{})}
}

func (s *Service) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.closedCh)
	}
	s.scopes = nil
	s.stateBytes = 0
	for _, run := range s.active {
		run.cancel()
	}
	s.mu.Unlock()
	s.done.Wait()
	return nil
}

// ForgetOwner releases one deleted conversation's state and cancels its already
// registered programs, including calls waiting for a canceled predecessor.
// Hosts must prevent new calls for deleted conversations through their normal
// lifecycle admission; this does not keep a permanent tombstone for the owner.
func (s *Service) ForgetOwner(owner string) {
	if owner == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.active {
		if run.owner == owner {
			run.cancel()
		}
	}
	for key, scope := range s.scopes {
		if scope.owner != owner {
			continue
		}
		s.stateBytes -= scope.bytes
		// A running program may still be serializing its committed snapshot.
		// Drop the reference without mutating that immutable map.
		scope.values, scope.bytes = nil, 0
		if scope.done == nil {
			delete(s.scopes, key)
		}
	}
}

// Run retains printed/returned values, media, and successful store()/remove() mutations.
// load() returns a fresh clone of the scope's state, including staged writes.
// remove() returns whether the key existed and reclaims its byte/key budget;
// removing the last key also releases the scope slot after successful commit.
// State commits only after successful completion and cleanup; errors, cancellation,
// and state-limit rejection preserve the prior commit. Nested tool effects are
// not transactional and are not rolled back when program state is discarded.
// Calls use the caller's policy, scheduler and durable ledger. The interpreter
// has no native capabilities. An explicit deadline includes tool/approval waits.
func (s *Service) Run(parent context.Context, request RunRequest, opts RunOptions) (result RunResult, err error) {
	var history callHistory
	defer func() {
		// State commit can still fail after execution cleanup has finished.
		if result.Error != "" {
			result.CallSummary = history.summary()
		}
	}()
	if strings.TrimSpace(request.Code) == "" {
		return result, errors.New("PTC requires code")
	}
	catalog, catalogErr := newToolCatalog(request.Tools)
	if catalogErr != nil {
		return result, catalogErr
	}
	timeout := request.TimeoutMS
	if timeout < 0 || int64(timeout) > MaxTimeoutMS {
		return result, fmt.Errorf("PTC timeout_ms must be between 0 and %d (0 adds no deadline)", MaxTimeoutMS)
	}
	lifetime := parent
	if timeout > 0 {
		var cancelDeadline context.CancelFunc
		lifetime, cancelDeadline = context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
		defer cancelDeadline()
	}
	ownerContext, cancelOwner := context.WithCancel(lifetime)
	defer cancelOwner()
	// Execution cleanup cancels its I/O even after a successful program. Keep
	// that distinct from owner cancellation, which also prevents state commit.
	ctx, cancel := context.WithCancel(ownerContext)
	defer cancel()
	id := rand.Text()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return result, errors.New("PTC runtime is closed")
	}
	// Register before waiting for scope admission so owner deletion and Close
	// also cancel/drain callers that have not started an interpreter yet.
	s.active[id] = activeRun{owner: opts.StateOwner, cancel: cancelOwner}
	s.done.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
		s.done.Done()
	}()
	var scope *stateScope
	var committed map[string]string
	var staged map[string]string
	completed := false
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return result, errors.New("PTC runtime is closed")
		}
		if ownerContext.Err() != nil {
			s.mu.Unlock()
			result.Error = ownerContext.Err().Error()
			return result, nil
		}
		if opts.StateScope != "" {
			scope = s.scopes[opts.StateScope]
			if scope != nil && scope.owner != opts.StateOwner {
				s.mu.Unlock()
				return result, errors.New("PTC state scope belongs to a different owner")
			}
			if scope != nil && scope.done != nil {
				if scope.ctx.Err() == nil {
					s.mu.Unlock()
					return result, errors.New("PTC state scope already running; await the earlier program")
				}
				// Cancellation can return through a caller's transport before its
				// executor finishes. Drain that run without replaying either program.
				drained := scope.done
				s.mu.Unlock()
				select {
				case <-drained:
					continue
				case <-ownerContext.Done():
					// Recheck under the lock so Close keeps its established
					// closed-runtime error even when both signals are ready.
					continue
				case <-s.closedCh:
					return result, errors.New("PTC runtime is closed")
				}
			}
			if scope == nil {
				if len(s.scopes) >= MaxStateScopes {
					s.mu.Unlock()
					return result, fmt.Errorf("PTC state scope limit exceeded (%d)", MaxStateScopes)
				}
				scope = &stateScope{owner: opts.StateOwner}
				s.scopes[opts.StateScope] = scope
			}
		}
		if scope != nil {
			scope.ctx, scope.done = ctx, make(chan struct{})
			committed = scope.values
		}
		s.mu.Unlock()
		break
	}
	defer func() {
		s.mu.Lock()
		if scope != nil {
			if completed && err == nil && result.Error == "" {
				switch {
				case ownerContext.Err() != nil:
					result.Error = ownerContext.Err().Error()
				case s.closed:
					result.Error = context.Canceled.Error()
				default:
					size, stateErr := validateState(staged)
					if stateErr != nil {
						result.Error = stateErr.Error()
					} else if s.stateBytes-scope.bytes+size > MaxServiceStateBytes {
						result.Error = fmt.Sprintf("PTC session state exceeds the byte limit (%d)", MaxServiceStateBytes)
					} else {
						s.stateBytes += size - scope.bytes
						scope.values, scope.bytes = staged, size
					}
				}
				if result.Error != "" {
					result.Value = nil
				}
			}
			if !s.closed && len(scope.values) == 0 {
				delete(s.scopes, opts.StateScope)
			}
			close(scope.done)
			scope.ctx, scope.done = nil, nil
		}
		s.mu.Unlock()
	}()
	node := s.config.NodeExecutable
	if node == "" {
		node = os.Getenv("WUU_NODE_EXECUTABLE")
	}
	if node == "" {
		node, err = exec.LookPath("node")
		if err != nil {
			return result, errors.New("PTC requires Node.js 22.19+ or the desktop Node runtime")
		}
	}
	outputLimit := opts.MaxOutputBytes
	if outputLimit == 0 {
		outputLimit = defaultOutputBytes
	}
	if outputLimit < 128 || outputLimit > toolresult.MaxStructuredJSONSize {
		return result, errors.New("invalid PTC output limit")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return result, err
	}
	defer listener.Close()
	stopAccept := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopAccept()
	secret := rand.Text()
	modules := make(map[string]string)
	for _, name := range []string{"index.js", "wasi-shim.js", "extensions.js", "version.js"} {
		data, readErr := runtimeAssets.ReadFile("vendor/quickjs/" + name)
		if readErr != nil {
			return result, readErr
		}
		modules[name] = string(data)
	}
	wasm, readErr := loadRuntimeWasm(runtimeAssets)
	if readErr != nil {
		return result, readErr
	}
	boot, err := json.Marshal(map[string]any{"address": listener.Addr().String(), "secret": secret, "code": request.Code, "tools": catalog.Names(), "maxOutputBytes": outputLimit, "modules": modules, "wasm": wasm,
		"state": committed, "stateEnabled": scope != nil, "maxStateBytes": MaxStateBytes, "maxStateKeys": MaxStateKeys})
	if err != nil {
		return result, err
	}
	if len(boot) > maxFrameBytes {
		return result, errors.New("PTC program and tool catalog exceed the transport limit")
	}
	cmd := exec.Command(node, "--max-old-space-size=512", "--input-type=module", "-e", bootstrap)
	cmd.Dir = opts.CWD
	cmd.Env = []string{"ELECTRON_RUN_AS_NODE=1", "NODE_NO_WARNINGS=1"}
	// Windows requires its loader path before the bootstrap clears process.env.
	if value := os.Getenv("SystemRoot"); value != "" {
		cmd.Env = append(cmd.Env, "SystemRoot="+value)
	}
	cmd.Stdin = strings.NewReader(string(boot))
	output := &runOutput{limit: outputLimit, cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 2 * time.Second
	if opts.Sandbox != nil {
		if _, err = processsandbox.ApplyWithProvider(ctx, cmd, *opts.Sandbox, opts.SandboxProvider); err != nil {
			return result, err
		}
	}
	handle, err := proc.StartCommand(cmd)
	if err != nil {
		return result, fmt.Errorf("start Node PTC: %w", err)
	}
	defer func() { _ = handle.Stop(250 * time.Millisecond) }()
	go func() {
		select {
		case <-handle.Done():
			_ = listener.Close()
		case <-ctx.Done():
		}
	}()
	conn, acceptErr := listener.Accept()
	if acceptErr != nil {
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			return result, nil
		}
		return result, fmt.Errorf("Node PTC startup failed: %s", strings.Join(output.snapshot(), ""))
	}
	defer conn.Close()
	_ = listener.Close()
	stopIO := context.AfterFunc(ctx, func() { _ = conn.Close(); _ = handle.Stop(250 * time.Millisecond) })
	defer stopIO()
	var hello struct {
		Secret string `json:"secret"`
	}
	if err = readFrame(conn, &hello); err != nil || hello.Secret != secret {
		return result, errors.New("Node PTC control authentication failed")
	}
	allowed := make(map[string]struct{}, len(request.Tools))
	for _, tool := range request.Tools {
		allowed[tool.Name] = struct{}{}
	}
	var writes sync.Mutex
	var calls sync.WaitGroup
	var mediaMu sync.Mutex
	var media []toolresult.ContentPart
	slots := make(chan struct{}, maxPendingCalls)
	send := func(value any) {
		frame, encodeErr := encodeFrame(value)
		if encodeErr != nil {
			cancel()
			return
		}
		writes.Lock()
		writeErr := writeFrame(conn, frame)
		writes.Unlock()
		if writeErr != nil {
			cancel()
		}
	}
	defer func() {
		cancel()
		calls.Wait()
		_ = handle.Stop(250 * time.Millisecond)
		result.Logs = output.snapshot()
		result.Media = media
		// Once tools may have run, preserve recovery diagnostics on transport failures too.
		if err != nil {
			result.Error, err = err.Error(), nil
		}
		if output.overflowed() {
			result.Error = "PTC output exceeded the byte limit"
		}
	}()
	nextID := 1
	for {
		var frame struct {
			Type  string            `json:"type"`
			ID    int               `json:"id"`
			Name  string            `json:"name"`
			Args  json.RawMessage   `json:"args"`
			Value json.RawMessage   `json:"value"`
			Error string            `json:"error"`
			Text  string            `json:"text"`
			State map[string]string `json:"state"`
		}
		if readErr := readFrame(conn, &frame); readErr != nil {
			if ctx.Err() != nil {
				result.Error = ctx.Err().Error()
				return result, nil
			}
			return result, fmt.Errorf("Node PTC control channel: %w", readErr)
		}
		if frame.Type == "call" || frame.Type == "search" || frame.Type == "describe" {
			if frame.ID != nextID || len(frame.Args) == 0 || !json.Valid(frame.Args) {
				return result, errors.New("invalid PTC request")
			}
			nextID++
		}
		switch frame.Type {
		case "log":
			_, _ = output.Write([]byte(frame.Text))
		case "done":
			if !output.admitValue(frame.Value, frame.Error) {
				result.Error = "PTC output exceeded the byte limit"
			} else {
				result.Value = frame.Value
				result.Error = frame.Error
			}
			if result.Error == "" && ctx.Err() != nil {
				result.Error = ctx.Err().Error()
			}
			completed, staged = true, frame.State
			return result, nil
		case "search", "describe":
			var value any
			var lookupErr error
			if frame.Type == "describe" {
				value, lookupErr = catalog.Describe(frame.Name)
			} else {
				var query struct {
					Query  string `json:"query"`
					Limit  int    `json:"limit"`
					Offset int    `json:"offset"`
				}
				lookupErr = json.Unmarshal(frame.Args, &query)
				if lookupErr == nil {
					value, lookupErr = catalog.Search(ctx, query.Query, query.Limit, query.Offset)
				}
			}
			message := ""
			if lookupErr != nil {
				message = lookupErr.Error()
			}
			send(map[string]any{"id": frame.ID, "value": value, "error": message})
		case "call":
			if _, ok := allowed[frame.Name]; !ok {
				return result, errors.New("PTC requested an unavailable tool")
			}
			if opts.Executor == nil {
				return result, errors.New("PTC nested executor is unavailable")
			}
			select {
			case slots <- struct{}{}:
			default:
				return result, errors.New("PTC pending tool call limit exceeded")
			}
			outcome := history.begin(frame.ID, frame.Name)
			calls.Add(1)
			go func(id int, name string, args json.RawMessage) {
				defer calls.Done()
				defer func() { <-slots }()
				value, callErr := opts.Executor.Invoke(ctx, providers.ToolCall{ID: strconv.Itoa(id), Name: name, Arguments: string(args), Kind: providers.ToolCallKindFunction})
				failed := callErr != nil || value.IsError
				history.finish(outcome, failed, failed && (ctx.Err() != nil || errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded)))
				message := ""
				if callErr != nil {
					message = callErr.Error()
				} else if value.IsError {
					message = value.TextProjection()
					if message == "" {
						message = "tool call failed"
					}
				}
				if message == "" {
					mediaMu.Lock()
					for _, part := range value.Content {
						if part.Type == toolresult.ContentTypeImage || part.Type == toolresult.ContentTypeAudio {
							if !output.admitMedia(part) {
								message = "PTC media output limit exceeded; use fewer or smaller images/audio"
								break
							}
							media = append(media, part)
						}
					}
					mediaMu.Unlock()
				}
				reply := map[string]any{"id": id, "error": message}
				if callErr == nil {
					reply["value"] = value
				}
				send(reply)
			}(frame.ID, frame.Name, frame.Args)
		default:
			return result, errors.New("unknown PTC control message")
		}
	}
}

func validateState(values map[string]string) (int, error) {
	if values == nil {
		return 0, errors.New("PTC successful program omitted state")
	}
	if len(values) > MaxStateKeys {
		return 0, fmt.Errorf("PTC state key limit exceeded (%d)", MaxStateKeys)
	}
	size := 0
	for key, value := range values {
		size += len(key) + len(value)
		if size > MaxStateBytes {
			return 0, fmt.Errorf("PTC state exceeds the byte limit (%d)", MaxStateBytes)
		}
		var decodedKey string
		if len(key) == 0 || key[0] != '"' || json.Unmarshal([]byte(key), &decodedKey) != nil || !json.Valid([]byte(value)) {
			return 0, errors.New("PTC state contains invalid JSON")
		}
	}
	return size, nil
}

// runOutput also bounds raw native stdout/stderr, which never share the control channel.
type runOutput struct {
	mu                     sync.Mutex
	logs                   []string
	bytes, limit           int
	mediaBytes, mediaParts int
	over                   bool
	cancel                 context.CancelFunc
}

func (o *runOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	encoded, _ := json.Marshal(string(p))
	if o.bytes+len(encoded)+1 > o.limit || o.bytes+len(encoded)+1+o.mediaBytes+8192+maxCallSummaryBytes > toolresult.MaxResultBytes {
		o.over = true
		o.cancel()
		return len(p), nil
	}
	o.bytes += len(encoded) + 1
	o.logs = append(o.logs, string(p))
	return len(p), nil
}
func (o *runOutput) admitValue(p []byte, message string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	encodedError, _ := json.Marshal(message)
	size := len(p) + len(encodedError) + 128
	if o.bytes+size > o.limit || o.bytes+size+o.mediaBytes+8192+maxCallSummaryBytes > toolresult.MaxResultBytes {
		o.over = true
		return false
	}
	o.bytes += size
	return true
}

// Reserve the final rich-result envelope before retaining intermediate media.
func (o *runOutput) admitMedia(part toolresult.ContentPart) bool {
	encoded, _ := json.Marshal(part)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.mediaParts >= toolresult.MaxContentParts-1 || o.bytes+o.mediaBytes+len(encoded)+8192+maxCallSummaryBytes > toolresult.MaxResultBytes {
		return false
	}
	o.mediaParts++
	o.mediaBytes += len(encoded) + 1
	return true
}
func (o *runOutput) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string{}, o.logs...)
}
func (o *runOutput) overflowed() bool { o.mu.Lock(); defer o.mu.Unlock(); return o.over }
