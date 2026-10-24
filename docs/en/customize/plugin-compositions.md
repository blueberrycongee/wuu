# Isolated agent compositions

`host.composition` version 1 lets a trusted plugin create an independent Go runtime, try a harness selection, and explicitly bind logical agents to it. It uses the same versioned services, dependency graph, activation checks, and reverse dependency cleanup as the main runtime. The [composition lab](../../../examples/plugins/composition-lab.ts) exposes these operations as an agent tool and a developer client capability.

```bash
wuu plugin dev examples/plugins/composition-lab.ts
```

A plugin declares `requireCompositionService()` in `required_services` and calls `compositionService(host)` with the current callback's host. This requires the `kernel` module. The service is unavailable during preparation. It adds no manifest or build requirement to single-file TypeScript development.

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

## Admission and ownership

| Method | Effect |
| --- | --- |
| `create({selection, paths?, root?, driver_profile?})` | Prepare and activate a candidate in a fresh state namespace; return its ID, workspace, selection, modules, and packages. |
| `inspect(id)` | Read the live modules, selected agents, and running execution count. |
| `bind(agent, id, expected)` | Compare and exchange the current binding. `expected: ""` means unbound; `id: ""` unbinds. A mismatch returns `binding_conflict`. |
| `run({id, prompt})` | Try a composition without selecting it. |
| `run({agent, prompt})` | Capture the agent's current composition and start a fresh execution. Returns `id`, `execution_id`, `content`, and optional inference `error`. |
| `dispose(id)` | Unbind that composition and stop new admissions. Return `draining: true` while admitted runs finish; release providers and temporary state after the last run. |

The bind/run boundary is atomic. Runs admitted before a switch retain their original provider graph and checkpoint identity. Selecting an earlier ID restores future selection; it does not reverse completed effects. Keep the old scope alive if undo is needed.

IDs and agent names belong to the calling plugin, conversation, and owning generation. The registry supplies that identity; inputs cannot choose another caller or conversation. Developer client callbacks without a conversation have a separate namespace. Each run has a fresh session and its own driver binding; this API does not migrate existing desktop conversations or resume a checkpoint under a replacement driver. Scope IDs and bindings are temporary and do not survive the owning generation's retirement or process restart.

Caller cancellation propagates into a run. Plugin retirement cancels its remaining work and closes its scopes; generation shutdown also cleans abandoned scopes. Consumer cleanup precedes provider shutdown. A cleanup failure is reported and retains the temporary directory for diagnosis.

## Isolation boundary

A scope gets an independent state directory, in-memory configuration provider, service graph, plugin processes, and default empty workspace. Creation copies the owning generation's model and runtime configuration. Process-wide model metadata is initialized once by the host; creating a scope does not replace it or start another refresh. It never installs into the global plugin catalog or changes another session's configuration. Supply `root` only when file sharing is intended; relative sources and explicit roots resolve against the owner runtime's workspace.

Extensions remain trusted code. This is lifecycle and state isolation, not an OS security sandbox. Explicitly shared directories, OS credentials, configured external services, and effects performed by plugins can still be shared. Persistent provider replacement and checkpoint migration require their own compatible protocol or stopped-writer migration. These temporary scopes do not perform such a migration.
