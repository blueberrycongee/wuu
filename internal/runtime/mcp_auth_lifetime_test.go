package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/mcp"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

// The ordinary graph deliberately creates a distinct selected credential client
// and MCP manager on every replacement. No injected shared OAuthManager hides it.
func TestMCPAuthRetainsAdmittedOwnerAcrossCompatiblePublication(t *testing.T) {
	rt, cfg := mcpAuthRuntime(t)
	old := rt.Toolkit.MCPManager()
	start, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers["docs"]
	server.ToolTimeoutMS = 81
	cfg.MCPServers["docs"] = server
	publishMCPAuthConfig(t, rt, cfg)
	if rt.Toolkit.MCPManager() == old {
		t.Fatal("replacement reused manager")
	}
	status, auth, err := rt.FinishMCPAuth(context.Background(), "docs", start.State, "accepted")
	if err != nil || !auth.Authenticated || status.Name != "docs" {
		t.Fatalf("finish retained owner: %v %+v %+v", err, status, auth)
	}
	current, err := rt.Toolkit.MCPManager().OAuthStatus(context.Background(), "docs")
	if err != nil || !current.Authenticated {
		t.Fatalf("selected provider lost accepted credentials: %+v %v", current, err)
	}
	if _, _, err := rt.FinishMCPAuth(context.Background(), "docs", start.State, "replayed"); err == nil {
		t.Fatal("terminal attempt replayed")
	}
}

func TestMCPAuthIncompatiblePublicationRevokesBeforeCallback(t *testing.T) {
	for _, change := range []string{"disabled", "removed", "resource-query", "oauth-client", "headers", "credential-provider", "credential-namespace"} {
		t.Run(change, func(t *testing.T) {
			rt, cfg := mcpAuthRuntime(t)
			start, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
			if err != nil {
				t.Fatal(err)
			}
			server := cfg.MCPServers["docs"]
			switch change {
			case "disabled":
				enabled := false
				server.Enabled = &enabled
			case "removed":
				delete(cfg.MCPServers, "docs")
			case "resource-query":
				server.URL += "?different-resource=1"
			case "oauth-client":
				server.OAuth.ClientID = "replacement-client"
			case "headers":
				server.Headers = map[string]string{"X-Account": "replacement"}
			case "credential-namespace":
				rt.WuuHome = filepath.Join(t.TempDir(), "replacement-home")
			case "credential-provider":
				cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{credentialstore.FileModule: false, "fixture.credentials": true}})
			}
			if change != "removed" {
				cfg.MCPServers["docs"] = server
			}
			if change == "credential-namespace" {
				// Moving the provider namespace also degrades unrelated storage;
				// a committed graph must still revoke the old credential owner.
				candidate, err := rt.PreflightDiscoveredExtensions(cfg, []pluginpkg.Plugin{})
				if err != nil {
					t.Fatal(err)
				}
				if err = rt.ActivatePluginGeneration(candidate, nil); err != nil && !PluginGenerationWasCommitted(err) {
					t.Fatal(err)
				}
			} else {
				publishMCPAuthConfig(t, rt, cfg)
			}
			if _, _, err := rt.FinishMCPAuth(context.Background(), "docs", start.State, "late"); err == nil {
				t.Fatal("incompatible owner accepted callback")
			}
		})
	}
}

func TestMCPAuthCancelPreservesAcceptedCredentialsAcrossPublication(t *testing.T) {
	rt, cfg := mcpAuthRuntime(t)
	accepted, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.FinishMCPAuth(context.Background(), "docs", accepted.State, "accepted"); err != nil {
		t.Fatal(err)
	}
	pending, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers["docs"]
	server.ToolTimeoutMS = 82
	cfg.MCPServers["docs"] = server
	publishMCPAuthConfig(t, rt, cfg)
	if err := rt.CancelMCPAuth(context.Background(), "docs", pending.State); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.FinishMCPAuth(context.Background(), "docs", pending.State, "late"); err == nil {
		t.Fatal("canceled attempt accepted")
	}
	status, err := rt.Toolkit.MCPManager().OAuthStatus(context.Background(), "docs")
	if err != nil || !status.Authenticated {
		t.Fatalf("cancel removed accepted credentials: %+v %v", status, err)
	}
}

