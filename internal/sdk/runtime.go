package sdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/runtimeconfig"
)

// ProtocolVersion is the app-server contract implemented by Runtime.Serve.
const ProtocolVersion = "wuu-app-server/v0.1"

// HostKind identifies where the embedded core process is running.
type HostKind string

const (
	HostLocal HostKind = "local"
	HostCloud HostKind = "cloud"
)

// Host describes immutable process identity supplied by the embedding shell or
// managed control plane.
type Host struct {
	Kind       HostKind
	InstanceID string
}

// Options configures one workspace runtime. Empty WorkDir and HomeDir values
// use the process working directory and HOME respectively.
type Options struct {
	// WorkDir is the workspace root used for tools and project discovery.
	WorkDir string
	// WorkspaceID is a stable identity that keeps workspace state attached when
	// WorkDir moves. Empty uses path-anchored state.
	WorkspaceID string
	// HomeDir controls user-level config discovery. WUU_HOME still controls the
	// Wuu state directory when present.
	HomeDir string
	// ConfigPath loads one explicit config file, relative to WorkDir when needed.
	ConfigPath string

	// IgnoreUserConfig loads only the trusted project config. ConfigPath takes
	// precedence when both values are supplied.
	IgnoreUserConfig bool
	// CreateConfigIfMissing enables the local first-run setup behavior. Managed
	// hosts must always provide ConfigPath and cannot enable this fallback.
	CreateConfigIfMissing bool

	// Host identifies the process environment. The zero value is a local host.
	Host Host
	// Provider and Model override the configured default for this process.
	Provider string
	Model    string

	// AgentProfile selects a configured agent profile for this process.
	AgentProfile string
	// MaxTurns overrides the agent step limit. Zero keeps the configured value.
	MaxTurns int
	// Effort and Variant override model reasoning controls.
	Effort  string
	Variant string
	// PermissionMode is a process-scoped override and is not persisted.
	PermissionMode string

	// NoTools disables local tool execution.
	NoTools bool
	// NonInteractive rejects human question services and declines interactive
	// engine approvals. It applies to every connection sharing this runtime.
	NonInteractive bool
	// SafeMode discovers plugin manifests for management but activates no plugin
	// contributions.
	SafeMode bool
}

// Runtime is an initialized, UI-neutral Wuu host for one workspace.
//
// A Runtime may serve app-server connections sequentially or concurrently.
// Close must be called after all Serve calls return; it rejects an early close
// instead of racing shared provider, plugin, and persistence resources.
type Runtime struct {
	mu       sync.Mutex
	core     *runtime.Session
	serving  int
	closed   bool
	closeErr error
}

// New builds the default Wuu host using the same composition path as the
// command-line and desktop shells.
func New(opts Options) (*Runtime, error) {
	if ProtocolVersion != appserver.ProtocolVersion {
		return nil, fmt.Errorf("SDK protocol %q does not match core protocol %q", ProtocolVersion, appserver.ProtocolVersion)
	}
	rootDir, err := resolveWorkDir(opts.WorkDir)
	if err != nil {
		return nil, err
	}
	homeDir := strings.TrimSpace(opts.HomeDir)
	if homeDir == "" {
		homeDir = os.Getenv("HOME")
		if strings.TrimSpace(homeDir) == "" {
			homeDir, err = os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve home directory: %w", err)
			}
		}
	}
	host, err := runtimeconfig.ResolveHost(
		string(opts.Host.Kind),
		opts.Host.InstanceID,
		opts.WorkspaceID,
		opts.ConfigPath,
	)
	if err != nil {
		return nil, err
	}
	if host.Kind == runtime.HostCloud && opts.CreateConfigIfMissing {
		return nil, errors.New("cloud host cannot create a starter config")
	}

	cfg, configPath, configLoadMode, err := runtimeconfig.Load(runtimeconfig.LoadOptions{
		RootDir:               rootDir,
		HomeDir:               homeDir,
		ConfigPath:            opts.ConfigPath,
		IgnoreUserConfig:      opts.IgnoreUserConfig,
		CreateConfigIfMissing: opts.CreateConfigIfMissing,
	})
	if err != nil {
		return nil, err
	}
	if err := runtimeconfig.ApplyOverrides(&cfg, runtimeconfig.Overrides{
		AgentProfile:   opts.AgentProfile,
		MaxTurns:       opts.MaxTurns,
		Effort:         opts.Effort,
		Variant:        opts.Variant,
		PermissionMode: opts.PermissionMode,
	}); err != nil {
		return nil, err
	}

	core, err := runtime.NewSession(runtime.Options{
		RootDir:                rootDir,
		Host:                   host,
		WorkspaceID:            strings.TrimSpace(opts.WorkspaceID),
		HomeDir:                homeDir,
		ConfigPath:             configPath,
		ConfigLoadMode:         configLoadMode,
		Config:                 cfg,
		ProviderName:           strings.TrimSpace(opts.Provider),
		ModelOverride:          strings.TrimSpace(opts.Model),
		PermissionModeExplicit: strings.TrimSpace(opts.PermissionMode) != "",
		NoTools:                opts.NoTools,
		NonInteractive:         opts.NonInteractive,
		SafeMode:               opts.SafeMode,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{core: core}, nil
}

// Serve runs one app-server JSONL connection until the client requests
// shutdown, the input closes, or the protocol returns an error. The caller owns
// in and out.
func (r *Runtime) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if r == nil {
		return errors.New("runtime is required")
	}
	if ctx == nil {
		return errors.New("context is required")
	}
	if in == nil {
		return errors.New("app-server input is required")
	}
	if out == nil {
		return errors.New("app-server output is required")
	}

	r.mu.Lock()
	if r.closed || r.core == nil {
		r.mu.Unlock()
		return errors.New("runtime is closed")
	}
	core := r.core
	r.serving++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.serving--
		r.mu.Unlock()
	}()

	return appserver.RunStdio(ctx, core, in, out)
}

// Close releases resources owned by the embedded runtime. It is idempotent.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.closeErr
	}
	if r.serving != 0 {
		return fmt.Errorf("close runtime: %d app-server connection(s) still serving", r.serving)
	}
	r.closed = true
	if r.core != nil {
		_, r.closeErr = r.core.Cleanup()
		r.core = nil
	}
	return r.closeErr
}

func resolveWorkDir(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		return cwd, nil
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}
	return abs, nil
}
