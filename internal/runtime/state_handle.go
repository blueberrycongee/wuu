package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// State captures one selected authority for a request or durable execution.
// Close releases its generation after all consumers and leases finish cleanup.
// Documents and Sessions never resolve a later published generation.
type State struct {
	Documents               persistence.Documents
	Sessions                *session.Store
	Generation              *PluginGeneration
	Host                    *pluginhost.Host
	InferenceJournalRuntime *session.InferenceJournalRuntime
	mu                      sync.Mutex
	closed                  bool
	once                    sync.Once
	close                   func() error
	closeErr                error
}

func (s *State) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if s.close != nil {
			s.closeErr = s.close()
		}
	})
	return s.closeErr
}

func (s *Session) RetainState() (*State, error) {
	if s == nil {
		return nil, errors.New("runtime is not initialized")
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	generation := s.pluginGeneration
	if generation == nil && s.PluginHost != nil {
		generation = s.capturePluginGeneration()
		s.pluginGeneration = generation
	}
	documents, sessions := s.Documents, s.Sessions
	if generation != nil && generation.state != nil {
		documents, sessions = generation.state.documents, generation.state.sessions
	}
	if documents == nil || sessions == nil {
		return nil, errors.New("selected composition has no session state")
	}
	result := &State{Documents: documents, Sessions: sessions, Host: s.PluginHost, Generation: generation, InferenceJournalRuntime: s.InferenceJournalRuntime}
	if generation != nil {
		generation.retain()
		result.Host = generation.host
		if generation.state != nil {
			result.InferenceJournalRuntime = generation.state.journal
		}
		result.close = func() error { return s.releasePluginGeneration(generation) }
	}
	return result, nil
}

// StateOptions uses the same provider discovery, trust and composition as the
// interactive runtime without requiring a model or inference credentials.
type StateOptions struct {
	RootDir, HomeDir, ConfigPath string
	Config                       config.Config
	ConfigLoadMode               ConfigLoadMode
	NativeProviders              []NativeProviderFactory
}

func OpenState(ctx context.Context, opts StateOptions) (*State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var err error
	opts.NativeProviders, err = withCredentialProviders(opts.NativeProviders, opts.HomeDir)
	if err != nil {
		return nil, err
	}
	opts.NativeProviders = withConfigurationProvider(opts.NativeProviders, opts.ConfigPath, opts.RootDir, opts.Config, opts.ConfigLoadMode)
	root := strings.TrimSpace(opts.RootDir)
	if root == "" {
		return nil, errors.New("root dir is required")
	}
	home, err := statepath.Home(opts.HomeDir)
	if err != nil {
		return nil, err
	}
	lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(home)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, errors.New("plugin packages are being changed by another app-server")
	}
	defer lease.Release()
	catalog, acquired, err := session.TryAcquirePluginCatalogReadLease(home)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, errors.New("plugin catalog is being published by another process")
	}
	defer catalog.Release()
	plan, err := ResolvePluginActivationPlan(opts.Config, discoverPlugins(root, home), opts.NativeProviders...)
	if err != nil {
		return nil, err
	}
	plugins, roots, err := snapshotMutableExecutionPackages(plan.Plugins)
	if err != nil {
		return nil, err
	}
	owned := true
	defer func() {
		if owned {
			for _, root := range roots {
				_ = os.RemoveAll(root)
			}
		}
	}()
	if err := catalog.Release(); err != nil {
		return nil, err
	}
	workspace, err := resolveWorkspaceStateDir(home, "", root)
	if err != nil {
		return nil, err
	}
	host, kernel, err := buildPluginHost(plugins, root, "", home, workspace, nil, startPluginClient, NewPluginSessionRouter(), nil, opts.Config.Composition, "", opts.NativeProviders...)
	if err != nil {
		return nil, err
	}
	generation := &PluginGeneration{id: newPluginGenerationID(home), host: host, state: kernel.state, ownedRoots: roots, settings: opts.Config, plugins: plugins}
	if services := kernel.selectedServices; services != nil {
		generation.credentials, generation.configClient = services.credentials, services.config
		generation.chatGPTManager, generation.artifactClient, generation.mcpOAuth = services.chatGPT, services.artifacts, services.oauth
	}
	generation.retain()
	kernel.bindStateGeneration(generation, nil)
	owned = false
	graphOwned := true
	defer func() {
		if graphOwned {
			generation.Release()
		}
	}()
	if err := host.Activate(ctx); err != nil {
		return nil, fmt.Errorf("activate state composition: %w", err)
	}
	if kernel.state == nil || kernel.state.Status().State != pluginhost.StateActive {
		return nil, errors.New("selected composition has no active session state")
	}
	generation.revokedMu.Lock()
	generation.active = availablePluginPackages(plugins, host)
	generation.revokedMu.Unlock()
	graphOwned = false
	return &State{Documents: kernel.state.documents, Sessions: kernel.state.sessions, Host: host, Generation: generation, InferenceJournalRuntime: kernel.state.journal, close: func() error {
		if generation.Release() {
			return generation.closeErr
		}
		return nil
	}}, nil
}

// Retain transfers a live capture without observing the publishing Session.
// A closed handle cannot be used to resurrect an already released generation.
func (s *State) Retain() *State {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.Generation.retain()
	return &State{Documents: s.Documents, Sessions: s.Sessions, Generation: s.Generation, Host: s.Host, InferenceJournalRuntime: s.InferenceJournalRuntime, close: s.close}
}

var ErrStateGenerationChanged = errors.New("state generation changed before runtime admission")

// NewThreadRuntimeForState either constructs against the admitted generation or
// rejects before effects so its caller can retry admission with a new capture.
func (s *Session) NewThreadRuntimeForState(binding *State, sessionID, rootDir string, selected ThreadModelSelection) (*ThreadRuntime, error) {
	shadow, err := s.cloneForThreadModel(binding)
	if err != nil {
		return nil, err
	}
	defer s.releasePluginGeneration(shadow.pluginGeneration)
	return shadow.NewThreadRuntimeForRootModel(sessionID, rootDir, selected)
}

func (s *Session) NewThreadCapabilityRuntimeForState(binding *State, sessionID, rootDir string, selected ThreadModelSelection) (*ThreadRuntime, error) {
	shadow, err := s.cloneForThreadModel(binding)
	if err != nil {
		return nil, err
	}
	defer s.releasePluginGeneration(shadow.pluginGeneration)
	return shadow.NewThreadCapabilityRuntime(sessionID, rootDir, selected)
}

func (s *Session) NewSideThreadRunnerForState(binding *State, sessionID, rootDir string, selected ThreadModelSelection, prompt string, parentSession ...string) (*agent.StreamRunner, error) {
	shadow, err := s.cloneForThreadModel(binding)
	if err != nil {
		return nil, err
	}
	defer s.releasePluginGeneration(shadow.pluginGeneration)
	return shadow.newSideThreadRunner(sessionID, rootDir, selected, prompt, parentSession...)
}
