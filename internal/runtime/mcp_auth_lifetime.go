package runtime

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/mcp"
)

const mcpAuthLifetime = 10 * time.Minute

// An attempt owns the exact manager and selected services admitted at Start.
// Only the runtime's publication path can keep this authority compatible; a
// callback never transfers an OAuth state to a successor manager.
type mcpAuthAttempt struct {
	session    *Session
	generation *PluginGeneration
	manager    *mcp.Manager
	name       string
	state      string // protected by pluginGenerationMu
	identity   mcpAuthIdentity
	ctx        context.Context
	cancel     context.CancelFunc
	stop       func() bool
	mu         sync.Mutex // physical discovery/exchange/cleanup, never a global I/O lock
	closed     bool
}

type mcpAuthIdentity struct {
	config             mcp.ServerConfig
	connector          []string
	credentials        [][]string
	credentialScope    []string
	credentialOverride *mcp.OAuthManager
}

// StartMCPAuth retains the admitted graph until completion, cancellation or
// expiry. Lifetime belongs to the calling connection, not the start RPC.
func (s *Session) StartMCPAuth(ctx, lifetime context.Context, name, redirect string) (mcp.OAuthStartResult, error) {
	if s == nil {
		return mcp.OAuthStartResult{}, errors.New("runtime is unavailable")
	}
	name = strings.TrimSpace(name)
	if lifetime == nil {
		lifetime = context.Background()
	}
	s.pluginGenerationMu.Lock()
	if s.mcpAuthClosed || s.Toolkit == nil || s.Toolkit.MCPManager() == nil {
		s.pluginGenerationMu.Unlock()
		return mcp.OAuthStartResult{}, errors.New("MCP manager is unavailable")
	}
	if s.pluginGeneration == nil && s.PluginHost != nil {
		s.pluginGeneration = s.capturePluginGeneration()
	}
	generation, manager := s.pluginGeneration, s.Toolkit.MCPManager()
	identity, valid := mcpAuthOwnerIdentity(generation, manager, name)
	if !valid || s.mcpAuthTransition != nil && !compatibleMCPAuthOwner(identity, s.mcpAuthTransition, name) {
		s.pluginGenerationMu.Unlock()
		return mcp.OAuthStartResult{}, mcp.ErrServerRevoked
	}
	attemptCtx, cancel := context.WithTimeout(lifetime, mcpAuthLifetime)
	attempt := &mcpAuthAttempt{session: s, generation: generation, manager: manager, name: name, identity: identity, ctx: attemptCtx, cancel: cancel}
	// Register before any effectful discovery. The opaque attempt object, rather
	// than its server name or not-yet-known OAuth state, owns cancellation.
	attempt.mu.Lock()
	if generation != nil {
		generation.retain()
	}
	if s.mcpAuthAttempts == nil {
		s.mcpAuthAttempts = make(map[*mcpAuthAttempt]struct{})
	}
	var superseded []*mcpAuthAttempt
	for older := range s.mcpAuthAttempts {
		if older.name == name {
			older.cancel()
			superseded = append(superseded, older)
		}
	}
	s.mcpAuthAttempts[attempt] = struct{}{}
	s.pluginGenerationMu.Unlock()
	attempt.stop = context.AfterFunc(attemptCtx, func() { _ = attempt.close() })
	for _, older := range superseded {
		_ = older.close()
	}
	operationCtx, operationCancel := context.WithCancel(ctx)
	stop := context.AfterFunc(attemptCtx, operationCancel)
	result, err := manager.StartOAuthWithRedirect(operationCtx, name, redirect)
	stop()
	operationCancel()
	s.pluginGenerationMu.Lock()
	attempt.state = result.State
	s.pluginGenerationMu.Unlock()
	err = errors.Join(err, attemptCtx.Err())
	attempt.mu.Unlock()
	if err != nil {
		_ = attempt.close()
		return mcp.OAuthStartResult{}, err
	}
	return result, nil
}

