# MCP servers

MCP connects the Wuu engine to tools provided by a local process or remote service. After connecting, ask the agent to use the server by name and describe the task. Wuu discovers and calls tools; it does not currently provide a resource or prompt browser, and MCP requests for an additional user-input exchange are unsupported.

## Connect an account

This plugin connector flow currently supports the **Coding / current-project host only**. User-bound Personal/twin hosts keep shared MCP configuration out of their domain and show setup as unavailable rather than changing shared preferences or credentials. Existing dedicated Personal connectors are unchanged; using this picker there requires domain-scoped connection identity, preferences, and credential storage.

Open **Plugins → Account Connectors**. The bundled plugin is enabled by default so its connector cards are discoverable, but every service is initially disabled: no account connection starts until you choose to connect it. Disabling the plugin stops its services and removes its connector behavior.

- **Notion:** choose **Connect** to enable the service and begin browser authorization. Review the workspace and access requested by Notion before approving. The official endpoint is `https://mcp.notion.com/mcp`; it supports public-client dynamic registration and PKCE, so no internal integration token or client secret is needed. See [Notion's MCP client guide](https://developers.notion.com/guides/mcp/build-mcp-client).
- **GitHub:** setup is required; the card provides instructions, not a Connect action. Wuu needs its own registered GitHub App or OAuth client before offering account authorization. The official `https://api.githubcopilot.com/mcp/` URL alone is insufficient. See [GitHub's MCP server documentation](https://github.com/github/github-mcp-server).
- **Google Drive:** setup is required; no Connect action is offered. The official preview endpoint is `https://drivemcp.googleapis.com/mcp/v1`, not a generic Google connector. It requires Workspace Developer Preview membership, a Cloud project with the Drive and MCP APIs enabled, and a registered AI-agent Desktop OAuth client with consent and scopes configured. See Google's [MCP setup](https://developers.google.com/workspace/guides/configure-mcp-servers) and [client registration](https://developers.google.com/workspace/guides/create-credentials).

Connect saves the enabled preference through the selected configuration provider and checks its revision. If another window changed the configuration, reload it before trying again. Authorization uses the [remote OAuth flow](#remote-oauth), including cancellation and expiry; a canceled or failed login does not grant access. Provider account and workspace policies can still prevent authorization.

When enabling rebuilds the plugin graph, the current window carries only that explicit Connect intent to the replacement controls. Browser authorization starts once, after the original save succeeds and the same context, plugin source, and resource are validated again. Canceling, navigating away, replacing the plugin, or the one-minute handoff expiry discards the intent; failed saves are not retried automatically. This handoff exists only in memory, contains no OAuth material, and cannot resume after restarting Wuu. Already-enabled connectors can retry login without another configuration write.

A selected remote service that requires authorization remains registered after plugin refresh or sign-out, so you can sign in without losing sibling connectors. It exposes no tools until connected; a hard startup failure is not treated as a successful connection.

An OAuth-required service shows **Authentication required**, not a generic connection failure. During a same-context, same-source graph refresh, connector labels can remain visible as non-interactive placeholders until the new owner loads its list. Account identity and authorization details are not retained in those placeholders; context or plugin-source changes discard them.

A connector's **Disconnect** removes its saved OAuth credentials and saves the service as disabled, retaining the definition. Projects using the same canonical connector name and credential provider share that login: replacing it or disconnecting affects those projects too. Current-project enablement does not imply a separate account per project. Disconnect does not revoke the grant at the provider; use the provider's connected-app settings when you also want to revoke access there.

Expand the connection details to inspect the endpoint, local authorization state, and reported scopes. When the owning plugin declares a read-only identity probe, Connect also reads the account and workspace reported by the authorized service. Notion uses its official `notion-get-self` MCP tool; the card shows the returned account/workspace IDs, available names and email, and verification time. A returned bot is labeled as a bot, not the person who approved OAuth. The provider's OAuth application name is separate from this connection identity. See [Notion's tool reference](https://developers.notion.com/guides/mcp/mcp-supported-tools).

If identity is unavailable, the successful connection and authorization remain usable. Choose **Verify identity** to retry that read without logging in again; browsing the catalog or reading status never runs the probe. Reconnect clears the cached identity while retaining existing credentials until replacement succeeds. Older saved logins remain identity-unknown until an explicit Connect or identity verification. Names are display data, not permission or ownership authority. The resource URL identifies the remote service; it does not determine where the card appears or which personal/work domain owns the connection.

The optional [Notion workspace plugin](notion-workspace.md) is different: it uses a separately configured REST integration token and can open the Notion website. Website login does not grant either MCP or REST API access. Custom plugins can provide the same cards through the [connector metadata contract](plugin-authoring.md#mcp-connector-cards), without a separate connector registry.

## Add a server

Open **Plugins → MCP services → Add MCP**. Enter a server URL or a local executable with its arguments, or choose **Import JSON** to paste an `mcpServers` or `mcp_servers` object. Advanced options accept headers, environment variables, transport, OAuth registration values, and tool timeouts as JSON.

Saved definitions use the selected configuration provider and apply immediately when Wuu has no running turns or background work. Editing and removal check the configuration revision; reopen the editor if another window has changed it. Imports reject duplicate names and unsupported connection fields. Imported values are literal, including `${VAR}` placeholders.

You can also add definitions to `mcp_servers` in the user configuration, normally `~/.wuu/config.json`, and restart Wuu after editing on disk.

This fragment configures a local server and a remote one. Replace the executable, script path, and URL with your server's values:

```json
{
  "mcp_servers": {
    "project-tools": {
      "command": "node",
      "args": ["/absolute/path/mcp-server.js"],
      "env": { "PROJECT_ID": "demo" }
    },
    "docs": {
      "url": "https://mcp.example.com/mcp",
      "transport": "http"
    }
  }
}
```

For stdio, Wuu starts the executable directly with `args`, not through a shell. The child inherits the process environment, with `env` overriding named values. The server must reserve stdout for protocol messages and write logs to stderr.

For remote servers, `http` and `streamable-http` select streamable HTTP, while `sse` selects legacy SSE. Omitting transport enables HTTP probing with SSE fallback for an incompatible endpoint, not for an unreachable network. An explicitly selected transport does not fall back.

Each streamable HTTP POST response belongs to its call. Completing or cancelling
a call closes its response stream. If the stream ends before a result arrives,
or sends an invalid or oversized message, that call fails promptly. Wuu does not
replay a tool call after a broken stream because its effects may already have
occurred. Other calls and the server notification stream stay connected.
A POST response with another call's ID is rejected without resolving that other
call. Server-initiated requests use a separate ID namespace.

Replies to server-initiated requests use a separate bounded lane: up to four
sends and 32 waiting replies per connection, with a five-second budget including
queue time. A stalled reply does not block tool results or notifications.
Completing or cancelling the originating POST call cancels its queued and active
replies; a server cancellation targets the exact numeric or string request ID.
Disconnect cancels reply work and waits for the senders to stop. Failed or
excess replies fail their originating call without replaying it. An unsolicited
request flood that fills the queue closes the connection with a capacity error;
individual unsolicited send failures are logged. A reply rejected with an expired
session is not resent into a new session. Queued replies and authentication retries
stay bound to the session that received the request; replacing that session
invalidates them without sending them with the new session ID.

Remote `headers` are literal header values. Native `mcp_servers` does not expand `${VAR}` placeholders. Keep secrets out of committed configuration and do not assume a placeholder is a credential reference. `enabled: false` retains a definition without connecting at startup.

## Tool execution budgets

Set `tool_timeout_ms` on a server to allow longer reports, exports, or builds:

```json
{
  "mcp_servers": {
    "docs": {
      "url": "https://mcp.example.com/mcp",
      "transport": "http",
      "tool_timeout_ms": 120000
    }
  }
}
```

The value is a whole number of milliseconds from `1` to `3600000` (one hour).
Omit the field to keep the 30-second default. Zero, negative, fractional, null,
string, and out-of-range values are rejected; there is no unlimited mode.
The budget is a wall-clock limit for each tool call, including the wait for
response headers. Progress notifications do not extend it. An earlier caller
cancellation, turn deadline, or PTC deadline still wins. Timing out does not
prove that a remote operation stopped or had no effect; Wuu does not replay a
tool because it timed out.

This setting applies to native calls and calls nested in PTC. It is separate
from connection and tool-discovery budgets. Use the same field in a trusted
project `.mcp.json` server or a plugin's `mcpServers` / `mcp_servers` declaration.
A native preference entry containing only `tool_timeout_ms` (and optionally
`enabled` or `tool_overrides`) overrides the budget of a same-named project
server without replacing its definition. For a plugin server, use its qualified
name, for example `plugin.my-plugin.docs`.

Restart after editing settings on disk. A newly loaded plugin generation or
reconnection uses its loaded budget; calls retained by an older foreground
generation keep that generation's budget. The connection Refresh action does
not reread configuration files.

## Manage the connection

Open **Plugins** to manage standalone MCP services. A plugin's MCP services appear on that plugin's detail page; a plugin requiring MCP authorization also shows a notice in the catalog. Disabling the plugin disables its services. Plugin and project definitions retain their original source and are not editable as standalone definitions.

Connection controls show state, tool count, and errors. For entries in MCP services, Disconnect ends only the current connection without deleting its definition or credentials. Connector Disconnect instead signs out locally and persists the disabled preference, as described above. Refresh reconnects with the already-loaded configuration and discovers tools again. Edit changes the saved definition, including its startup preference. Remove deletes the standalone definition; a same-named project definition may become visible again. Removing a definition keeps its saved OAuth credentials; use **Remove OAuth login** first when you also want to sign out.

Restart Wuu after editing definitions on disk. Refresh is not a configuration-file reload. The older Settings MCP deep link remains available for compatibility; the main management entry is Plugins.

Tools receive model-visible names such as `mcp_docs_search`, with normalization for unsupported characters and long names. Large or infrequently used tool definitions can be deferred and found through tool search. The current tool surface and permission policy can still limit availability or execution.

### Session and notification recovery

For streamable HTTP servers, the optional GET notification stream follows the
initialized session. Session expiry starts one shared recovery handshake even
when several calls receive HTTP 404 together. A new session closes the old
listener, clears its resume cursor, and starts listening after initialization
completes. Queued notifications from obsolete sessions are discarded. HTTP 405
turns off GET listening only for that session.

With OAuth, a GET authentication failure refreshes credentials and retries once.
A later successful POST credential refresh can restart the listener while
preserving the same session's resume cursor. Disconnecting cancels listening and
any pending recovery. An unsuccessful recovery appears as a connection error;
use **Refresh** after resolving the reported problem.

Recovery does not replay a tool because its response stream broke: the tool may
already have taken effect. Wuu only retries requests explicitly rejected with
HTTP 401 or a session-expiry HTTP 404, within the authentication and recovery
limits. Other in-flight POST calls keep ownership of their own final responses.

## Project `.mcp.json`

Wuu reads Claude-style `.mcp.json` in the project configuration directory:

```json
{
  "mcpServers": {
    "local-docs": {
      "command": "node",
      "args": ["/absolute/path/docs-server.js"],
      "env": { "API_TOKEN": "${API_TOKEN}" }
    }
  }
}
```

Entries are not loaded by default. After reviewing the server, approve its name in your uncommitted `.wuu/settings.local.json`:

```json
{
  "mcp_json": { "enabled": ["local-docs"] }
}
```

`enable_all: true` enables all entries except those in `disabled`; explicit disabled names always win. Prefer individual names when only some servers are needed.

Unlike native definitions, `.mcp.json` expands `${VAR}` and `${VAR:-default}` in commands, arguments, environment values, URLs, and headers. Missing variables without a default remain literal and produce a warning. Supported types are `stdio`, `http`, and `sse`. A same-named native `mcp_servers` entry takes precedence.

## Remote OAuth

Remote URL servers can use OAuth discovery, PKCE, scopes, and dynamic client registration. An ordinary definition does not need an `oauth` section in advance: after an authentication challenge, choose **OAuth login** on the service.

Desktop Wuu opens the authorization page and listens on an HTTP loopback callback. After authorization it saves credentials and connects automatically. The listener uses an available port unless the definition specifies a fixed loopback redirect URI. The callback validates the state and stays bound to the initiating window and app-server across workspace changes. Canceling, closing the login dialog, closing the window, or reaching the ten-minute limit closes the listener and invalidates the pending attempt.

Browser clients and services with a non-loopback callback use manual completion: paste the callback URL or authorization code into the dialog. Fixed clients must register the actual callback URI with their service. Tokens go to Wuu's credential store rather than the server definition.

Saved credentials are bound to the resource URL used for login. Reusing a server name with a different URL requires another login; a path or query change also counts. Credentials from older versions without a saved resource URL require login again. Wuu keeps mismatched credentials until you explicitly replace or remove them. Logging in to the new resource replaces the credentials for that server name.

Logging out invalidates older login attempts and in-flight credential refreshes across Wuu processes. Their late responses cannot restore the removed credentials. Starting another login supersedes earlier login attempts for that server name; complete the newest authorization flow. Browser or token-exchange failures do not clear an existing credential. Signing in again after logout remains available.

Desktop MCP OAuth credentials remain in macOS Keychain or Linux Secret Service. Linux requires a running session D-Bus and an available, unlocked Secret Service keyring, such as GNOME Keyring. If the keyring is unavailable or access fails, Wuu reports the error and does not fall back to a credential file.

Owner-only lock files and credential-version records coordinate local access independently of project data directories; these files contain no tokens or client secrets. Keep these version records alongside your local Wuu state. Storage failures or a backend command that outlives a crashed Wuu process can make a credential unavailable and require another login; a stale or uncommitted credential is not accepted. All Wuu processes sharing credentials must use this version or newer for these guarantees.

Requests carrying OAuth credentials or configured headers cannot redirect across origins (scheme, host, or port). Legacy SSE message endpoints must share the stream's origin; same-origin paths and session queries remain supported.

If the service requires a fixed client, callback, or scopes, add an `oauth` object to that server:

```json
{
  "redirect_uri": "http://127.0.0.1:8765/callback",
  "client_id": "your-client-id",
  "scopes": ["tools:read"]
}
```

Use the service's actual registration values; a service may also require `client_secret`. Configured resource headers are not forwarded to a cross-origin authorization server.

## Tool metadata

A server definition can use `tool_overrides` to correct a tool's `read_only`, `concurrency_safe`, or `capability` metadata. Only set these when you know the operation's semantics. Mislabeling a write as a safe read can bypass protections that rely on those declarations.

## Troubleshooting and trust

If a server is absent, check configuration spelling, restart Wuu, and verify `.mcp.json` approval where applicable. For local connection errors, check executable discovery, dependencies, environment, and stdout discipline. For remote errors, check URL, transport, network, headers, and the server's OAuth requirements.

A connected server with tools may still have deferred definitions; ask the agent to search for the required tool. Server descriptions and results are external content, not system instructions. Local MCP processes run trusted code with their inherited environment, and remote services receive the arguments sent to them. Review the [security model](../reference/security-model.md) before connecting an unfamiliar service.
