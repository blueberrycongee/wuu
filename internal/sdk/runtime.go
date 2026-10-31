package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/personalcalendar"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/runtimeconfig"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/userspace"
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

// Options configures one workspace or explicitly domain-bound user runtime. Empty WorkDir and HomeDir values
// use the process working directory and HOME respectively.
type Options struct {
	// CompositionProfile explicitly selects a shipped or configured product profile.
	// Empty preserves the configured profile; identity never selects a product.
	CompositionProfile string
	// PersonalConnectors supplies trusted test/embedding dependencies, never RPC credentials.
	PersonalConnectors                    personalconnectors.Options
	OpenPersonalConnectorAuthorizationURL func(context.Context, string) error
	// PersonalCalendar is trusted local-host composition; zero stays unavailable.
	// The SDK replaces StateDir with its resolved Personal state root.
	PersonalCalendar                     personalcalendar.DesktopOptions
	OpenPersonalCalendarAuthorizationURL func(context.Context, string) error
	// UserHostIsolationMode defaults to logical_memory for a user binding. It
	// separates memory APIs and automatic context by domain, but does not promise
	// OS/file confidentiality. verified_host additionally requires a trusted verifier.
	UserHostIsolationMode runtime.UserHostIsolationMode
	// UserHostIsolationVerifier is a trusted host integration, never a client or environment assertion.
	UserHostIsolationVerifier runtime.UserHostIsolationVerifier
	UserSpaceID               string
	ProductDomain             string
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
	// DesktopExecutionDefaults uses unlimited steps and automatic temperature
	// for desktop conversations, ignoring persisted overrides without rewriting them.
	DesktopExecutionDefaults bool
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

// Runtime is an initialized, UI-neutral Wuu workspace or domain-bound user host.
//
// A Runtime may serve app-server connections sequentially or concurrently.
// Close must be called after all Serve calls return; it rejects an early close
// instead of racing shared provider, plugin, and persistence resources.
type Runtime struct {
	mu       sync.Mutex
	core     *runtime.Session
	userHost *appserver.UserHost
	bindings *appserver.RuntimeBindings
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
	binding := userspace.Binding{UserSpaceID: strings.TrimSpace(opts.UserSpaceID), ProductDomain: strings.TrimSpace(opts.ProductDomain)}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if binding.IsUser() {
		if opts.WorkspaceID != "" || opts.IgnoreUserConfig {
			return nil, errors.New("user host cannot inherit a project binding")
		}
		home, err := statepath.Home(homeDir)
		if err != nil {
			return nil, err
		}
		rootDir = binding.Root(home)
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

	var cfg config.Config
	var configPath string
	var configLoadMode runtime.ConfigLoadMode
	if binding.IsUser() {
		configPath = strings.TrimSpace(opts.ConfigPath)
		if configPath == "" {
			configPath, err = statepath.ConfigPath(homeDir)
			if err != nil {
				return nil, err
			}
		}
		if !filepath.IsAbs(configPath) {
			return nil, errors.New("user host requires an absolute config path")
		}
		cfg, configPath, err = config.LoadPath(configPath)
		configLoadMode = runtime.ConfigLoadFile
	} else {
		cfg, configPath, configLoadMode, err = runtimeconfig.Load(runtimeconfig.LoadOptions{RootDir: rootDir, HomeDir: homeDir, ConfigPath: opts.ConfigPath, IgnoreUserConfig: opts.IgnoreUserConfig, CreateConfigIfMissing: opts.CreateConfigIfMissing})
	}
	if err != nil {
		return nil, err
	}
	overrides := runtimeconfig.Overrides{
		CompositionProfile: opts.CompositionProfile,
		AgentProfile:       opts.AgentProfile,
		MaxTurns:           opts.MaxTurns,
		Effort:             opts.Effort,
		Variant:            opts.Variant,
		PermissionMode:     opts.PermissionMode,
	}
	if err := runtimeconfig.ApplyOverrides(&cfg, overrides); err != nil {
		return nil, err
	}

	bindings := appserver.NewRuntimeBindings()
	bindings.SetPersonalAccountsFactory(func(input runtime.NativeProviderContext) (personalhost.Options, error) {
		personalOptions := personalhost.Options{}
		if input.UserBinding.Domain() != "personal" || host.Kind != runtime.HostLocal || opts.SafeMode {
			personalOptions.ConnectorsUnavailableCode = "local_personal_host_required"
			return personalOptions, nil
		}
		credentials := credentialstore.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := input.Registry.Call(ctx, appserver.PersonalAccountsModule, pluginhost.ServiceCallParams{Service: credentialstore.ServiceName, Method: method, Params: raw})
			if err != nil {
				return nil, err
			}
			return result, nil
		})
		stateHome, pathErr := statepath.Home(homeDir)
		if pathErr != nil {
			return personalOptions, pathErr
		}
		connectorOptions := opts.PersonalConnectors
		connectorOptions.StateDir = filepath.Join(input.UserBinding.State(stateHome), "connectors")
		if connectorOptions.Store == nil {
			connectorOptions.Store = credentials
		}
		personalOptions.Connectors = personalconnectors.New(connectorOptions)
		personalOptions.OpenConnectorAuthorizationURL = opts.OpenPersonalConnectorAuthorizationURL
		if opts.PersonalCalendar.ClientID != "" {
			calendarOptions := opts.PersonalCalendar
			calendarOptions.StateDir = filepath.Join(input.UserBinding.State(stateHome), "calendar")
			if calendarOptions.Store == nil {
				calendarOptions.Store = credentials
			}
			broker, calendarErr := personalcalendar.NewDesktop(calendarOptions)
			if calendarErr != nil {
				personalOptions.UnavailableCode = "secure_store_unavailable"
				var failure *personalcalendar.Failure
				if errors.As(calendarErr, &failure) {
					personalOptions.UnavailableCode = failure.Code
				}
			} else {
				personalOptions.Calendar = broker
				personalOptions.OpenAuthorizationURL = opts.OpenPersonalCalendarAuthorizationURL
			}
		}
		return personalOptions, nil
	})
	core, err := runtime.NewSession(runtime.Options{
		UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain, UserHostIsolationVerifier: opts.UserHostIsolationVerifier, UserHostIsolationMode: opts.UserHostIsolationMode,
		NativeProviders:          bindings.NativeProviders(),
		RootDir:                  rootDir,
		Host:                     host,
		WorkspaceID:              strings.TrimSpace(opts.WorkspaceID),
		HomeDir:                  homeDir,
		ConfigPath:               configPath,
		ConfigLoadMode:           configLoadMode,
		DesktopExecutionDefaults: opts.DesktopExecutionDefaults,
		Config:                   cfg,
		ProviderName:             strings.TrimSpace(opts.Provider),
		ModelOverride:            strings.TrimSpace(opts.Model),
		Overrides:                overrides,
		NoTools:                  opts.NoTools,
		NonInteractive:           opts.NonInteractive,
		SafeMode:                 opts.SafeMode,
	})
	if err != nil {
		return nil, err
	}
	result := &Runtime{core: core, bindings: bindings}
	if binding.IsUser() {
		result.userHost, err = appserver.NewUserHost(core, personalhost.Options{}, appserver.WithRuntimeBindings(bindings))
		if err != nil {
			_, _ = core.Cleanup()
			return nil, err
		}
	}
	return result, nil
}