func (s *Session) mcpAuthAttempt(name, state string) *mcpAuthAttempt {
	if s == nil {
		return nil
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	for attempt := range s.mcpAuthAttempts {
		if attempt.name == strings.TrimSpace(name) && state != "" && attempt.state == state {
			return attempt
		}
	}
	return nil
}

// FinishMCPAuth uses only the original admitted authority. Publication revokes
// incompatible attempts before it exposes a new selected graph.
func (s *Session) FinishMCPAuth(ctx context.Context, name, state, code string) (mcp.ServerStatus, mcp.OAuthStatus, error) {
	attempt := s.mcpAuthAttempt(name, state)
	if attempt == nil {
		return mcp.ServerStatus{}, mcp.OAuthStatus{}, errors.New("invalid or expired OAuth state")
	}
	attempt.mu.Lock()
	if attempt.closed || attempt.ctx.Err() != nil {
		attempt.mu.Unlock()
		return mcp.ServerStatus{}, mcp.OAuthStatus{}, mcp.ErrOAuthSuperseded
	}
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(attempt.ctx, cancel)
	status, err := attempt.manager.FinishOAuth(operationCtx, name, state, code)
	var auth mcp.OAuthStatus
	if err == nil {
		auth, err = attempt.manager.OAuthStatus(operationCtx, name)
	}
	stop()
	cancel()
	attempt.mu.Unlock()
	closeErr := attempt.close()
	return status, auth, errors.Join(err, closeErr)
}

// CancelMCPAuth addresses an exact pending state and never signs out an account.
func (s *Session) CancelMCPAuth(ctx context.Context, name, state string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	attempt := s.mcpAuthAttempt(name, state)
	if attempt == nil {
		return nil
	}
	return attempt.close()
}

func (a *mcpAuthAttempt) close() error {
	a.cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.stop != nil {
		a.stop()
	}
	// The operation has physically returned. Cancellation only fences its pending
	// login generation; already accepted tokens remain available for reconnect.
	var err error
	if a.state != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = a.manager.CancelOAuth(ctx, a.name, a.state)
		cancel()
	}
	a.session.pluginGenerationMu.Lock()
	delete(a.session.mcpAuthAttempts, a)
	a.session.pluginGenerationMu.Unlock()
	if a.generation != nil {
		err = errors.Join(err, a.session.releasePluginGeneration(a.generation))
	}
	return err
}

// prepareMCPAuthPublication closes incompatible admission under the lookup lock,
// then performs cancellation and the existing per-server commit fence outside
// it. Starts arriving during this interval must also match the candidate.
func (s *Session) prepareMCPAuthPublication(candidate *PluginGeneration) {
	s.pluginGenerationMu.Lock()
	s.mcpAuthTransition = candidate
	var revoked []*mcpAuthAttempt
	for attempt := range s.mcpAuthAttempts {
		if !compatibleMCPAuthOwner(attempt.identity, candidate, attempt.name) {
			attempt.cancel()
			revoked = append(revoked, attempt)
		}
	}
	s.pluginGenerationMu.Unlock()
	for _, attempt := range revoked {
		_ = attempt.manager.Revoke(attempt.name)
		_ = attempt.close()
	}
}

func (s *Session) closeMCPAuthAttempts() {
	s.pluginGenerationMu.Lock()
	s.mcpAuthClosed = true
	var attempts []*mcpAuthAttempt
	for attempt := range s.mcpAuthAttempts {
		attempt.cancel()
		attempts = append(attempts, attempt)
	}
	s.pluginGenerationMu.Unlock()
	for _, attempt := range attempts {
		_ = attempt.close()
	}
}

func compatibleMCPAuthOwner(identity mcpAuthIdentity, generation *PluginGeneration, name string) bool {
	if generation == nil {
		return false
	}
	candidate, valid := mcpAuthOwnerIdentity(generation, generation.mcp, name)
	return valid && reflect.DeepEqual(identity, candidate)
}

func mcpAuthOwnerIdentity(generation *PluginGeneration, manager *mcp.Manager, name string) (mcpAuthIdentity, bool) {
	cfg, ok := manager.Config(name)
	if !ok || !cfg.IsEnabled() || strings.TrimSpace(cfg.URL) == "" {
		return mcpAuthIdentity{}, false
	}
	// Tool policy does not change the resource/account granting authority.
	cfg.ToolTimeoutMS = 0
	cfg.ToolOverrides = nil
	cfg.Enabled = nil
	identity := mcpAuthIdentity{config: cfg}
	if generation != nil {
		identity.credentialScope = append([]string(nil), generation.credentialScope...)
		identity.credentialOverride = generation.mcpOAuthOverride
	}
	if generation == nil {
		return identity, !IsPluginMCPServerName(name)
	}
	available := generation.availableModuleIDs()
	if IsPluginMCPServerName(name) {
		found := false
		for _, item := range generation.active {
			if !available[item.ID] {
				continue
			}
			for localName, definition := range item.MCPServers {
				if PluginMCPServerName(item.ID, localName) != name {
					continue
				}
				if definition.Connector != nil && strings.TrimSpace(definition.Connector.SetupRequired) != "" {
					return mcpAuthIdentity{}, false
				}
				identity.connector = mcpAuthPackageIdentity(generation, item.ID)
				found = true
			}
		}
		if !found {
			return mcpAuthIdentity{}, false
		}
	}
	identity.credentials, ok = mcpCredentialOwnership(generation)
	return identity, ok
}

