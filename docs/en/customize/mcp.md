# MCP servers

MCP (Model Context Protocol) lets Wuu connect to local or remote tool servers. Once
connected, the tools the server provides join the agent's tool set; when there are
many tools, Wuu defers displaying low-frequency tools, and the agent can still search
and call them on demand.

The current integration surface only discovers and calls MCP tools; there is no
user-facing browsing entry for MCP resources or prompts.

MCP tools add context and external access scope. Only connect to trusted servers, and
only enable the tools the current work actually needs.

## Configure servers

The desktop settings can manage existing servers, but cannot currently create or edit
server definitions. Add the definition to `mcp_servers` in the user configuration
`~/.wuu/config.json`, then restart Wuu.

### Local stdio server

```json
{
  "mcp_servers": {
    "project-tools": {
      "command": "npx",
      "args": ["-y", "@example/project-mcp"],
      "env": {
        "PROJECT_ID": "demo"
      }
    }
  }
}
```

Wuu starts `command` as a child process and uses MCP over stdin/stdout. Do not add
commands provided by untrusted repositories directly to the user configuration. `args`
is passed to the program directly without going through a shell; `env` only applies to
the stdio child process and overrides same-named variables in the Wuu process.

### Remote server

```json
{
  "mcp_servers": {
    "docs": {
      "url": "https://mcp.example.com/mcp",
      "transport": "http",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      }
    }
  }
}
```

`transport` can be `http`, `streamable-http`, or `sse` for legacy servers. When
omitted, streamable HTTP is tried first; if protocol probing and the legacy
initialization POST indicate the endpoint does not support it, it falls back to SSE.

Wuu negotiates the supported protocol automatically. Tool results requiring an
additional user-input exchange are not currently supported and report an error.

`headers` only apply to remote requests. Native `mcp_servers` does not expand
`${VAR}`, so values such as the token in the example are used literally; do not write
secrets into team-shared project configuration.

Each server can also set `enabled`. `false` keeps the definition but does not connect;
the desktop toggle writes this choice back to the configuration.

## Manage in the desktop

Open **Settings → General → MCP servers**. Each row shows the connection state and the
number of discovered tools, and provides:

- turning the server on or off;
- connecting or disconnecting;
- refreshing the connection and tool list;
- logging in or removing login for remote servers that need OAuth;
- viewing connection errors.

The enable/disable toggle saves the `enabled` configuration, but the current
connection is not guaranteed to start or end immediately with the toggle; when you
need an immediate change, use the connect/disconnect button beside it, or restart Wuu.
"Disconnect" only ends the current connection; it does not delete the definition.
After editing the config file on disk, restarting Wuu is the most reliable; refresh
only reconnects and rediscovers tools with the already-loaded server configuration,
and does not re-read the configuration file.

## Use tools

After connecting, just tell the agent to use the service, for example:

```text
Use the docs MCP to find the latest documentation for this API, then give a
conclusion with sources.
```

The model-visible name follows `mcp_<server>_<tool>`; incompatible characters are
replaced, and over-long names are truncated with a hash appended. Ordinary tasks do
not need to memorize the full names; describe the server name and the goal instead.

MCP tools still respect the current tool surface, permission mode, and local policy.
Descriptions and returned metadata from the server are treated as untrusted external
content; they do not become Wuu system instructions.

## Use a project `.mcp.json`

Wuu is compatible with the Claude Code style `.mcp.json` at the repository root:

```json
{
  "mcpServers": {
    "local-docs": {
      "command": "node",
      "args": ["scripts/mcp-server.js"],
      "env": {
        "API_TOKEN": "${API_TOKEN}"
      }
    }
  }
}
```

Servers in project files are **not loaded by default**, because a stdio definition can
execute a program specified by the repository. Approve them one by one in
`.wuu/settings.local.json`, which is not committed:

```json
{
  "mcp_json": {
    "enabled": ["local-docs"]
  }
}
```

You can also approve everything with `enable_all: true` and reject individual names
with `disabled`. `disabled` always wins:

```json
{
  "mcp_json": {
    "enable_all": true,
    "disabled": ["unsafe-server"]
  }
}
```

`.mcp.json` supports `stdio`, `http`, and `sse`, and expands `${VAR}` and
`${VAR:-default}` in command, args, env, URL, and headers. A missing environment
variable produces a stderr warning. If it uses the same name as a native
`mcp_servers` definition, the native configuration wins.

## OAuth

OAuth only applies to remote URL servers. Wuu discovers protected resources,
authorization servers, PKCE, scopes, and dynamic client registration information from
the server's `WWW-Authenticate` response and standard well-known metadata. Ordinary
remote definitions do not need `oauth` in advance; after the server returns 401, you
can start the login directly from the settings page.

The current desktop flow still requires you to paste the authorization code back
manually; there is no corresponding CLI login command or automatic callback listener.
The default callback address is `http://127.0.0.1/callback`. When the service requires
a fixed client, a special callback address, or specific scopes, you can add `oauth` to
override the discovery result:

```json
{
  "mcp_servers": {
    "issues": {
      "url": "https://mcp.example.com/mcp",
      "transport": "http",
      "oauth": {
        "redirect_uri": "http://127.0.0.1:8765/callback",
        "client_id": "YOUR_CLIENT_ID",
        "client_secret": "YOUR_CLIENT_SECRET",
        "scopes": ["tools:read", "tools:execute"]
      }
    }
  }
}
```

If `client_id` is omitted, Wuu tries the dynamic client registration endpoint the
server publishes. After choosing login in the desktop, the authorization address
opens; once authorized, paste the code from the callback back into the settings page
to finish login. Tokens are saved by Wuu's credential store and are not written back
to the server configuration. Custom headers in the configuration are only sent to the
MCP resource endpoint and same-origin discovery endpoints, and are not forwarded to a
cross-origin authorization server.

## Tool metadata overrides

Only use `tool_overrides` to correct the server's declarations when you trust and
understand the server tool semantics:

```json
{
  "mcp_servers": {
    "docs": {
      "url": "https://mcp.example.com/mcp",
      "tool_overrides": {
        "search": {
          "read_only": true,
          "concurrency_safe": true,
          "capability": "search.semantic"
        }
      }
    }
  }
}
```

Marking a write operation as read-only or concurrency-safe by mistake can bypass the
serialization and permission protections that should apply. Do not guess these values
from the tool name alone.

## Troubleshooting

### No server in settings

Confirm the definition is under `mcp_servers`, the JSON fields are not misspelled, and
restart Wuu. A project `.mcp.json` must also pass the `mcp_json` approval; unapproved
entries only produce a stderr notice and do not appear in the configured list.

### Local server connection fails

In the same environment, confirm `command` is executable, dependencies are installed,
and the server writes protocol messages to stdout and ordinary logs to stderr.

### Remote server fails

Check the URL, transport, headers, and network proxy. Legacy SSE endpoints should
explicitly use `sse`; modern endpoints prefer `http`.

### Authentication or registration needed

Confirm the server publishes OAuth discovery metadata in the 401 response or at the
standard well-known address. When dynamic registration fails, use the `client_id` and
`client_secret` provided by the service in `oauth`; when the service requires a
specific callback address, configure `redirect_uri` as well.

### Tools do not appear to the model

First confirm the state is "connected" and the tool count is greater than zero. Large
numbers of tools or very large tools may be deferred by Wuu, and the agent can
discover them on demand with tool search; tools incompatible with the current session's
tool surface are not exposed.