func TestMCPAuthCanceledDiscoveryReleasesRetainedGeneration(t *testing.T) {
	started := make(chan struct{})
	rt, cfg := mcpAuthRuntime(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/.well-known/oauth-protected-resource" {
			return false
		}
		close(started)
		<-r.Context().Done()
		return true
	})
	old := rt.Toolkit.MCPManager()
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := rt.StartMCPAuth(context.Background(), lifetime, "docs", ""); done <- err }()
	<-started
	server := cfg.MCPServers["docs"]
	server.ToolTimeoutMS = 83
	cfg.MCPServers["docs"] = server
	publishMCPAuthConfig(t, rt, cfg)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled discovery returned authority")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled discovery did not finish physical cleanup")
	}
	if err := old.Connect(context.Background(), "docs"); !errors.Is(err, mcp.ErrManagerClosed) {
		t.Fatalf("canceled attempt retained retired manager: %v", err)
	}
}

func TestMCPAuthSupersessionBeforeStateDoesNotCancelNewAttempt(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	rt, _ := mcpAuthRuntime(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/.well-known/oauth-protected-resource" || calls.Add(1) != 1 {
			return false
		}
		close(started)
		<-r.Context().Done()
		return true
	})
	oldDone := make(chan error, 1)
	go func() {
		_, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
		oldDone <- err
	}()
	<-started
	newer, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-oldDone; err == nil {
		t.Fatal("superseded discovery returned authority")
	}
	if err := rt.CancelMCPAuth(context.Background(), "docs", ""); err != nil {
		t.Fatal(err)
	}
	if _, auth, err := rt.FinishMCPAuth(context.Background(), "docs", newer.State, "accepted"); err != nil || !auth.Authenticated {
		t.Fatalf("old cancellation affected newer attempt: %+v %v", auth, err)
	}
}

func TestMCPAuthExpiryReleasesPendingDiscovery(t *testing.T) {
	started := make(chan struct{})
	rt, _ := mcpAuthRuntime(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/.well-known/oauth-protected-resource" {
			return false
		}
		close(started)
		<-r.Context().Done()
		return true
	})
	lifetime, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := rt.StartMCPAuth(context.Background(), lifetime, "docs", ""); done <- err }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("discovery did not start: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatalf("expiry error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expired attempt retained discovery")
	}
	rt.DrainCanceledMCPAuth()
	rt.pluginGenerationMu.Lock()
	pending := len(rt.mcpAuthAttempts)
	rt.pluginGenerationMu.Unlock()
	if pending != 0 {
		t.Fatal("expired attempt still owns generation")
	}
}

func TestMCPAuthUnrelatedShadowCleanupPreservesRootAttempt(t *testing.T) {
	rt, _ := mcpAuthRuntime(t)
	pending, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	shadow, err := rt.cloneForThreadModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shadow.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, auth, err := rt.FinishMCPAuth(context.Background(), "docs", pending.State, "accepted"); err != nil || !auth.Authenticated {
		t.Fatalf("shadow cleanup canceled root auth: %+v %v", auth, err)
	}
}

func TestMCPAuthPackageReplacementCannotReusePendingOwner(t *testing.T) {
	for _, change := range []string{"source", "fingerprint", "subject", "setup"} {
		t.Run(change, func(t *testing.T) {
			rt, cfg := mcpAuthRuntime(t)
			definition := cfg.MCPServers["docs"]
			definition.Connector = &config.MCPConnectorDescriptor{Title: "Synthetic connector"}
			delete(cfg.MCPServers, "docs")
			item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "fixture.connector", MCPServers: map[string]config.MCPServerConfig{"docs": definition}}, Source: "bundled", Official: true, SubjectID: "fixture-owner", Fingerprint: "fixture-original"}
			cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{item.ID: true}})
			publish := func() {
				t.Helper()
				candidate, err := rt.PreflightDiscoveredExtensions(cfg, []pluginpkg.Plugin{item})
				if err != nil {
					t.Fatal(err)
				}
				if err = rt.ActivatePluginGeneration(candidate, nil); err != nil {
					t.Fatal(err)
				}
			}
			publish()
			name := PluginMCPServerName(item.ID, "docs")
			pending, err := rt.StartMCPAuth(context.Background(), context.Background(), name, "")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "source":
				item.Source = "alternate-bundle"
			case "fingerprint":
				item.Fingerprint = "replacement"
			case "subject":
				item.SubjectID = "replacement-owner"
			case "setup":
				item.Fingerprint = "replacement-setup"
				definition.Connector = &config.MCPConnectorDescriptor{Title: "Synthetic connector", SetupRequired: "Synthetic setup"}
				item.MCPServers = map[string]config.MCPServerConfig{"docs": definition}
			}
			publish()
			if _, _, err := rt.FinishMCPAuth(context.Background(), name, pending.State, "late"); err == nil {
				t.Fatal("replaced package accepted original callback")
			}
		})
	}
}