// Serve runs one app-server JSONL connection until the client requests
// shutdown, the input closes, or the protocol returns an error. The caller owns
// in and out.
// Serve is the trusted local-pipe embedding entrypoint. Network transports
// must use ServeProviderAuthenticated or carry a verified device principal.
func (r *Runtime) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	trustedLocal := false
	if ctx != nil {
		if _, ok := principal.FromContext(ctx); !ok {
			trustedLocal = true
			ctx = principal.WithContext(ctx, principal.LocalOwner())
		}
	}
	return r.serve(ctx, in, out, nil, nil, trustedLocal)
}

// ServeForDevice accepts an already-authenticated device principal and its
// bound push registrar; it never upgrades a transport to local owner authority.
func (r *Runtime) ServeForDevice(ctx context.Context, in io.Reader, out io.Writer, registrar appserver.PushRegistrar) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	caller, ok := principal.FromContext(ctx)
	if !ok {
		return errors.New("authenticated device principal is required")
	}
	if err := caller.Check(ctx); err != nil {
		return err
	}
	return r.serve(ctx, in, out, nil, nil, false, registrar)
}

func (r *Runtime) serve(ctx context.Context, in io.Reader, out io.Writer, credential *principal.Credential, authenticationID json.RawMessage, trustedLocal bool, registrars ...appserver.PushRegistrar) error {
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

	if credential != nil {
		identity, err := core.AuthenticateTransport(ctx, *credential)
		if err != nil {
			_ = json.NewEncoder(out).Encode(map[string]any{"id": authenticationID, "error": map[string]string{"code": "authentication_failed", "message": "identity provider rejected the credential"}})
			return errors.New("authentication failed")
		}
		ctx = principal.WithContext(ctx, identity)
		if err := json.NewEncoder(out).Encode(map[string]any{"id": authenticationID, "result": principal.Authentication{Identity: identity.Identity(), ExpiresAt: identity.ExpiresAt()}}); err != nil {
			return err
		}
	}
	var registrar appserver.PushRegistrar
	if len(registrars) > 0 {
		registrar = registrars[0]
	}
	if r.userHost != nil {
		return r.userHost.Serve(ctx, in, out, registrar)
	}
	options := []appserver.ServerOption{appserver.WithRuntimeBindings(r.bindings)}
	if trustedLocal {
		// Forwarding belongs to this private ingress, not to a caller's owner
		// identity. Authenticated peers and device transports cannot opt into it.
		options = append(options, appserver.WithTrustedLocalTransport())
	}
	if len(registrars) > 0 {
		return appserver.RunStdioForDevice(ctx, core, in, out, registrar, options...)
	}
	return appserver.RunStdio(ctx, core, in, out, options...)
}

