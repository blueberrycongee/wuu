# Plugin development practices

These recommendations help people and coding agents build plugins that work with Wuu and other extensions. Apply them according to the plugin's purpose. They do not add admission checks or a new enforcement policy. Existing API and protocol requirements still apply; use the [authoring reference](plugin-authoring.md), [desktop plugin guide](desktop-plugins.md), and [compatibility reference](plugin-compatibility.md) for their current contracts.

## Choose how to extend Wuu

- Use the smallest public extension point that fits. Add a tool, service, or UI contribution before replacing a complete provider.
- Use `@wuu/plugin-sdk` and documented host services instead of another plugin's private files, process, or renderer state. An additive plugin can consume a service without replacing its provider.
- Declare runtime services in `required_services` and desktop services in `desktop.required_services`. Package dependencies select packages and their activation order; service requirements select an API name and major version. Declare both when you need both.
- Give optional dependencies an explicit unavailable path. Keep unrelated features usable and report the affected feature's failure.
- Keep package, service, and capability identifiers stable across compatible updates. Product version, package version, protocol version, and service major are separate compatibility decisions.

## Own work for its lifetime

- Keep `initialize` for declarations. Read and validate state in `prepare`; start timers, subscriptions, and other active work in `activate`. Preparation methods have the restrictions described in the [lifecycle reference](plugin-authoring.md).
- Stop plugin-owned work in `shutdown`, including after partial preparation or activation. Clean up desktop listeners, subscriptions, and timers when their view or generation ends.
- Pass `execution.signal` to work started by a tool, capability, or service invocation. Use `lifecycle.signal` for runtime background work and `api.signal` for desktop generation work. Cancellation is cooperative and cannot undo a completed effect.
- Use the handler's scoped `host` for its work. Avoid storing one invocation's host, execution ID, or signal in shared state for another invocation.
- Design activation and cleanup for reload and disable. An ordinary update can leave an earlier generation serving in-flight work, and a failure after commit does not roll back external effects.

## Report failures where they matter

- For a failed tool operation, return a tool result with `is_error: true` and content that explains the failure. Do not return success after a required operation fails. Preserve cancellation as cancellation instead of reporting success or retrying it. Tool result content can be seen by the model.
- When a service caller needs to distinguish expected failures, use documented stable error codes and keep the display message separate. Preserve useful host error codes, such as those carried by `RuntimeHostServiceError`; do not make decisions by parsing message text.
- Pick a capability's `error_policy` for its role. Observation failures should not stop the observed work; use `isolate` when a diagnostic is useful, and `ignore` only when continuing without a diagnostic is acceptable. For decisions or transforms, consider whether continuing without the contribution is safe. If a required authorization or safety check fails, report the failure instead of silently allowing the action. Follow the restrictions on each capability in the authoring reference.
- Report an action failure at the affected tool, service, or UI. Include the operation, its impact, and a useful next step when known. Keep tokens, credentials, conversation content, and private file contents out of errors and diagnostics by default.
- If a write times out or fails after it may have taken effect, report the outcome as unknown. Check the current state before retrying when the operation supports that check; do not claim rollback without evidence.
- For a JSONL runtime, reserve stdout for protocol frames. Send diagnostics to stderr. Handle rejected background promises so failures remain attributable to the plugin.

## Protect inputs and state

- Validate tool arguments and external data at the point of use, including constraints the declared schema does not express. Declare a tool's real effects and concurrency behavior in `activity`; opt into concurrent execution only when shared state is safe.
- Use the callback's execution context for session and workspace identity. Do not substitute the currently selected conversation for a missing identity, or treat a UI snapshot as authority for an action.
- Keep durable plugin state in the appropriate host storage or service. For concurrent updates to one storage value, use `host.storage.compare_exchange`, check the result, and bound retries. Preserve user data when disabling or reloading a plugin.
- Declare non-secret settings with an appropriate `live`, `next-operation`, or `restart` application mode. Capture a snapshot when an operation needs related settings to stay together. Keep coupled values in one scope; a combined user and workspace snapshot is not a cross-scope transaction.
- Use the host credential service for supported authenticated requests. Do not put secrets in tool arguments, ordinary settings or storage, logs, or packaged files. Plugin code is trusted and runs with user authority; requested permissions do not sandbox it.

## Compose desktop contributions

- Use the host's React instance, UI Kit, theme tokens, and localized text. Prefer a declared additive slot for a small control or annotation over replacing the whole composer, conversation, or shell.
- Treat snapshots as read-only. A wrapper that does not handle an item should preserve its `children`; a presenter can delegate with `next()`. Avoid private DOM selectors and internal React state.
- Handle asynchronous action failures in the affected view. Keep a failed optional contribution from presenting unrelated work as failed, and release view-owned resources on unmount.

## Check the behavior you changed

- For a packaged plugin, use `wuu plugin validate` for package structure and `wuu plugin test` for initialization and negotiation. These checks do not prove a tool result or desktop view works.
- Exercise the changed feature in a real conversation or desktop view as appropriate. For UI changes, inspect the rendered result rather than relying on registration alone.
- When relevant, check a failed operation, cancellation, disable, and reload. For persistent or external writes, check the state after failure and avoid repeating side effects whose outcome is unknown.
- For a local runtime, the [agent quickstart](plugin-quickstart.md) also describes the single-file development path. Report what you verified and any behavior you could not exercise.