func TestMCPAuthInjectedCredentialAuthorityDoesNotBecomeSelectedProvider(t *testing.T) {
	injected := mcp.NewOAuthManager(credentialstore.NewFileStore(filepath.Join(t.TempDir(), "injected.json")), nil)
	rt, cfg := mcpAuthRuntimeWithOptions(t, func(options *Options) { options.MCPOAuth = injected })
	pending, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers["docs"]
	server.ToolTimeoutMS = 84
	cfg.MCPServers["docs"] = server
	publishMCPAuthConfig(t, rt, cfg)
	if _, _, err := rt.FinishMCPAuth(context.Background(), "docs", pending.State, "late"); err == nil {
		t.Fatal("injected store inherited selected-provider authority")
	}
}

func TestMCPAuthPublicationCancelsExchangeWithoutBlockingSibling(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	rt, cfg := mcpAuthRuntime(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/token" {
			return false
		}
		r.ParseForm()
		if r.Form.Get("code") != "held" {
			return false
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "late-token"})
		return true
	})
	defer close(release)
	cfg.MCPServers["other"] = cfg.MCPServers["docs"]
	publishMCPAuthConfig(t, rt, cfg)
	pending, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := rt.StartMCPAuth(context.Background(), context.Background(), "other", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := rt.FinishMCPAuth(context.Background(), "docs", pending.State, "held")
		done <- err
	}()
	<-started
	server := cfg.MCPServers["docs"]
	disabled := false
	server.Enabled = &disabled
	cfg.MCPServers["docs"] = server
	candidate, err := rt.PreflightDiscoveredExtensions(cfg, []pluginpkg.Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	published := make(chan error, 1)
	go func() { published <- rt.ActivatePluginGeneration(candidate, nil) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, auth, err := rt.FinishMCPAuth(ctx, "other", sibling.State, "accepted"); err != nil || !auth.Authenticated {
		t.Fatalf("revocation blocked sibling: %+v %v", auth, err)
	}
	select {
	case err := <-published:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication failed to cancel token exchange")
	}
	if err := <-done; err == nil {
		t.Fatal("revoked exchange committed credentials")
	}
	auth, err := rt.Toolkit.MCPManager().OAuthStatus(context.Background(), "docs")
	if err != nil || auth.Authenticated {
		t.Fatalf("late revoked grant survived publication: %+v %v", auth, err)
	}
}

func mcpAuthRuntime(t *testing.T, intercept ...func(http.ResponseWriter, *http.Request) bool) (*Session, config.Config) {
	t.Helper()
	return mcpAuthRuntimeWithOptions(t, nil, intercept...)
}

func mcpAuthRuntimeWithOptions(t *testing.T, customize func(*Options), intercept ...func(http.ResponseWriter, *http.Request) bool) (*Session, config.Config) {
	t.Helper()
	var endpoint string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(intercept) > 0 && intercept[0](w, r) {
			return
		}
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			json.NewEncoder(w).Encode(map[string]any{"resource": endpoint, "authorization_servers": []string{endpoint}})
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token"})
		case "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-accepted", "expires_in": 3600})
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(peer.Close)
	endpoint = peer.URL
	root := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(root, "home"))
	t.Setenv("PATH", root)
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: endpoint, Model: "fixture", APIKey: "synthetic-unused"}}, MCPServers: map[string]config.MCPServerConfig{"docs": {URL: endpoint, OAuth: &config.MCPOAuthConfig{ClientID: "fixture", RedirectURI: "http://127.0.0.1/callback"}}}, Composition: composition.DefaultConfig("headless")}
	cfg.Composition.Patches = []composition.Patch{{Modules: map[string]bool{credentialstore.OSModule: false, credentialstore.FileModule: true}}}
	packages := []pluginpkg.Plugin{}
	options := Options{RootDir: root, HomeDir: root, Config: cfg, DiscoveredPlugins: &packages, NativeProviders: []NativeProviderFactory{{ID: "fixture.credentials", Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService("fixture.credentials", func() (credentialstore.Store, error) {
			return credentialstore.NewFileStore(filepath.Join(root, "replacement-credentials.json")), nil
		}), nil
	}}}}
	if customize != nil {
		customize(&options)
	}
	rt, err := NewSession(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	return rt, cfg
}

