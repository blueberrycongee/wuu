# Extend Wuu

Choose an extension mechanism for the behavior you need. A reusable task procedure can be a skill; an existing tool service can connect through MCP. Use a plugin when you need managed code, host services, or desktop UI.

| Goal | Start here |
|---|---|
| Reuse a task workflow | [Skills](skills.md) and [skill authoring](skill-authoring.md) |
| Connect a local or remote tool server | [MCP](mcp.md) |
| Run checks around lifecycle events | [Hooks](hooks.md) |
| Install packaged agent or desktop behavior | [Wuu plugins](plugins.md) |
| Choose a theme or configure a plugin | [Themes and settings](themes-settings.md) |
| Keep durable information | [Memory](memory.md) and optional [Dream](dream.md) |

Skills add instructions and resources. MCP exposes tools from another process or service. Hooks run commands or model checks at supported events. A plugin can combine these contributions with agent code, desktop code, themes, and settings under one package lifecycle.

## Build an extension

Start with the [agent plugin quickstart](plugin-quickstart.md) for model-visible tools or runtime behavior, or the [desktop plugin quickstart](desktop-plugin-quickstart.md) for interface contributions. The [desktop extension guide](desktop-plugins.md) explains the available UI boundaries, with practical examples in [recipes](plugin-recipes.md).

Use the [authoring reference](plugin-authoring.md) for package fields and APIs, and the [system architecture](plugin-system.md) for loading, lifecycle, and compatibility boundaries. You do not need to read the architecture reference before writing a local extension.

## Understand the trust involved

A skill can influence tool use even though it is text. MCP services and hooks can execute code or send data elsewhere. Agent plugins run in managed processes, and desktop plugins run trusted code in the renderer. Wuu does not sandbox or certify installed extensions.

Agent permission modes apply to supported tool execution paths, not to all arbitrary code you install. Review the source and data access of an extension before using it; see the [security model](../reference/security-model.md).
