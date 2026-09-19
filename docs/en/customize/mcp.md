# MCP servers

MCP connects the Wuu engine to tools provided by a local process or remote service. After connecting, ask the agent to use the server by name and describe the task. Wuu discovers and calls tools; it does not currently provide a resource or prompt browser, and MCP requests for an additional user-input exchange are unsupported.

## Add a server

Add definitions to `mcp_servers` in the user configuration, normally `~/.wuu/config.json`, and restart Wuu. The desktop manages existing definitions but does not provide a definition editor.

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

Remote `headers` are literal header values. Native `mcp_servers` does not expand `${VAR}` placeholders. Keep secrets out of committed configuration and do not assume a placeholder is a credential reference. `enabled: false` retains a definition without connecting at startup.

## Manage the connection

Open **Settings → General → MCP servers** to see connection state, tool count, and errors. Connect, disconnect, or refresh a server there. Disconnect ends the current connection without deleting its definition; refresh reconnects with the already-loaded configuration and discovers tools again.

The enable toggle saves the startup preference. Use the connection controls when you need an immediate connection change, and restart Wuu after editing definitions on disk. Refresh is not a configuration-file reload.

Tools receive model-visible names such as `mcp_docs_search`, with normalization for unsupported characters and long names. Large or infrequently used tool definitions can be deferred and found through tool search. The current tool surface and permission policy can still limit availability or execution.

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

Remote URL servers can use OAuth discovery, PKCE, scopes, and dynamic client registration. An ordinary definition does not need an `oauth` section in advance: after an authentication challenge, use the login action in settings.

The current desktop flow opens the authorization URL and requires you to paste the returned authorization code into settings. It does not start an automatic callback listener. Tokens go to Wuu's credential store rather than the server definition.

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