func publishMCPAuthConfig(t *testing.T, rt *Session, cfg config.Config) {
	t.Helper()
	candidate, err := rt.PreflightDiscoveredExtensions(cfg, []pluginpkg.Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
}

// This synthetic external provider keeps its package identity but selects its
// credential namespace from a restart-scoped setting, like a vault endpoint.
type mcpConfiguredCredentialProvider struct {
	*credentialstore.Service
	settings *pluginsettings.Client
	subject  string
	vault    string
}

func (p *mcpConfiguredCredentialProvider) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginsettings.ServiceName, MajorVersion: 1, Required: true}}
}
func (p *mcpConfiguredCredentialProvider) Prepare(ctx context.Context) error {
	document, err := p.settings.Read(ctx, p.subject, pluginsettings.ScopeUser)
	if err != nil {
		return err
	}
	p.vault = "original"
	if raw, ok := document.Values["vault"]; ok {
		return json.Unmarshal(raw, &p.vault)
	}
	return nil
}

func TestMCPAuthCredentialProviderConfigurationChangeRevokesOriginalVault(t *testing.T) {
	for _, change := range []string{"unchanged", "changed", "mutable", "secret"} {
		t.Run(change, func(t *testing.T) {
			rt, cfg := mcpAuthRuntime(t)
			root := t.TempDir()
			item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "fixture.configured-credentials", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: "synthetic"}, Settings: map[string]pluginpkg.SettingDefinition{}}, Source: "bundled", Official: true, SubjectID: "fixture-configured-vault", Fingerprint: "unchanged"}
			item.Settings[item.ID+".vault"] = pluginpkg.SettingDefinition{Type: pluginpkg.SettingTypeString, Default: "original", Scope: pluginpkg.SettingScopeUser, Apply: pluginpkg.SettingApplyRestart}
			if change == "mutable" {
				definition := item.Settings[item.ID+".vault"]
				definition.Apply = pluginpkg.SettingApplyLive
				item.Settings[item.ID+".vault"] = definition
			}
			if change == "secret" {
				item.Settings[item.ID+".password"] = pluginpkg.SettingDefinition{Type: pluginpkg.SettingTypeSecret, Default: "", Scope: pluginpkg.SettingScopeUser, Apply: pluginpkg.SettingApplyRestart}
			}
			cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{credentialstore.FileModule: false, item.ID: true}})
			start := func(ctx context.Context, input pluginhost.ProcessConfig) (pluginhost.Client, error) {
				provider := &mcpConfiguredCredentialProvider{subject: item.SubjectID}
				provider.settings = pluginsettings.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
					result, serviceErr := input.ServiceRouter.RouteServiceCall(ctx, item.ID, pluginhost.ServiceCallParams{Service: pluginsettings.ServiceName, Method: method, Params: raw})
					if serviceErr != nil {
						return nil, serviceErr
					}
					return result, nil
				})
				provider.Service = credentialstore.NewService(item.ID, func() (credentialstore.Store, error) {
					return credentialstore.NewFileStore(filepath.Join(root, provider.vault+".json")), nil
				})
				return provider, nil
			}
			publish := func() {
				t.Helper()
				candidate, err := rt.buildPluginGeneration(cfg, []pluginpkg.Plugin{item}, nil, nil, start)
				if err != nil {
					t.Fatal(err)
				}
				if err = rt.ActivatePluginGeneration(candidate, nil); err != nil {
					t.Fatal(err)
				}
			}
			publish()
			pending, err := rt.StartMCPAuth(context.Background(), context.Background(), "docs", "")
			if err != nil {
				t.Fatal(err)
			}
			if change == "changed" {
				services, release, err := rt.RetainStateServices()
				if err != nil {
					t.Fatal(err)
				}
				_, err = services.Settings.Update(context.Background(), item.SubjectID, pluginsettings.ScopeUser, item.Fingerprint, func(values map[string]json.RawMessage) error {
					values["vault"] = json.RawMessage(`"replacement"`)
					return nil
				})
				release()
				if err != nil {
					t.Fatal(err)
				}
			}
			publish()
			_, _, err = rt.FinishMCPAuth(context.Background(), "docs", pending.State, "late")
			if change == "unchanged" && err != nil {
				t.Fatalf("unchanged prepared authority was not retained: %v", err)
			}
			if change != "unchanged" && err == nil {
				t.Fatal("changed or unverifiable credential authority accepted old callback")
			}
			original := mcp.NewOAuthManager(credentialstore.NewFileStore(filepath.Join(root, "original.json")), nil)
			if auth, err := original.Status(context.Background(), "docs", cfg.MCPServers["docs"].URL); err != nil || auth.Authenticated != (change == "unchanged") {
				t.Fatalf("retired vault authentication state: %+v %v", auth, err)
			}
		})
	}
}
