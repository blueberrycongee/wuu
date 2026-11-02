package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type personalSharedConnectors struct{ server *Server }

func (p personalSharedConnectors) state(ctx context.Context, actor personalconnectors.Actor) (*runtime.State, error) {
	binding := p.server.rt.UserBinding()
	if actor.UserSpaceID != binding.UserSpaceID || actor.ProductDomain != binding.Domain() || !binding.LocalAccounts() {
		return nil, &personalconnectors.Failure{Code: "shared_account_owner_required"}
	}
	return runtime.RetainProductInvocationState(ctx)
}

// Each operation opens its own transport, using the same selected credential
// store and server identity as Coding. No tokens or login state are copied into
// Personal. Reading current preferences also observes changes from other hosts.
func sharedConnectorDefinitions(ctx context.Context, state *runtime.State) (*runtime.StateServices, map[string]config.MCPServerConfig, map[string]string, error) {
	services, err := state.Services()
	if err != nil {
		return nil, nil, nil, err
	}
	view, err := services.Config.Read(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	source, err := editableConfigSource(view)
	if err != nil {
		return nil, nil, nil, err
	}
	var ownerConfig config.Config
	if err = json.Unmarshal(source, &ownerConfig); err != nil {
		return nil, nil, nil, err
	}
	definitions, owners := map[string]config.MCPServerConfig{}, map[string]string{}
	for _, item := range services.Snapshot.ActivePlugins {
		if !slices.ContainsFunc(services.Snapshot.Plugins, func(installed pluginpkg.Plugin) bool {
			return installed.ID == item.ID && installed.Source == item.Source && installed.Fingerprint == item.Fingerprint
		}) {
			continue
		}
		for localName, definition := range item.MCPServers {
			if definition.Connector == nil {
				continue
			}
			name := runtime.PluginMCPServerName(item.ID, localName)
			if preference, ok := ownerConfig.MCPServers[name]; ok && preference.Enabled != nil {
				definition.Enabled = preference.Enabled
			}
			if !definition.ConnectorEnabled() {
				continue
			}
			definitions[name] = definition
			identity, _ := json.Marshal([]string{item.ID, item.Source, item.Fingerprint, item.SubjectID, item.WorkspaceID})
			owners[name] = string(identity)
		}
	}
	return services, definitions, owners, nil
}

func sharedConnector(ctx context.Context, services *runtime.StateServices, name string, definition config.MCPServerConfig, owner string) (personalconnectors.Connection, *mcp.Manager, error) {
	cfg := runtime.MCPServerConfiguration(name, definition)
	cfg.IdentityProbe, cfg.IdentitySource = definition.Connector.IdentityProbe, owner
	manager := mcp.NewManager()
	manager.SetOAuthManager(mcp.NewOAuthManager(services.Credentials, nil))
	manager.Configure(map[string]mcp.ServerConfig{name: cfg})
	auth := mcp.OAuthStatus{}
	connection := personalconnectors.Connection{ID: "mcp:" + name, Provider: "mcp", AccountLabel: definition.Connector.Title, AccountID: name, Domain: "work", WorkReadPolicy: "selected_resources", ScopeVerification: "mcp_connection", State: "unavailable"}
	if cfg.URL != "" {
		var err error
		auth, err = manager.OAuthStatus(ctx, name)
		if err != nil {
			_ = manager.Close()
			return connection, nil, err
		}
		if auth.Authenticated {
			connection.ScopeVerification = "mcp_oauth"
		} else {
			connection.State = "needs_reauth"
		}
	}
	connection.Scopes = append([]string{}, auth.Scopes...)
	if auth.Identity != nil && auth.Identity.State == "verified" && auth.Identity.Account != nil {
		connection.AccountID = auth.Identity.Account.ID
		if auth.Identity.Account.Label != "" {
			connection.AccountLabel = auth.Identity.Account.Label
		}
		if auth.Identity.Workspace != nil {
			connection.WorkspaceID = auth.Identity.Workspace.ID
		}
	}
	// Never expose URL credentials/query parameters through the model catalog.
	connection.Resources = []string{name}
	if err := manager.Connect(ctx, name); err == nil {
		connection.State = "connected"
		for _, tool := range manager.NativeTools() {
			override := definition.ToolOverrides[tool.Definition.Name]
			connection.Tools = append(connection.Tools, personalconnectors.Operation{Name: tool.Definition.Name, Description: tool.Definition.Description, InputSchema: tool.Definition.InputSchema, ReadOnly: override.ReadOnly != nil && *override.ReadOnly})
		}
	} else if auth.Authenticated {
		connection.State = "unavailable"
	}
	identity, _ := json.Marshal(struct {
		Owner, Authorization, Account, Workspace string
		Scopes                                   []string
		Definition                               config.MCPServerConfig
		Tools                                    []personalconnectors.Operation
	}{owner, auth.AuthorizationID, connection.AccountID, connection.WorkspaceID, connection.Scopes, definition, connection.Tools})
	digest := sha256.Sum256(identity)
	connection.Revision = hex.EncodeToString(digest[:])
	return connection, manager, nil
}

func (p personalSharedConnectors) List(ctx context.Context, actor personalconnectors.Actor) ([]personalconnectors.Connection, error) {
	state, err := p.state(ctx, actor)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	services, definitions, owners, err := sharedConnectorDefinitions(ctx, state)
	if err != nil {
		return nil, err
	}
	connections := []personalconnectors.Connection{}
	for name, definition := range definitions {
		connection, manager, err := sharedConnector(ctx, services, name, definition, owners[name])
		if err != nil {
			return nil, err
		}
		_ = manager.Close()
		connections = append(connections, connection)
	}
	return connections, nil
}

func (p personalSharedConnectors) Call(ctx context.Context, actor personalconnectors.Actor, expected personalconnectors.Connection, operation string, arguments json.RawMessage) (json.RawMessage, bool, error) {
	state, err := p.state(ctx, actor)
	if err != nil {
		return nil, false, err
	}
	defer state.Close()
	services, definitions, owners, err := sharedConnectorDefinitions(ctx, state)
	if err != nil {
		return nil, false, err
	}
	name := strings.TrimPrefix(expected.ID, "mcp:")
	definition, ok := definitions[name]
	if !ok {
		return nil, false, &personalconnectors.Failure{Code: "connection_changed"}
	}
	current, manager, err := sharedConnector(ctx, services, name, definition, owners[name])
	if err != nil {
		return nil, false, err
	}
	defer manager.Close()
	if current.State != "connected" || current.Revision != expected.Revision {
		return nil, false, &personalconnectors.Failure{Code: "connection_changed"}
	}
	for _, tool := range manager.NativeTools() {
		if tool.Definition.Name != operation {
			continue
		}
		// Discovery can take time. Recheck the selected package and live preference
		// after it finishes, before admitting the exact external call.
		_, latest, latestOwners, err := sharedConnectorDefinitions(ctx, state)
		if err != nil {
			return nil, false, err
		}
		if !reflect.DeepEqual(latest[name], definition) || latestOwners[name] != owners[name] {
			return nil, false, &personalconnectors.Failure{Code: "connection_changed"}
		}
		readOnly := definition.ToolOverrides[operation].ReadOnly
		write := readOnly == nil || !*readOnly
		result, err := tool.Client.CallTool(ctx, operation, arguments)
		if err != nil || result.IsError {
			return nil, write, &personalconnectors.Failure{Code: "mcp_result_unconfirmed"}
		}
		data, err := json.Marshal(result)
		return data, write && err != nil, err
	}
	return nil, false, errors.New("MCP operation is no longer available")
}
