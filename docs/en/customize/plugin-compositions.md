# Agent compositions

`host.composition` version 1 lets a trusted plugin create an independent Go runtime, try a harness selection, and explicitly bind logical agents to it. It uses the same versioned services, dependency graph, activation checks, and reverse dependency cleanup as the main runtime. The [composition lab](../../../examples/plugins/composition-lab.ts) exposes these operations as an agent tool and a developer client capability.

```bash
wuu plugin dev examples/plugins/composition-lab.ts
```

A plugin declares `requireCompositionService()` in `required_services` and calls `compositionService(host)` with the current callback's host. This requires the `kernel` module. The service is unavailable during preparation. It adds no manifest or build requirement to single-file TypeScript development.

For a fixed task list, independent grading, process isolation and retained results, use [local harness experiments](harness-experiments.md). A fresh composition run alone does not define a benchmark.

## Try, select, and undo

```ts
const scopes = compositionService(host);
const baseline = await scopes.create({
  selection: { version: 2, profile: "headless" },
});
const candidate = await scopes.create({
  paths: ["./my-loop"],
  selection: {
    version: 2,
    profile: "headless",
    patches: [{
      modules: { "wuu.default-loop": false, "example.my-loop": true },
      services: [{ service: "driver.default", major: 2, provider: "example.my-loop" }],
    }],
  },
});
const trial = await scopes.run({ id: candidate.id, prompt: "Check this harness." });
if (trial.error) throw new Error(trial.error);

await scopes.bind("writer", baseline.id, "");
const changed = await scopes.bind("writer", candidate.id, baseline.id);
await scopes.run({ agent: "writer", prompt: "Use the selected harness." });
await scopes.bind("writer", changed.previous, candidate.id); // undo selection
await scopes.dispose(candidate.id);
await scopes.dispose(baseline.id);
```

`example.my-loop` must actually provide `driver.default` major 2. Other replacements use their own advertised contracts, such as `agent.prompt` major 1. Include required provider modules explicitly. Missing dependencies, ambiguous providers, failed activation, and unavailable runtime capabilities reject creation; they never select a substitute or change an existing binding. See [composition configuration](plugin-system.md) for profiles, bundles, and patches.

`paths` accepts directories, zip packages, and individual `.ts` files. Installation grants trust only in the new composition. The catalog includes the owning generation's active packages, but only selected modules start. Sources are copied into execution snapshots; editing the source after creation cannot change an existing scope. A single-file module ID is `local-` followed by the first 24 hexadecimal characters of SHA-256 of its absolute source path, matching `wuu plugin dev`. For a file you can compute this with Node's `createHash("sha256").update(resolve(path)).digest("hex").slice(0, 24)`. Package directories use their manifest ID. Creation returns active package IDs and fingerprints for inspection.

Use `create({ inherit: true })` to copy the parent generation’s actual module set and service bindings. You can supply `selection` with module, service, and patch overrides; a profile is mutually exclusive with inheritance because it starts its own selection. The result reports `parent_generation` and the effective `model`. Model configuration is copied, and durable state is isolated by default:

```ts
const scoped = await scopes.create({
  inherit: true,
  model: { provider: "research", model: "my-model", effort: "high" },
});
```

Omitting `model` preserves the captured parent choice. An explicit model object uses the selected connection’s defaults for omitted fields. Native embedding providers are constructed again using the candidate’s own provider context. This also applies to nested scopes: an isolated descendant of a scope that imports parent state reconstructs the selected state providers in its own context. A nested `state: "parent"` scope shares that same captured state authority and can bind its product conversations.

## Product conversations

Product session bindings support the native Wuu engine only. Binding an external-engine session is rejected. If a bound session later selects an external engine, new turns fail before dispatch; explicitly unbind its composition first.

A scope with `state: "parent"` imports the parent's selected session, document and artifact services. This explicit import overrides state-service selection patches. The service graph, model configuration and plugin processes remain separate. Bind an existing product conversation using its session ID:

```ts
const candidate = await scopes.create({
  inherit: true,
  state: "parent",
  model: { provider: "research", model: "my-model" },
});
const previous = await scopes.inspectSession(sessionID);
await scopes.bindSession(sessionID, candidate.id, previous.id);
// Future product turns use candidate; an admitted turn keeps its captured graph.
await scopes.bindSession(sessionID, "", candidate.id); // restore workspace selection
await scopes.dispose(candidate.id);
```

The candidate's model takes precedence over the conversation's saved model default; conversation permission and speed choices remain in force. Product tools use the conversation's workspace. A child created by an admitted parent snapshots that parent's composition; later edits to the parent do not redirect the child. The bundled subagent uses this contract for foreground work and background completion.

A side conversation captures the running parent's admitted composition, or the current durable selection when the parent is idle. It retains that scope through its own completion, including when the parent finishes, rebinds or disposes the scope. Its tools use the parent's workspace and its native toolkit remains read-only. The selected graph's step and request policies also apply. Missing compositions or explicit models fail admission instead of falling back to workspace defaults.

