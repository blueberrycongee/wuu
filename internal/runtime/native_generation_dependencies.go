package runtime

import (
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/mcp"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
)

// nativeGenerationDependencies are allocated only if a selected provider
// delegates to native tools. Thread and preflight scopes share them while their
// generation leases keep the dependencies alive.
type nativeGenerationDependencies struct {
	mu                  sync.Mutex
	initialized, closed bool
	manager             *mcp.Manager
	codeMode            *codemode.Service
	err                 error
}

func (d *nativeGenerationDependencies) get(g *PluginGeneration) (*mcp.Manager, *codemode.Service, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, nil, errors.New("native generation dependencies are closed")
	}
	if !d.initialized {
		d.initialized = true
		// Snapshot the published revocation mask before creating connections.
		// A concurrent revoke waits for this initialization and fences any
		// server that completed starting while the mask changed.
		g.revokedMu.Lock()
		revoked := make(map[string]bool, len(g.revokedPlugins))
		for id, removed := range g.revokedPlugins {
			revoked[id] = removed
		}
		g.revokedMu.Unlock()
		active := make([]pluginpkg.Plugin, 0, len(g.active))
		cfg := g.settings
		cfg.MCPServers = make(map[string]config.MCPServerConfig, len(g.settings.MCPServers))
		for name, server := range g.settings.MCPServers {
			cfg.MCPServers[name] = server
		}
		for _, plugin := range g.active {
			if revoked[plugin.ID] {
				for name := range plugin.MCPServers {
					delete(cfg.MCPServers, PluginMCPServerName(plugin.ID, name))
				}
				continue
			}
			active = append(active, plugin)
		}
		d.manager, d.err = startMCPManager(cfg, active, g.requiredPlugins, g.mcpOAuth)
		if d.err == nil {
			g.bindMCPActivityManager(d.manager)
			d.codeMode = codemode.NewService(codemode.ServiceConfig{NodeExecutable: g.settings.PTC.NodeExecutable})
		}
	}
	return d.manager, d.codeMode, d.err
}

// revoke waits for an in-progress initializer, then fences the retired
// plugin's exact server names without disturbing user-configured servers.
func (d *nativeGenerationDependencies) revoke(plugin pluginpkg.Plugin) error {
	d.mu.Lock()
	manager := d.manager
	d.mu.Unlock()
	if manager == nil {
		return nil
	}
	var err error
	for name := range plugin.MCPServers {
		err = errors.Join(err, manager.Revoke(PluginMCPServerName(plugin.ID, name)))
	}
	return err
}

func (d *nativeGenerationDependencies) close() (bool, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false, nil
	}
	d.closed = true
	used, manager, code := d.initialized, d.manager, d.codeMode
	d.manager, d.codeMode = nil, nil
	d.mu.Unlock()
	var err error
	if manager != nil {
		err = manager.Close()
	}
	if code != nil {
		err = errors.Join(err, code.Close())
	}
	return used, err
}

// RetainActiveMCPManager pins the currently published generation while a host
// operation uses its manager. It never creates native dependencies.
func (s *Session) RetainActiveMCPManager() (*mcp.Manager, func() bool, func()) {
	if s == nil {
		return nil, func() bool { return false }, func() {}
	}
	generation := s.RetainPluginGeneration()
	if generation != nil {
		manager := generation.mcp
		if manager == nil {
			generation.nativeDependencies.mu.Lock()
			manager = generation.nativeDependencies.manager
			generation.nativeDependencies.mu.Unlock()
		}
		if manager != nil {
			return manager, func() bool { return s.IsCurrentPluginGeneration(generation) }, func() { s.ReleasePluginGeneration(generation) }
		}
		s.ReleasePluginGeneration(generation)
	}
	// Legacy embedded sessions can be constructed without a plugin generation.
	if generation == nil && s.Toolkit != nil {
		return s.Toolkit.MCPManager(), func() bool { return s.IsCurrentPluginGeneration(nil) }, func() {}
	}
	return nil, func() bool { return false }, func() {}
}