// SubscribeUserAttention observes owner-admitted notifications for the user
// host without a phone or desktop connection. The receiver must honor ctx for
// external work. Close ends the subscription; Coding runtimes reject it.
func (r *Runtime) SubscribeUserAttention(ctx context.Context, receive func(appserver.UserAttentionNotification)) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if r == nil {
		return errors.New("runtime is required")
	}
	r.mu.Lock()
	host := r.userHost
	closed := r.closed
	r.mu.Unlock()
	if closed || host == nil {
		return errors.New("active user host is required")
	}
	if _, ok := principal.FromContext(ctx); !ok {
		ctx = principal.WithContext(ctx, principal.LocalOwner())
	}
	return host.SubscribeUserAttention(ctx, receive)
}

// AuthorizeUserAttention verifies one recipient against the selected resource
// policy and the original host-minted event provenance. Transport adapters must
// pass a verified recipient principal; this method never grants local ownership.
func (r *Runtime) AuthorizeUserAttention(ctx context.Context, event appserver.UserAttentionNotification) error {
	if r == nil || ctx == nil {
		return errors.New("runtime and context are required")
	}
	r.mu.Lock()
	host, closed := r.userHost, r.closed
	r.mu.Unlock()
	if closed || host == nil {
		return errors.New("active user host is required")
	}
	return host.AuthorizeUserAttention(ctx, event)
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
	if r.userHost != nil {
		r.closeErr = r.userHost.Close()
		r.userHost = nil
	}

	if r.core != nil {
		_, coreErr := r.core.Cleanup()
		r.closeErr = errors.Join(r.closeErr, coreErr)
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