A background lifecycle callback returns to the plugin instance that sent the request, even after the parent turn ends or its next turn selects another composition. Admitted child execution and callback delivery keep that origin alive through disposal; its private resources drain afterward. Request IDs are plugin-wide within the shared state authority: reusing one from another composition or for another target session fails before admission. Terminal observations remain durable if their origin is unavailable after restart; replay cannot deliver them to the default plugin instance.

Product selection is authoritative in `state.documents` and survives disposal, generation retirement and process restart. Runtime resources are temporary. If they are missing or their parent state generation has changed, new turns fail before inference. `inspectSession` returns `default`, `active`, or `unavailable`, the selected ID, selecting caller, and the live composition or saved binding snapshot. Explicitly rebind a newly created scope, or unbind using the old ID. Storage errors and unknown commit outcomes propagate; inspect before retrying a write. An isolated scope cannot bind a product session.

## Admission and ownership

| Method | Effect |
| --- | --- |
| `create({selection?, inherit?, model?, paths?, state?, root?, driver_profile?})` | Prepare and activate a candidate with isolated state by default; return its ID, workspace, selection, modules, and packages. |
| `inspect(id)` | Read the live modules, selected agents, and running execution count. |
| `bind(agent, id, expected)` | Compare and exchange the current binding. `expected: ""` means unbound; `id: ""` unbinds. A mismatch returns `binding_conflict`. |
| `bindSession(session, id, expected)` | Persist a product conversation's selection with compare-and-exchange. |
| `inspectSession(session)` | Read persisted selection and current resource availability. |
| `run({id, prompt})` | Try a composition without selecting it. |
| `run({agent, prompt})` | Capture the agent's current composition and start a fresh execution. Returns `id`, `execution_id`, `content`, and optional inference `error`. |
| `dispose(id)` | Unbind logical agents and stop new admissions. Product selections retain the unavailable ID. Return `draining: true` while admitted runs finish; release providers and temporary state after the last run. |

The bind/run boundary is atomic. Runs admitted before a switch retain their original provider graph and checkpoint identity. Selecting an earlier ID restores future selection; it does not reverse completed effects. Keep the old scope alive if undo is needed.

IDs and agent names belong to the calling plugin, conversation, and owning generation. The registry supplies that identity; inputs cannot choose another caller or conversation. Developer client callbacks without a conversation have a separate namespace. Each trial run has a fresh session and its own driver binding. Product bindings select future turns of existing conversations; they do not migrate an admitted checkpoint to a replacement driver. Logical agent bindings are temporary; product bindings persist as described above.

Caller cancellation propagates into a run. Plugin retirement cancels its scopes' main turns, native workers and direct model invocations; generation shutdown also cleans abandoned scopes. Explicit disposal lets admitted work drain. Consumer cleanup and inference accounting precede provider shutdown. A cleanup failure is reported and retains the temporary directory for diagnosis.

A candidate has its composition identity before plugin activation. Activation may inspect captured state, but execution admission remains unavailable until creation publishes the ready composition. Failed preparation cannot attribute candidate work to the default runtime.

Run `WUU_E2E_CORE=/path/to/wuu node scripts/composition-e2e.mjs` against a product-version build to verify concurrent scopes, model routing, native file tools, failed preparation, binding conflicts, disposal during an admitted run, foreground child inheritance, side conversations across rebinding and disposal, background delivery across rebinding and disposal, resource drainage, and product selection after process restart. It saves requests, app-server messages, diagnostics, and results under `.tmp/e2e/composition/run-*`. Inference is synthetic; the check does not establish model quality or rendered UI behavior.

## Isolation boundary

With the default `state: "isolated"`, a scope gets an independent state directory, in-memory configuration provider, service graph, plugin processes, and default empty workspace. Creation copies the owning generation's model and runtime configuration. Process-wide model metadata is initialized once by the host; creating a scope does not replace it or start another refresh. It never installs into the global plugin catalog or changes another session's configuration. Supply `root` only when file sharing is intended; relative sources and explicit roots resolve against the owner runtime's workspace.

Extensions remain trusted code. This is lifecycle and state isolation, not an OS security sandbox. Explicitly shared directories, OS credentials, configured external services, and effects performed by plugins can still be shared. Persistent provider replacement and checkpoint migration require their own compatible protocol or stopped-writer migration. These temporary scopes do not perform such a migration.

## Discover the admitted service graph

`registry.introspect` returns `graph_generation`, the immutable runtime identity,
alongside the existing durable catalog `generation` epoch. Retained executions
and concurrent candidates can share a catalog epoch without sharing a graph.
Each service entry includes its full `descriptor`: method schemas and preparation
and cleanup admission flags. `requirements` includes unresolved declarations;
`dependencies` is the same provider-before-consumer graph used for activation
and reverse-order cleanup, including service major versions. Retirement removes
callable providers but retains dependency edges for diagnosis.

Composition `create` and `inspect` return this snapshot in `services`. Use it to
record the implementation actually loaded, rather than reconstructing provider
choices from a newer catalog or treating a profile name as execution identity.
