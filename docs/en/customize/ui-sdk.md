# Application shells and direct TSX preview

An application shell owns the layout, navigation, conversation, composer, activity,
settings and resource preview. Both the default shell and an external shell use
`ApplicationServiceV1` from `@wuu/plugin-sdk`: an immutable snapshot, a subscription,
and typed actions. A shell must not import desktop source, scrape another shell's
DOM, or maintain a second copy of application state.

## Start outside the repository

Use the SDK package matching your Wuu build. A maintainer can produce the package
with `npm pack ./packages/plugin-sdk`; copy that tarball to the author. No Wuu
source checkout is required in the author's directory:

```bash
mkdir my-research-ui
cd my-research-ui
npm install /path/to/wuu-plugin-sdk-0.1.0.tgz
```

Save this as `panel.tsx`:

```tsx
import type { PluginGenerationApi } from '@wuu/plugin-sdk';

export function activate(api: PluginGenerationApi) {
  const React = api.react;
  const { Button, Section } = api.ui;

  function Panel() {
    const state = React.useSyncExternalStore(
      api.application.subscribe,
      api.application.getSnapshot,
    );
    return <Section title="Research">
      <p>{state.conversation.title ?? 'No thread selected'}</p>
      <Button onClick={() => {
        void api.application.invoke('thread.create', {});
      }}>New thread</Button>
    </Section>;
  }

  api.registerViewType({ id: 'panel', title: 'Research', render: Panel });
}
```

Run `npx wuu-plugin-preview panel.tsx` and open its loopback URL. Saving the file
checks TypeScript and rebuilds the preview automatically. No plugin manifest,
plugin installation, or manual build is required. `--check` performs the same
public type check without starting a server. `--port` chooses a port; the default
uses an available port. Multiple source arguments load collaborating plugins.

The component picker lists registered views and declared shells. For an isolated
component, export `preview(api)` returning JSX. Define `const React = api.react`
inside activation or the preview function, and define components in that scope.
This classic JSX path uses the host React instance. Single-file preview supports
imports from `@wuu/plugin-sdk` and `@wuu/plugin-sdk/ui`; other dependencies need a
package build. Do not bundle a second React runtime into a desktop extension.

The preview uses synthetic application data and local actions. It makes no model
requests or persistent settings writes. An optional `--state snapshot.json` loads
an `ApplicationSnapshotV1` fixture at startup. Theme, text-size, and browser-width
controls help inspect layout. Syntax and type errors show the source file, line
and column and retain the last successful build. Activation errors retain the
previous preview; render errors show a component diagnostic. Fixing and saving
reloads the component. Local component state resets on reload; the preview's
application state is retained. Unsupported product APIs report an error.

Use [single-file runtime development](plugin-quickstart.md) for a `.ts` runtime
plugin that executes against Wuu services. Component preview is separate from
production inference, migration, native integration and persistence acceptance.

## Register a selected shell

A shell package declares its service explicitly in `plugin.json`:

```json
{
  "schema_version": 1,
  "id": "research-workbench",
  "name": "Research workbench",
  "version": "1.0.0",
  "desktop": {
    "entry": "dist/shell.js",
    "services": [{
      "name": "ui.shell",
      "version": "1.0.0",
      "methods": [{
        "name": "describe",
        "input_schema": "ui.shell.describe.input.v1",
        "output_schema": "ui.shell.describe.output.v1"
      }]
    }]
  }
}
```

Its module registers `api.registerApplicationShell({ id: 'ui.shell', render })`.
`render` receives `{ application, slots }`. The configured composition selects one
provider by plugin ID, provider ID and generation. Missing, disabled or failed
selected providers must expose their diagnostic rather than substitute a hidden
shell. For direct preview, export
`descriptor = { id: 'research-workbench', services: [APPLICATION_SHELL_SERVICE] }`;
this explicit declaration distinguishes a shell from a component file.

`application.getSnapshot()` retains object identity until a revision changes.
Use `api.react.useSyncExternalStore(application.subscribe,
application.getSnapshot)` to render it. Snapshots cover workspaces, navigation,
conversation items and activity, composer state, settings summaries, appearance,
preview and status. Credentials are excluded. Actions such as `thread.select`,
`thread.create`, `composer.set-draft`, `composer.submit`, `composer.stop`,
`appearance.set` and `preview.open` validate their payload types. Surface errors
from rejected actions; do not retry submissions automatically. A disposed
consumer generation cannot start a new action or accept a late result.

## Nested slots and a third plugin

A shell declares its slot and mounts the public component:

```tsx
api.registerUISlot({ id: 'evidence', cardinality: 'many' });
// Inside the shell component:
const Slot = slots.Slot;
return <Slot name="research-workbench/evidence"
  context={{ threadId: application.getSnapshot().conversation.threadId }} />;
```

Another plugin calls `api.registerUIContribution` with its local `id`, that
qualified `target`, and a typed component. It receives its own generation-bound
`application`, `slots` and the owner's `context`. Contributions may declare and
mount further nested slots. Local IDs belong to the registering plugin. Multiple
contributors to a `one` slot are a conflict; `many` slots sort by order and stable
identity. Cycles fail visibly. Removing a contribution or unloading its generation
removes its owned UI; unloading a slot owner also removes that mounting point.
Disposing an old generation must not remove a successor's registrations.

The complete examples are
[research workbench](../../../examples/plugins/research-workbench/shell.tsx) and
[research evidence](../../../examples/plugins/research-evidence/evidence.tsx).
Copy both files into an independent directory with only the packed SDK installed,
then run:

```bash
npx wuu-plugin-preview --check shell.tsx evidence.tsx
npx wuu-plugin-preview shell.tsx evidence.tsx
```

The workbench implements thread navigation/create/rename/archive, conversation and
activity, send/stop, appearance settings and resource previews. The third plugin
captures visible answers and keeps per-thread working notes in its loaded
component; those notes are explicitly ephemeral. It also owns a nested details
slot. Both read the same application subscription and use public actions.
For distribution, the example package scripts optionally bundle a desktop entry;
the author preview does not require those scripts.

## Service errors

A runtime provider may throw
`new RuntimeHostServiceError({ code: 'state_conflict', message: 'revision changed' })`.
The JSONL response preserves the code and message, including across nested service
calls. An error with a nonempty string `code` follows the same transport contract;
other fields and stacks are not serialized. Ordinary exceptions have no domain
code. Consumers must classify the code rather than match message text. An
uncertain effect result is not permission to replay the effect.
