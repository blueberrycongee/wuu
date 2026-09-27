package codexengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/enginecatalog"
)

// Host manages one shared codex app-server process. Multiple wuu threads
// multiplex over the same app-server via their own codex thread ids (the
// reference architecture: one host per app-server, N threads per host).
type Host struct {
	binaryPath string
	env        []string
	extraArgs  []string
	rootDir    string

	mu       sync.Mutex
	client   *Client
	refs     int
	startErr error
}

// ResolveBinary locates the codex executable. The WUU_CODEX_BINARY
// environment variable wins. Otherwise lookup uses PATH and the standard
// install locations, because a Finder or Dock launch does not receive the
// terminal PATH.
func ResolveBinary() (string, error) {
	if path := envCodexBinary(); path != "" {
		return path, nil
	}
	path, err := enginecatalog.LookBinary("codex")
	if err != nil {
		return "", errors.New("codex binary not found: set WUU_CODEX_BINARY or install the codex CLI on PATH")
	}
	return path, nil
}

func envCodexBinary() string {
	return strings.TrimSpace(os.Getenv("WUU_CODEX_BINARY"))
}

// NewHost builds a host around an absolute codex binary path.
func NewHost(binaryPath, rootDir string) *Host {
	return &Host{binaryPath: binaryPath, rootDir: rootDir}
}

// SetExtraArgs appends arguments after the app-server subcommand (for
// example -c config overrides).
func (h *Host) SetExtraArgs(args ...string) {
	h.extraArgs = append([]string(nil), args...)
}

// Acquire returns the shared app-server client, starting and initializing
// the process on first use. Concurrent callers share one startup.
func (h *Host) Acquire(ctx context.Context) (*Client, error) {
	if h == nil {
		return nil, errors.New("codex host is nil")
	}
	h.mu.Lock()
	if h.client != nil {
		h.refs++
		client := h.client
		h.mu.Unlock()
		return client, nil
	}
	if h.startErr != nil {
		err := h.startErr
		h.mu.Unlock()
		return nil, err
	}
	h.mu.Unlock()

	client, err := h.start(ctx)
	h.mu.Lock()
	if err != nil {
		h.startErr = err
		h.mu.Unlock()
		return nil, err
	}
	h.client = client
	h.refs++
	h.mu.Unlock()
	return client, nil
}

// ListModels reads the model catalog owned by the installed Codex app-server.
func (h *Host) ListModels(ctx context.Context) ([]ModelListItem, error) {
	client, err := h.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer h.Release()
	models := make([]ModelListItem, 0)
	cursor := ""
	seen := make(map[string]struct{})
	for {
		var page ModelListResponse
		if err := client.Request(ctx, MethodModelList, ModelListParams{
			Cursor: cursor,
			Limit:  100,
		}, &page); err != nil {
			return nil, fmt.Errorf("codex model/list: %w", err)
		}
		for _, model := range page.Data {
			if !model.Hidden {
				models = append(models, model)
			}
		}
		next := strings.TrimSpace(page.NextCursor)
		if next == "" {
			return models, nil
		}
		if _, ok := seen[next]; ok {
			return nil, fmt.Errorf("codex model/list repeated cursor %q", next)
		}
		seen[next] = struct{}{}
		cursor = next
	}
}

// Release drops one session's reference. The app-server is shut down when
// the last reference is released.
func (h *Host) Release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.refs > 0 {
		h.refs--
	}
	if h.refs == 0 && h.client != nil {
		client := h.client
		h.client = nil
		h.startErr = nil
		go func() {
			_ = client.Close()
		}()
	}
}

// start spawns the app-server and runs the initialize handshake. The binary
// is resolved lazily so a missing codex CLI produces a clear error at first
// use rather than at session construction.
func (h *Host) start(ctx context.Context) (*Client, error) {
	binaryPath := strings.TrimSpace(h.binaryPath)
	if binaryPath == "" {
		resolved, err := ResolveBinary()
		if err != nil {
			return nil, err
		}
		binaryPath = resolved
	}
	transport, err := NewTransport(TransportOptions{
		BinaryPath: binaryPath,
		CWD:        h.rootDir,
		Env:        h.env,
		ExtraArgs:  h.extraArgs,
	})
	if err != nil {
		return nil, err
	}
	client := NewClient(transport)
	client.Start()

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp InitializeResponse
	if err := client.Request(initCtx, MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{
			Name:    "wuu",
			Title:   "Wuu Desktop",
			Version: "1",
		},
		Capabilities: &InitializeCapabili{ExperimentalAPI: true},
	}, &resp); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("codex app-server initialize: %w", err)
	}
	return client, nil
}
