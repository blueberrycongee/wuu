package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

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
	Documents  persistence.Documents
	Sessions   *session.Store
	Generation *PluginGeneration
	Host       *pluginhost.Host
	once       sync.Once
	close      func() error
	closeErr   error
}

func (s *State) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
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
	result := &State{Documents: documents, Sessions: sessions, Host: s.PluginHost, Generation: generation}
	if generation != nil {
		generation.retain()
		result.Host = generation.host
		result.close = func() error { s.releasePluginGeneration(generation); return generation.closeErr }
	}
	return result, nil
}

// StateOptions uses the same provider discovery, trust and composition as the
// interactive runtime without requiring a model or inference credentials.
type StateOptions struct {
	RootDir, HomeDir, ConfigPath string
	Config                       config.Config
	NativeProviders              []NativeProviderFactory
}

func OpenState(ctx context.Context, opts StateOptions) (*State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	plan, err := ResolvePluginActivationPlan(opts.Config, discoverPlugins(root, home))
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
	if err := host.Activate(ctx); err != nil {
		_ = host.Close(context.Background())
		return nil, fmt.Errorf("activate state composition: %w", err)
	}
	if kernel.state == nil || kernel.state.Status().State != pluginhost.StateActive {
		_ = host.Close(context.Background())
		return nil, errors.New("selected composition has no active session state")
	}
	generation := &PluginGeneration{host: host, state: kernel.state, ownedRoots: roots}
	generation.retain()
	owned = false
	return &State{Documents: kernel.state.documents, Sessions: kernel.state.sessions, Host: host, Generation: generation, close: func() error { generation.Release(); return generation.closeErr }}, nil
}
