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

const DefaultTimeoutMS = 120000
const MaxTimeoutMS = 600000
const defaultOutputBytes = 1024 * 1024
const maxPendingCalls = 128

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ServiceConfig struct{ NodeExecutable string }

type RunRequest struct {
	Code      string           `json:"code"`
	Tools     []ToolDefinition `json:"tools"`
	TimeoutMS int              `json:"-"`
}
type RunOptions struct {
	CWD             string
	Executor        toolctx.NestedExecutor
	Sandbox         *processsandbox.Policy
	SandboxProvider processsandbox.Provider
	MaxOutputBytes  int
}
type RunResult struct {
	Logs  []string
	Value json.RawMessage
	Error string
	Media []toolresult.ContentPart
}

// Service owns running programs, not a persistent JavaScript kernel. Every Run
// creates a fresh Node process; failed or interrupted programs are never replayed.
type Service struct {
	config ServiceConfig
	mu     sync.Mutex
	closed bool
	active map[string]context.CancelFunc
	done   sync.WaitGroup
}

func NewService(config ServiceConfig) *Service {
	return &Service{config: config, active: map[string]context.CancelFunc{}}
}

func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.active {
		cancel()
	}
	s.mu.Unlock()
	s.done.Wait()
	return nil
}

// Run retains only printed/returned values and media; nested calls go through
// the caller's policy, scheduler and durable ledger. The interpreter has no native
// capabilities. The deadline includes tool and approval waits.
func (s *Service) Run(parent context.Context, request RunRequest, opts RunOptions) (result RunResult, err error) {
	if strings.TrimSpace(request.Code) == "" {
		return result, errors.New("PTC requires code")
	}
	catalog, catalogErr := newToolCatalog(request.Tools)
	if catalogErr != nil {
		return result, catalogErr
	}
	timeout := request.TimeoutMS
	if timeout == 0 {
		timeout = DefaultTimeoutMS
	}
	if timeout < 1 || timeout > MaxTimeoutMS {
		return result, fmt.Errorf("PTC timeout_ms must be between 1 and %d", MaxTimeoutMS)
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	id := rand.Text()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return result, errors.New("PTC runtime is closed")
	}
	s.active[id] = cancel
	s.done.Add(1)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, id); s.mu.Unlock(); s.done.Done() }()
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
	boot, err := json.Marshal(map[string]any{"address": listener.Addr().String(), "secret": secret, "code": request.Code, "tools": catalog.Names(), "maxOutputBytes": outputLimit, "modules": modules, "wasm": wasm})
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
		if output.overflowed() {
			result.Error = "PTC output exceeded the byte limit"
		}
	}()
	nextID := 1
	for {
		var frame struct {
			Type  string          `json:"type"`
			ID    int             `json:"id"`
			Name  string          `json:"name"`
			Args  json.RawMessage `json:"args"`
			Value json.RawMessage `json:"value"`
			Error string          `json:"error"`
			Text  string          `json:"text"`
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
			calls.Add(1)
			go func(id int, name string, args json.RawMessage) {
				defer calls.Done()
				defer func() { <-slots }()
				value, callErr := opts.Executor.Invoke(ctx, providers.ToolCall{ID: strconv.Itoa(id), Name: name, Arguments: string(args), Kind: providers.ToolCallKindFunction})
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
				send(map[string]any{"id": id, "value": value, "error": message})
			}(frame.ID, frame.Name, frame.Args)
		default:
			return result, errors.New("unknown PTC control message")
		}
	}
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
	if o.bytes+len(encoded)+1 > o.limit || o.bytes+len(encoded)+1+o.mediaBytes+8192 > toolresult.MaxResultBytes {
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
	if o.bytes+size > o.limit || o.bytes+size+o.mediaBytes+8192 > toolresult.MaxResultBytes {
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
	if o.mediaParts >= toolresult.MaxContentParts-1 || o.bytes+o.mediaBytes+len(encoded)+8192 > toolresult.MaxResultBytes {
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
