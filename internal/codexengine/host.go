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
	current  *hostGeneration
	owners   int
	starting *hostStartup
}

// References belong to a particular subprocess, never to whichever process
// happens to be current when a session closes.
type hostGeneration struct {
	client *Client
	refs   int
}

type hostStartup struct {
	done       chan struct{}
	cancel     context.CancelFunc
	waiters    int
	finished   bool
	generation *hostGeneration
	err        error
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
	h.mu.Lock()
	defer h.mu.Unlock()
	h.extraArgs = append([]string(nil), args...)
}

// Acquire returns the shared app-server client and an idempotent release for
// this acquisition. Concurrent callers share initialization. Canceling a caller
// only cancels startup when no other caller still needs it. Failed or closed
// processes are not reused by later acquisitions.
func (h *Host) Acquire(ctx context.Context) (*Client, func(), error) {
	if h == nil {
		return nil, nil, errors.New("codex host is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	if generation := h.current; generation != nil {
		generation.client.mu.Lock()
		closed := generation.client.closed
		generation.client.mu.Unlock()
		if !closed {
			generation.refs++
			h.mu.Unlock()
			return generation.client, h.release(generation), nil
		}
		h.current = nil
		if generation.refs == 0 {
			go func() { _ = generation.client.Close() }()
		}
	}
	startup := h.starting
	if startup == nil {
		// The handshake has its own timeout; the first waiter's deadline must
		// not terminate the shared process while another waiter still needs it.
		startCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		startup = &hostStartup{done: make(chan struct{}), cancel: cancel}
		h.starting = startup
		go h.initialize(startCtx, startup)
	}
	startup.waiters++
	h.mu.Unlock()

	select {
	case <-startup.done:
	case <-ctx.Done():
	}
	h.mu.Lock()
	if !startup.finished {
		startup.waiters--
		last := startup.waiters == 0
		if last {
			// A later caller can start afresh without joining abandoned work.
			h.starting = nil
			startup.cancel()
		}
		h.mu.Unlock()
		if last {
			// A caller draining its final turn must not finish shutdown while
			// the canceled startup still owns a child. Other waiters cancel
			// independently, and new acquisitions may already start afresh.
			<-startup.done
		}
		return nil, nil, ctx.Err()
	}
	generation, err := startup.generation, startup.err
	h.mu.Unlock()
	if generation == nil {
		return nil, nil, err
	}
	release := h.release(generation)
	if err := ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	return generation.client, release, nil
}

func (h *Host) initialize(ctx context.Context, startup *hostStartup) {
	client, err := h.start(ctx)
	h.mu.Lock()
	startup.finished = true
	startup.err = err
	if h.starting == startup {
		h.starting = nil
		if err == nil {
			// Reserve ownership for every waiting caller before publishing. A
			// fast first caller cannot close the process ahead of its peers.
			startup.generation = &hostGeneration{client: client, refs: startup.waiters}
			h.current = startup.generation
		}
	}
	startup.cancel()
	abandoned := client != nil && startup.generation == nil
	h.mu.Unlock()
	if abandoned {
		_ = client.Close()
	}
	close(startup.done)
}

// Retain keeps a healthy host warm between acquisitions until its returned,
// idempotent release is called. It does not start a process. Long-lived callers
// (such as an app-server) own this lifetime separately from per-turn leases;
// failed generations still retire and later acquisitions may replace them.
func (h *Host) Retain() func() {
	h.mu.Lock()
	h.owners++
	h.mu.Unlock()
	return sync.OnceFunc(func() {
		h.mu.Lock()
		h.owners--
		var client *Client
		if h.current != nil {
			client = h.detachUnusedLocked(h.current)
		}
		h.mu.Unlock()
		if client != nil {
			// Server shutdown has already drained its turn leases. Wait for
			// this final child cleanup instead of leaving a process behind.
			_ = client.Close()
		}
	})
}

// release binds cleanup to one acquisition and one generation. Owners retain
// only the current healthy generation, never an old failed process.
func (h *Host) release(generation *hostGeneration) func() {
	return sync.OnceFunc(func() {
		h.mu.Lock()
		generation.refs--
		client := h.detachUnusedLocked(generation)
		h.mu.Unlock()
		if client != nil {
			// The final lease owns cleanup even after a settings replacement
			// has released the host's warm owner. Turn draining must include
			// this retired child before publishing terminal or closing Server.
			_ = client.Close()
		}
	})
}

func (h *Host) detachUnusedLocked(generation *hostGeneration) *Client {
	if generation.refs != 0 || (h.current == generation && h.owners != 0) {
		return nil
	}
	if h.current == generation {
		h.current = nil
	}
	return generation.client
}

// ListModels reads the model catalog owned by the installed Codex app-server.
func (h *Host) ListModels(ctx context.Context) ([]ModelListItem, error) {
	client, release, err := h.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return listModels(ctx, client)
}

func listModels(ctx context.Context, client *Client) ([]ModelListItem, error) {
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

// start spawns the app-server and runs the initialize handshake. The binary
// is resolved lazily so a missing codex CLI produces a clear error at first
// use rather than at session construction.
func (h *Host) start(ctx context.Context) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	binaryPath := strings.TrimSpace(h.binaryPath)
	extraArgs := append([]string(nil), h.extraArgs...)
	h.mu.Unlock()
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
		ExtraArgs:  extraArgs,
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
