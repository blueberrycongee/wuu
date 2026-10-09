# Add contextual command actions

A desktop plugin can place a registered command in a view's title bar or beside a conversation message. The host renders the button, manages its placement, and passes a public context to the command. These actions add controls; they do not replace the view shell, message renderer, or native approval controls.

## Register a placement

Use `registerCommand` with `placements`:

| Placement | Context available to `when`, `enabled`, and a click's `execute` |
| --- | --- |
| `view.title` | `contractVersion: 1`, `target`, `viewId`, `viewTypeId`, `viewPluginId`, `region` |
| `conversation.message.actions` | `contractVersion: 1`, `target`, optional `threadId`, `turnId`, and `item: ConversationItemSnapshotV1` |

`PluginCommandActionContext` is the SDK's discriminated union. Check `context.target` before reading fields belonging to a placement. Message context is the public snapshot of the clicked message, not a selection range, the complete conversation, or a private thread record.

- `title` labels the action; optional `icon` uses a public semantic icon name.
- `order` controls ordering among plugin actions.
- `when(context)` controls visibility. Filter on both `viewPluginId` and `viewTypeId` when an action belongs to your view.
- `enabled(context)` controls whether the visible action can run. For example, disable capture while message text is streaming or empty.
- Predicates are synchronous and side-effect-free; return a boolean. Context snapshots are immutable. A failing predicate is diagnosed rather than allowed to break the host action row.
- The host checks visibility and enabled state again when the action is invoked. They describe UI availability, not new security permissions.

A command without `placements` keeps its existing behavior. Its normal command-palette or direct invocation can still have no input, or an input of another shape. `execute(input?: unknown)` must validate context instead of casting blindly. Do not infer the current thread from global state when a message context already identifies the target.

## Try the action-notes example

The [Action Notes example](../../../examples/plugins/action-notes/README.md) is a desktop-only plugin with two actions:

1. **Refresh action notes** appears only on its own notes view. It republishes that view's snapshot and increments a refresh counter.
2. **Save to action notes** copies the clicked message's displayed text into a plugin-owned temporary list. Repeating the action on the same message does not add a duplicate.

The view is available through Workspace Tools after you explicitly load the plugin. The example does not automatically open a view, enable itself, read other messages, use credentials, or send data over the network. Its notes are memory-only and disappear on reload or disable. For durable notes, use the existing plugin storage contract and keep one authoritative store; see [plugin recipes](plugin-recipes.md).

## Lifecycle and recovery

Commands and their placed buttons belong to the activating plugin generation. The registration returns a disposable. Disposing it, unloading, or disabling that generation removes its contribution; an obsolete button cannot invoke a replacement command merely because the command ID matches.

Use `registerCleanup` for plugin-owned subscriptions, timers, and other resources. React subscriptions should return an unsubscribe function. Keep asynchronous work generation-scoped and ignore late results after disposal. The example uses `useSyncExternalStore` with the host's React runtime and closes its local store on cleanup.

Development reload replaces registrations with the new generation. A successful reload resets this example's temporary notes and refresh counter. Failed publication keeps the last published generation; a desktop activation error is reported separately in plugin status. Do not depend on a reload to preserve module variables.

## Verify the interaction

Build and run the example's contract test, then load it explicitly with `wuu plugin dev`. In Desktop, open Action notes, save a synthetic message, refresh the view, and verify no refresh action appears on another plugin's view. Check empty/streaming messages, keyboard operation, narrow layout, both themes, and disable/reload cleanup.

The automated example test checks command behavior and cleanup using synthetic data. It does not render Desktop or establish visual accessibility acceptance. `wuu plugin test` validates a desktop-only package without rendering its entry point.
