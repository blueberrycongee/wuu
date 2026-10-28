# Independent provider author probes

These implementations import only `@wuu/plugin-sdk`. They do not inherit, wrap,
or call a built-in provider.

- `tools.ts` owns an immutable registry and the `author_echo` tool, including
  discovery, resolution, freeze/unfreeze, preparation, execution and finalization. It performs no OS work.
- `sessions.ts`, `journal.ts` and `turn-state.ts` own volatile session metadata,
  atomic history batches, provider checkpoints with retained physical history,
  fork copies, request-context checkpoints, control fences, held-input settlement and
  journal transitions. Unsupported operations fail explicitly.
  **This is not a complete production replacement or cross-process durable storage.**
- `prompt.ts` owns complete main/worker prompt content.
- `title.ts` returns a complete title without inference and owns title resolution.
- `compaction.ts` explicitly refuses transcript replacement and note generation;
  it exercises failure propagation and retained-history contracts.
- `shell.tsx` declares an independently selected `ui.navigation` surface;
  `navigation.tsx` renders it using immutable public state and `thread.select`.
  This custom shell does not demonstrate replacement of the default product's
  navigation, composer, conversation or settings implementations.

- `application.tsx` independently owns `ui.application` and `ui.shell`. It reads
  actual workspace/session listings and bounded visible history using only the SDK,
  keeps navigation state and a versioned checkpoint, and revokes consumer scopes
  on stop. Select both services from `independent-application`; do not combine this
  specialized explorer with default-product surfaces. Unsupported snapshot facets,
  auxiliary services and actions throw explicit errors. It neither starts models
  nor edits sessions. History stops before the latest completed turn and excludes
  that boundary turn. This demonstrates application replacement, **not feature
  parity with the default product**. Package tests use labeled fixture data; they
  are not evidence of production IPC routing or visual acceptance.

From the repository root, run:

```sh
node packages/plugin-sdk/test/parity-package.mjs
```

The script compiles and packs the public SDK into a temporary directory, compiles
these examples against that package, and tests provider behavior. It reuses the
existing SDK dependencies offline without installing or modifying them. These
checks do not prove production routing.

For production routing, provide an already built integrated Wuu executable:

```sh
WUU_PARITY_BINARY=/absolute/path/to/wuu \
WUU_PARITY_OUTPUT=/absolute/path/to/evidence \
node packages/plugin-sdk/test/parity-package.mjs
```

This additionally verifies an ordinary plugin tool under the default registry, then bundles the public-SDK tool provider, installs it in a temporary
synthetic home, excludes `wuu.tools.default`, and drives the real app-server with a
local synthetic HTTP model. It asserts the model sees only the independent tool,
receives its result, never launches the default MCP backend, shuts down its provider, and cannot initialize after the selected provider is removed.
No external model endpoint or existing user home is used. The output directory
contains protocol, model request and result JSON. The same run checks direct title persistence without inference and omission without
default title fallback, selects the independent complete prompt and session domain, and exercises
create/list/rename/model/history/fork/resume and real journal transitions, then
checks independent compaction and context-window refusals preserve prior completed
history without substituting a native policy. A refused `new_context` request
continues with the original history and the selected policy error. These are assertions to run, not a claim that
a particular binary passes. Finally it publishes a new tool generation while an old model request is paused,
then verifies retained old ownership, new-turn replacement and process cleanup.

To reuse a previously verified public SDK package, set
`WUU_PARITY_SDK_TARBALL` to its absolute `.tgz` path. This compiles the examples
against that package and records its path and SHA-256; it does not validate the
current SDK source build. Without this setting, the current SDK is built and packed.
`WUU_PARITY_SDK_ROOT` can select another checkout's SDK and compiler dependencies.

Each production run records the executable SHA-256 in `binary.json` and writes
separate scenario results only after their assertions pass. `result.json` reports
failure if any required scenario fails, including session receipt settlement.
Archive the evidence directory before rerunning if earlier results must be kept.

For the production renderer host/outlet contract (with jsdom, no visible GUI):

```sh
cd desktop
npm exec -- vitest run --config scripts/parity-ui.config.ts
```

This verifies explicit surface selection, public action dispatch, unavailable
provider behavior, subscription cleanup and stale handles. Application command
transport and rendered visual acceptance are separate checks.
