---
name: wuu-plugin-development
description: Design, implement, debug, or review Wuu runtime plugins, desktop contributions, or replaceable plugin providers in this repository. Use when changing plugin behavior or public plugin contracts.
---

# Wuu plugin development

Inspect the affected plugin and current SDK before changing behavior. When the repository root is within the workspace's read scope, read the [plugin development practices](../../../docs/en/customize/plugin-development-practices.md). If a reference is unavailable, use the guidance below and report any contract you could not verify. Do not expand workspace permissions just to load a reference. The practices are guidance, not a new validation gate. Existing API and protocol contracts still apply.

Use the smallest public extension point that fits. Distinguish adding a contribution from selecting a replacement provider. Check the relevant reference rather than assuming another framework's APIs apply:

- Runtime tools, capabilities, services, and lifecycle: [plugin authoring](../../../docs/en/customize/plugin-authoring.md).
- Desktop contributions and cleanup: [desktop plugins](../../../docs/en/customize/desktop-plugins.md) and, when needed, [UI SDK](../../../docs/en/customize/ui-sdk.md).
- Provider selection or version changes: [replaceable providers](../../../docs/en/customize/replaceable-providers.md) and [plugin compatibility](../../../docs/en/customize/plugin-compatibility.md).
- Single-file development or package setup: [agent plugin quickstart](../../../docs/en/customize/plugin-quickstart.md).

## Apply the practices to the task

- Keep declarations in `initialize`, state reads and validation in `prepare`, and active work in `activate`. Stop owned work in `shutdown`, including after partial activation. Use the invocation's scoped host and cancellation signal; clean up desktop resources on unmount.
- Report failed tool operations with `is_error`, preserve useful service error codes, and choose capability `error_policy` according to whether failure can safely be isolated. Do not silently allow an action when a required safety check fails. Preserve cancellation and report uncertain external effects without assuming rollback or retry safety. Keep JSONL stdout for protocol frames and diagnostics on stderr.
- Declare actual dependencies and effects. Use public service and UI contracts, preserve other contributions, and protect credentials and user data. Check the relevant SDK types and host implementation before relying on a behavior.
- Match verification to the change. Package validation and initialization checks do not prove a tool result or rendered view. Exercise the affected behavior and, where relevant, failure, cancellation, reload, or disable; report what was actually verified.

For a design-only or review-only request, report findings without changing the plugin. If the user also requests implementation, carry it out within the requested scope. Do not introduce new enforcement checks solely for these recommendations unless the user requests them. Follow the repository's authorization rules and applicable GUI-test instructions.