func mcpAuthPackageIdentity(generation *PluginGeneration, id string) []string {
	for _, item := range generation.active {
		if item.ID == id {
			return []string{item.ID, item.Source, item.SubjectID, item.Fingerprint, item.WorkspaceID, strconv.FormatBool(item.Official), strconv.FormatBool(item.AuthorizedDev)}
		}
	}
	return []string{id}
}

func mcpCredentialOwnership(generation *PluginGeneration) ([][]string, bool) {
	var owners [][]string
	available := generation.availableModuleIDs()
	if generation.host != nil && generation.host.ServiceRegistry() != nil {
		registry := generation.host.ServiceRegistry()
		consumer := StateServicesConsumer
		visited := make(map[string]bool)
		for {
			provider, ok := registry.ProviderPluginIDFor(consumer, credentialstore.ServiceName, 1)
			if !ok {
				break
			}
			if visited[provider] {
				return nil, false
			}
			if !available[provider] {
				return nil, false
			}
			visited[provider] = true
			settingsIdentity := generation.credentialSettings[provider]
			if settingsIdentity == "" {
				for _, item := range generation.active {
					if item.ID == provider && len(item.Settings) > 0 {
						settingsIdentity = "generation:" + generation.id
					}
				}
			}
			owners = append(owners, append(mcpAuthPackageIdentity(generation, provider), settingsIdentity))
			// Providers with no dependency terminate the selected ownership chain.
			required := false
			for _, dependency := range generation.host.Dependencies()[provider] {
				if dependency.Service == credentialstore.ServiceName {
					required = true
				}
			}
			if !required {
				break
			}
			consumer = provider
		}
	}
	return owners, true
}

// Only synchronization is inherited. Each generation retains its own selected
// client and pending-login owners, including independent provider retirement.
func (s *Session) shareMCPRefreshCoordinator(candidate *PluginGeneration) {
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	old := s.pluginGeneration
	if old == nil || old.mcpOAuth == nil || candidate.mcpOAuth == nil || old.mcpOAuthOverride != nil || candidate.mcpOAuthOverride != nil || !reflect.DeepEqual(old.credentialScope, candidate.credentialScope) {
		return
	}
	previous, previousOK := mcpCredentialOwnership(old)
	current, currentOK := mcpCredentialOwnership(candidate)
	if previousOK && currentOK && len(previous) > 0 && reflect.DeepEqual(previous, current) {
		candidate.mcpOAuth.ShareRefreshCoordinator(old.mcpOAuth)
	}
}

// These roots are the semantic namespace supplied to selected providers. A new
// client instance alone is compatible; redirecting that provider is not.
func (s *Session) mcpCredentialScope() []string {
	return []string{s.WuuHome, s.HomeDir, s.RootDir, s.WorkspaceID, s.UserSpaceID, s.ProductDomain, s.DataRoot}
}

// DrainCanceledMCPAuth joins physical cleanup after a connection closes without
// canceling attempts belonging to any other still-live connection.
func (s *Session) DrainCanceledMCPAuth() {
	s.pluginGenerationMu.Lock()
	var attempts []*mcpAuthAttempt
	for attempt := range s.mcpAuthAttempts {
		if attempt.ctx.Err() != nil {
			attempts = append(attempts, attempt)
		}
	}
	s.pluginGenerationMu.Unlock()
	for _, attempt := range attempts {
		_ = attempt.close()
	}
}

// CancelPendingMCPAuth fences every pending login for an explicit sign-out.
func (s *Session) CancelPendingMCPAuth(name string) error {
	s.pluginGenerationMu.Lock()
	var attempts []*mcpAuthAttempt
	for attempt := range s.mcpAuthAttempts {
		if attempt.name == strings.TrimSpace(name) {
			attempt.cancel()
			attempts = append(attempts, attempt)
		}
	}
	s.pluginGenerationMu.Unlock()
	var err error
	for _, attempt := range attempts {
		err = errors.Join(err, attempt.close())
	}
	return err
}
