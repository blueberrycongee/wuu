# Replaceable providers

Wuu selects session, tool, agent-policy, storage, permission, coordination and application-shell implementations through the same versioned service graph. Native modules and process plugins declare the same service descriptors and dependencies. Selecting a provider does not migrate its data or grant a caller another module's authority.

Use [product composition](plugin-system.md#services-and-executions) to select modules and bindings, [runtime authoring](plugin-authoring.md) to implement a process plugin, and the [UI SDK](ui-sdk.md) for a renderer shell. All TypeScript names below are exported from `@wuu/plugin-sdk`; source-checkout imports are unnecessary.

## Services and boundaries

| Service | Supplied implementation | Public author entry |
| --- | --- | --- |
| `session.domain@1` | `wuu.session.default` | `sessionDomain`, `sessionDomainProvider`, `sessionDomainDescriptor` |
| `agent.tools@1` | `wuu.tools.default` | `toolService`, `TOOL_SERVICE_DESCRIPTOR` |
| `agent.tool.batch@1` | `wuu.tools.v1-batch` | `toolBatchResources`, `TOOL_BATCH_SERVICE_DESCRIPTOR` |
| `session.title@1` | `wuu.title.default` | `titleService`, `TITLE_SERVICE_DESCRIPTOR` |
| `agent.prompt@1` | `wuu.prompt.default` | `promptService`, `PROMPT_SERVICE_DESCRIPTOR` |
| `agent.policy@1` | `wuu.agent.policy.default` | `agentPolicy`, `agentPolicyServiceDescriptor`, `composeAgentPolicies` |
| `agent.compaction@1` | `wuu.compaction.default` | `compactionService`, `COMPACTION_SERVICE_DESCRIPTOR` |
| `state.documents@1` | `wuu.state.sqlite`, `wuu.state.jsonl` | `documentStore`, `documentStoreDescriptor`, `StateQuery` |
| `state.credentials@1` | `wuu.credentials.os`, `wuu.credentials.file` | `credentialStore`, `CREDENTIAL_STORE_DESCRIPTOR` |
| `state.config@1` | `wuu.config.local` | `configurationStore`, `CONFIG_SOURCE_DESCRIPTOR` |
| `state.plugin-settings@1` | `wuu.settings.local` | `pluginSettingsStore`, `PLUGIN_SETTINGS_STORE_DESCRIPTOR` |
| `state.artifacts@1` | `wuu.artifacts.local` | `artifactStore`, `ARTIFACT_STORE_DESCRIPTOR` |
| `host.data.query@2` | `wuu.index.trace` | `dataIndex`, `queryData`, `DATA_QUERY_DESCRIPTOR` |
| `security.authorize@1` | `wuu.permissions.default` | `authorize`, `AUTHORIZATION_SERVICE_DESCRIPTOR` |
| `project.coordination@1` | `wuu.project-coordination` | `projectCoordination`, `PROJECT_COORDINATION_DESCRIPTOR` |
| `host.managed-session@1` | `wuu.managed-sessions` | `managedSessions`, `MANAGED_SESSION_DESCRIPTOR` |
| `ui.shell@1` | `wuu.default-shell` or a renderer plugin | `APPLICATION_SHELL_SERVICE`, `ApplicationGenerationAPI` |

Descriptors belong in a provider's `provided_services`; consumers declare `required_services` with the service name, the major version shown above, and `required: true` when absence must block activation. Merely importing a client grants no call authority. Renderer services belong in the package's `desktop.services` declaration.

The low-level state services are for implementations and admitted consumers. Ordinary plugins use `host.settings.get/list`, `host.storage.*`, `host.credentials.request`, and `importArtifact(host, input)` for their own settings, opaque storage, authenticated requests and tool artifacts. Those gateways retain ownership checks and route to the selected backend. A supplied `subject`, `owner`, `plugin_id` or `session_id` is domain data, never proof of identity.

The host retains execution admission, cancellation, lifecycle order, OS locks, permissions enforcement, managed-session creation and transport. The selected permission provider decides allow/deny; an allow decision does not disable native sandbox enforcement. Coordination providers own role policy, instructions, work transitions and scheduling. A shell owns layout and presentation while application actions remain host services. Disabling a provider stops its behavior; a failed selection does not silently activate the native implementation.

Renderer consumers declare `desktop.required_services` and call `api.invokeService({ service, method, params }, options)`. The host chooses the provider from that consumer's dependency and binds plugin identity, generation and execution. Options carry workspace selection and cancellation. `invokeRuntime` remains an opaque request to the same plugin's runtime; it does not select a service implementation. Keeping a UI consumer independently selectable from its business provider lets the same UI use a replacement without starting the old provider's behavior.

## Partially extending a provider

Replacing a whole provider remains supported. For a small behavior change, an
adapter can instead provide the same contract, declare it in `required_services`,
and forward unchanged methods. It does not need to copy the supplied
implementation or import host internals.

`delegateService(descriptor, overrides)` creates an `invokeService` handler.
Only named overrides run custom code; other declared methods call the configured
delegate through the **current callback's** `RuntimeHost`. See the complete
[settings decorator](../../../examples/plugins/settings-decorator.ts): it adds a
missing default to the settings read view while leaving settings writes and
opaque-state reads/writes with the existing provider.

The composition administrator selects both routes. Replace the example ID below
with the installed/discovered adapter ID; selection does not install or trust it.

```json
{
  "version": 4,
  "profile": "headless",
  "patches": [{
    "modules": {"example.settings-decorator": true},
    "services": [
      {"service": "state.plugin-settings", "major": 1, "provider": "example.settings-decorator"},
      {"consumer": "example.settings-decorator", "service": "state.plugin-settings", "major": 1, "provider": "wuu.settings.local"}
    ]
  }]
}
```

The first binding is the global choice. The second changes only the adapter's
declared dependency. An external provider can be the delegate just as a native
provider can. Chains use the same dependency graph: unavailable targets,
undeclared consumers, incompatible shared method schemas and dependency cycles
reject preparation. Consumer modules must be included in the composition.

A scoped selection never falls back to the global provider when its target is
missing, disabled or fails. Override errors also do not run the base method.
`remove_services` includes the same `consumer`, `service` and `major` to remove
only a scoped choice. Registry diagnostics retain that consumer and binding
source; inherited runtime compositions retain those routes.

Delegation preserves the adapter's own identity, captured generation,
cancellation and cleanup dependencies. It does **not** impersonate the upstream
caller, transfer a caller's tool authority, or make provider-owned resource
handles interchangeable. Such methods still need their contract's explicit
ownership/delegation mechanism.

For an admitted `agent.tools.execute`, use the explicit
`continueService<ToolServiceExecution>(host, input)` inside the override; see the
[tool-service decorator](../../../examples/plugins/tool-service-decorator.ts).
Declare `SERVICE_CONTINUATION_HOST_FEATURE` (`service.continuation.v1`) in
`required_host_features`, provide `TOOL_SERVICE_DESCRIPTOR`, and require
`agent.tools@1` in runtime requirements (a renderer-only dependency is not a
backend delegation edge). Select the decorator globally and bind its own same-service
dependency to the native or external implementation, as in the settings example.
Other methods can still use ordinary `delegateService` forwarding.

The separate `host.service.continue` gateway accepts only the callback execution
ID and its opaque `continuation_id`. The SDK supplies the actual callback ID;
the host captures the original service major, method, arguments, tool identity,
generation and configured delegate. A continuation cannot rewrite a request,
choose another target or lend authority to an ordinary descendant callback.
Each decorator layer may continue once; delegate failure consumes it too.
Returning from the callback, cancellation or explicit removal/disable revokes
it. Normal generation replacement retains admitted callbacks and their original
dependencies: an old continuation never switches to the replacement provider.
The native tool provider also requires its own admitted tool callback, before
initializing its backend. An inherited backend scope or upstream `caller_scope`
is not enough. Ordinary forwarding that previously worked only with the native
implementation must use `continueService` for `execute`.
Process execution still passes the selected permission and sandbox services;
retaining work never retains an earlier permission decision.

Run `node scripts/service-continuation-e2e.mjs` for the packed-SDK decorator and
independent process backend. It checks chained decoration, denied and ordinary
calls to both native and external delegates, duplicate/failing continuations,
cancellation, owner/delegate removal,
post-pause permission denial, and replacement
while paused before continuation. Reports and logs are written to
`.tmp/e2e/service-continuation/`.

In a `project_agent` development build, the [project decorator](../../../examples/plugins/project-decorator.ts)
changes only instructions and enables `concurrentServices` because managed-session
creation can reenter coordination before returning. Select it for `project.coordination@1` and bind its
same-service dependency to `wuu.project-coordination` (or another delegate).
The native coordinator retains its own scheduler when it is in that selected
coordination chain, including through multiple decorators. Unrelated service
dependencies do not start coordination scheduling. Removing or disabling a
selected decorator does not activate the native coordinator as a fallback;
accepted turns retain their original graph while autonomous dispatch stops.

`state.documents@1` has one logical dataset and authority boundary per
composition. Its scoped routes may connect the selected provider's adapter chain;
an ordinary consumer cannot select a base around that authority. Use an isolated
[runtime composition](plugin-compositions.md) for independent state. Normal
provider replacement and verified state-migration requirements are unchanged.

Run `node scripts/provider-delegation-e2e.mjs` after installing repository
dependencies. It compiles the adapter against a packed public SDK and exercises
native/external chains, durable settings, delegated project scheduling, retained
turn completion, rejection and cleanup paths, recording
`report.json` and `verification.log` under `.tmp/e2e/provider-delegation/`.


## Replace an individual built-in tool

When the native `agent.tools@1` registry is selected, a trusted plugin tool can replace an individual supported tool without replacing the other tools. Install and enable the plugin first, then select its actual manifest ID and declared tool ID in trusted user agent configuration. Project settings cannot select replacements:

```json
{
  "agent": {
    "tool_replacements": {
      "web_search": {"plugin_id": "example-search", "tool_id": "search"}
    }
  }
}
```

The [synthetic search example](../../../examples/plugins/synthetic-web-search.ts) is a runnable authoring example; it returns a fixed demonstration result, not real search data. Install it with `wuu plugin dev` and replace `example-search` above with the plugin ID returned by Wuu. The supported replacements are `web_search` and the limited text and optional image contracts for `read_file` described below. The replacement must declare exactly the native input schema, freeform and direct-only settings; orchestration tools cannot be selected. The plugin's description is exposed under the selected native name, while its namespaced tool name is hidden. Missing, disabled or incompatible targets fail without falling back. Removing the binding is an explicit choice to use the native implementation in the next prepared generation.

For `read_file`, select the plugin and tool in the same `tool_replacements` map. The [local reader example](../../../examples/plugins/local-text-reader.ts) reads real local files up to 256 KiB; install it with `wuu plugin dev` and bind its actual plugin ID and tool ID `read`. It is trusted local code and runs outside the host path-check boundary. Its registration must declare `replacement_contracts: ["read_file.text.v1"]`, a read-only activity that is not an orchestrator, and exactly the native input schema and execution flags. For each fresh request, the provider receives the path, optional 1-based offset and limit, and the authenticated callback context including `cwd`. For text, it returns `structured_content: {text, total_lines}` with an optional pagination cursor as described below: `text` is the requested complete UTF-8 line range with its original line endings, and `total_lines` is the total number of source lines. The host validates range consistency and the size bound, renders line markers for the model and exposes the raw selected text to Code Mode. Invalid or failed responses fail the call without running another reader.

A provider may additionally declare `read_file.image.v1` in `replacement_contracts` to accept fresh local image requests. The host identifies the requested local file as an image and rejects line ranges. The provider returns exactly one inline `image` content part, with a matching `mime_type` and base64 data; the decoded input is limited to 2 MiB by the plugin result protocol. The host checks the format and decoded dimensions, rejects animated GIFs, and uses the native image processing path before forwarding the image to image-capable models and Code Mode. Small images may retain their original metadata and bytes; this processing is not metadata sanitization. The response is identified as unverified extension content. A text-only provider continues to reject image requests.

Only local regular text and supported images are accepted. Other binary and remote reads, and direct use of plugin-defined continuation tokens, are unsupported. Before calling the provider the host applies the native path and sensitive-file checks; the plugin process remains trusted code and is not automatically confined by those checks. Historical `read_file` continuations compatible with the native format and managed session artifacts use the host reader, including after the plugin retires. Their normal permissions still apply. A retired provider exposes only historical continuation calls in Code Mode; fresh reads fail. Extension content is marked `source: "extension"` and `content_verified: false`; it does not establish a native active-file freshness observation, and an older observation of the same file is invalidated after a successful extension read. This freshness record is context, not an edit permission. A provider must describe these restrictions to the model in its tool description.

A text provider can additionally declare `read_file.pagination.v1` and return a nonempty `next_cursor` alongside `text` and `total_lines` when a requested range has more lines. Each page must make progress, stay within the requested range, and include the same `total_lines`. When the range is complete it must omit `next_cursor`. The host returns an opaque `continuation.next` for the next `read_file` call; never return the provider cursor directly to the model. On continuation the host authenticates the token, rechecks both native and plugin authorization against the resolved path, and passes the original provider cursor with the next offset and remaining limit. The provider must support replay of a continuation against the same snapshot and reject a stale cursor if its content has changed; the host cannot verify an external provider's snapshot claim. The [local reader example](../../../examples/plugins/local-text-reader.ts) validates a content digest between pages.

Host tokens contain encrypted provider state and are bound to the session, actor, workspace directory and exact provider generation. The plugin cursor is limited to 4 KiB and tokens expire after 30 minutes. A token is replayable while its original provider generation remains available; retirement, a different generation, or a Wuu restart makes it unavailable rather than migrating state to another provider. Failed or cancelled calls do not leave a host-side per-page cursor entry. Native-format historical continuations and host result recovery retain their existing routes.

Run `node scripts/extension-read-e2e.mjs` for an independent process and a controlled model, including direct and Code Mode calls, legacy continuations, permission and retirement. This does not establish that an arbitrary provider is sandboxed or that its content matches the local file.

A successful result must be a single JSON object in the text or structured result: `action` is `web_search`, `query` matches the request, `evidence` has an `id`, `kind: "search"`, `source`, `source_tier`, RFC 3339 `retrieved_at`, and `version_matched_to_repo`, and it contains either `results` (at most 10 items with URLs) or a nonempty `error`. If both text and structured content are present, they must agree. Evidence IDs and source tiers contain only letters, digits, underscores or hyphens. For `version_matched_to_repo`, use the trimmed, whitespace-normalized `version_hint` when present; otherwise use `package_context` as `ecosystem name@version` using the available fields. Truncate to 160 Unicode characters and append `...` if longer; use `unknown` if neither supplies a value. The host rejects incompatible results and records valid evidence for the existing evidence context.

A session with an explicit tool allowlist must allow both the selected native name and the plugin tool’s namespaced public name for plugin execution. Run `wuu plugin describe --workdir <workspace>` and inspect `tools` to find the namespaced name registered for the plugin tool; the plugin ID and declared tool ID contribute to that name, which also contains a collision-dependent suffix. The command inspects a new CLI generation, so verify it matches the session’s selected generation before relying on it.

Scheduling uses the more restrictive native and plugin activity declarations. For a plugin execution, both the original and plugin tool authorization apply; disabling either name prevents that execution. Host-owned `read_file` continuation and managed-artifact calls use the native tool authorization and do not depend on plugin availability or authorization. A plugin runs as a trusted extension using its own callback identity. Native file and process safeguards do not automatically constrain arbitrary plugin-process code: use authenticated host gateways for managed effects and implement appropriate checks. Admitted work retains its generation; explicit removal or disable revokes the target. A frozen tool surface retains its definitions until the turn releases it, but execution checks target availability each time.

Run `node scripts/tool-replacement-e2e.mjs` for a packed-SDK synthetic plugin, a controlled model, direct and code-mode execution, worker and side-conversation paths, authorization and generation replacement. This does not test an external search service, real model quality or rendered UI.

## Session, tool and agent policy providers

`session.domain@1` replaces session-domain operations, including metadata, history, control and recovery transitions, membership admission and inference-journal resources. `state.documents@1` only replaces document storage; selecting it alone leaves the session algorithms in place. The native session provider owns those algorithms and uses the selected document store. An external session provider implements the public operations itself; the host does not run the native algorithms after a missing method or failed call.

Use `sessionDomainProvider(handlers)` to advertise implemented methods and dispatch their typed argument/value arrays. `sessionDomain(host).call(method, args)` is the bound consumer. Preserve the exported record field names, including Go-style casing where specified. Journal handles belong to the authenticated caller and provider generation. `session_outcome_unknown` requires durable inspection before replay; it is not proof that a mutation failed. OS locks and execution identity remain host mechanisms, while the provider owns atomic membership and domain decisions.

`agent.tools@1` replaces the registry and execution pipeline. `list` supplies definitions, discovery catalog and tool-surface facts; `resolve` supplies availability and metadata; `execute` runs the selected implementation. `freeze`, `unfreeze`, `prepare` and `finalize` preserve per-turn surface and result behavior. Contributing a tool is a different operation: contributions enter the selected registry's policy. The native provider initializes its Toolkit only when selected; external selection must not initialize a hidden native Toolkit or merge its catalog. Use `toolService(host)` and `TOOL_SERVICE_DESCRIPTOR`, with authenticated host gateways for OS effects.

`finalize` receives the normalized result once, including execution failures and
rejections settled through selected batch resources. Its `additional_context` follows that call's execute context;
`discovered_tools` merge by name, with finalization definitions taking precedence.
These additions stay with the settled invocation, including when parallel calls
finish out of order. Discovery belongs to that call's model-visible tool result.
Context also applies to nested calls and is saved in the next model-input receipt,
without becoming ordinary conversation history or leaking into later turns.
Hook decorators, native service adapters and selected batches do not infer call
ownership from a legacy shared last-result context slot; those integrations must
supply per-call context. The shared-slot API remains available only to direct
serial leaf-tool consumers without speculative streaming or nested execution.

`agent.prompt@1` owns complete main/worker system-prompt content and section metadata. `promptService(host).assemble(request)` supplies environment, instructions, skills, selected tool-surface facts and explicit contributions. Worker requests also carry role, isolation and identity facts in `worker`. The provider chooses wording and order; consumers must not prepend a native base, append worker policy, or replace an empty result with inherited instructions. Errors stop the request. Conversation titles use the independently selected `session.title` service below. Plugin-command side-conversation prompts remain separate from the main/worker contract; replacing `agent.prompt` alone does not replace either behavior.

### Replace one prompt section

The native prompt provider declares the optional `wuu.prompt.sections.v1` layout for main and worker results when their sections satisfy that layout. A decorator can call `promptService(host).assemble(request)` and then `await replacePromptSection(result, key, replacement)` from `@wuu/plugin-sdk`. The helper returns a new result, preserving the other sections and their metadata. The [prompt section decorator](../../../examples/plugins/prompt-section-decorator.ts) replaces `environment` while retaining the delegate's remaining prompt.

Select the decorator as the global `agent.prompt@1` provider and bind its own `agent.prompt@1` dependency to `wuu.prompt.default`, using the two-route composition shown in [partially extending a provider](#partially-extending-a-provider). Multiple decorators can be chained by binding each decorator to its next provider; each must preserve the declared layout. A missing delegate or failed replacement fails the request without a native fallback.

The layout joins UTF-8 section contents with two LF bytes. Each section records its UTF-8 byte length and the first 16 SHA-256 bytes as lowercase hexadecimal. The helper validates the declared layout, unique nonempty keys, metadata, separators, byte boundaries, hashes and complete content before replacing an existing key with nonempty text. It updates the replaced section's length and hash. An undeclared or incompatible layout is rejected; ordinary `agent.prompt@1` consumers can still use providers that do not declare it. A prompt section is guidance, not an authorization boundary.

`agent.compaction@1` owns context policy through `describe`, `compact`, `window`, `handoff`, `note-plan`, `compact-note`, `recover` and `note-attempt`. Use `compactionService(host)` and `COMPACTION_SERVICE_DESCRIPTOR`. Carry the captured policy key when required. Note preparation, cadence, retries and complete inference messages belong to the selected policy; host code validates history, commits checkpoints, accounts usage and supplies admitted model transport. Recovery calls the same selected service, never an implicit native alternative. A handoff byte limit of zero means no policy byte cap; model and transport limits still apply. Legacy compaction capabilities are an explicit behavior of the native provider, not a second selection path for external providers.

These contracts keep the Go host and public SDK. Authors implement a process plugin through the ordinary service declarations and callback-bound `RuntimeHost`; they do not need a replacement kernel, private source imports or privileged native APIs.

## Complete tool batches

`agent.tool.batch@1` replaces final-batch execution without replacing the loop or
`agent.tools@1` registry. The selected provider receives `execute` with an immutable
batch ID, raw calls, and the selected serial/capacity constraints. It owns call
inspection, ordering, parallel dispatch, per-call cancellation and rejection.
Return `{ completed: true }` only after joining every submitted resource operation.

If one process provides both batch and tool services, include both names in
`concurrentServices` so an awaited tool callback can enter that process.

Declare `TOOL_BATCH_SERVICE_DESCRIPTOR` and `requireToolBatchResources()`. Inside
the callback, `toolBatchResources(host, request)` exposes `resolve(index)`,
`execute(index)`, `cancel(index)` and `settle(index, errorResult)` through
`tool.batch.resources@1`. Indexes address only the original admitted calls; neither
arguments nor actor identity can be substituted. `execute` joins duplicate calls,
not duplicate effects. It returns the finalized, durable result and invocation ID.
`cancel` prevents a queued call or cancels and joins an active call. `settle` can
reject an unstarted call, but cannot replace an executed outcome. Results return to
the model in its original call order regardless of execution order.

The resource boundary retains permission checks, hooks, selected tool routing,
nested PTC/native-tool authority, capacity exclusion, result budgets, logging and
the durable invocation ledger. A provider error or missing completion receipt
cancels and drains accepted work, preserves completed results and rejects unstarted
calls exactly once. It never retries ambiguous effects or selects another backend.
Batch callbacks are scoped to their admitted generation; a new selection does not
reroute an in-flight batch. Raw arguments are limited to 4 MiB across 1–1024 calls.
The base descriptor accepts **completed batches**. A provider can additionally
advertise `TOOL_BATCH_STREAMING_SERVICE_DESCRIPTOR` to implement `stream_admit`.
For each complete, read-only, concurrency-safe prefix call, that method receives
`ToolBatchStreamRequest` and returns `{ admit: boolean }`. Only an affirmative
reply permits early execution; a defer decision holds the remaining prefix until
the final batch. This callback cannot dispatch effects itself. Final `execute`
receives all original calls; `resources.execute(index)` joins already admitted
work instead of executing it again. Errors cancel the stream and retain the
existing ledger replay fence. No product-ID checks select streaming behavior.
The supplied adapter supports this method and preserves early-tool/model overlap;
providers advertising only the base descriptor buffer until the final response.

`wuu.tools.v1-batch` is the explicit compatibility adapter for unchanged
`agent.tools@1` providers. It uses these same public resources, groups safe calls
for bounded parallel dispatch and keeps unsafe calls serial. Product profiles
include it; custom explicit module allowlists must include it or select an external
batch provider. Removing the selected provider fails closed. The reverse-order
[external example](../../../examples/plugins/independent-providers/batch.ts) and
`node scripts/tool-batch-e2e.mjs` demonstrate a separately packed SDK backend with
the default tool registry and batch adapter disabled. The native turn-resource
test also withholds model completion until the selected adapter starts its tool,
then verifies that final-batch joining does not repeat the effect.

## Conversation title policy

`session.title@1` is an optional dependency of the session execution consumer. The shipped `wuu.title.default` module supplies native title policy. Omitting the service disables automatic title generation and explicit regeneration; failure of a selected provider is reported without selecting a native fallback. Title selection is independent of `agent.prompt`.

Use `TITLE_SERVICE_DESCRIPTOR` to implement `plan` and `resolve`, and `titleService(host)` from `@wuu/plugin-sdk` to call the captured binding. `plan` receives `TitlePlanRequest`: model metadata, host-projected `human_messages`, current title and summary, read-only/ephemeral/parent facts, and `force`/`persist`. These facts do not grant authority. Its `TitlePlan` result chooses one action:

| Action | Result and behavior |
| --- | --- |
| `skip` | Optional `reason`; no inference or title write. |
| `complete` | A nonempty `title`; no model runtime is required and no native title cleanup or length policy is applied. |
| `infer` | Complete `messages` using public `ModelMessageV1`, plus optional `temperature`; the host invokes the selected model transport. |

Message fields use the public model wire (`role`, `content`, `reasoning_content`, and other declared fields), not serialized Go record names. An explicit zero temperature is preserved and overrides the configured sampling default; model capability restrictions still take precedence. After successful inference, `resolve({ text })` returns `{ title }`. The SDK helper `titleService(host).resolve(text)` returns that string. An empty resolved title skips persistence. Providers own eligibility, source selection, prompt wording, sampling policy and output cleanup; replacing the native provider replaces that business policy.

The host retains authenticated inputs, duplicate-job suppression, the captured generation, model receipt accounting, atomic generated-title updates and notifications. A receipt failure prevents saving or delivering the generated title. Generated-title updates do not overwrite a title already present, including a concurrent manual rename. A successful SDK call is not evidence that automatic triggering, cancellation and retained-generation cleanup have all passed product acceptance; verify those paths separately.

## Product profiles and renderer bindings

Composition version 2 includes `headless` and `desktop` profiles. Both use the `wuu.base` bundle; desktop adds `wuu.desktop`, including the supplied shell and settings renderer. Explicit configuration takes precedence over inferred product defaults. Profiles apply ordered bundles, then explicit module/service choices and patches. An explicit empty module set stays empty. Including a package does not grant trust or bypass admission; dependencies still need a valid selection. See [product composition](plugin-system.md#services-and-executions) for the configuration format.

A shell owns layout; a required surface has its own selected service binding. A shell's service-backed slot resolves that binding by plugin, generation and contribution identity, independently of the shell's slot namespace. Missing selected shells or required surfaces report unavailability instead of rendering a native substitute. Generic optional slots can still have a declared fallback. Use public `ApplicationGenerationAPI` views/actions and the [UI SDK](ui-sdk.md), not private application state. Selecting another shell does not by itself prove every business effect or surface has moved behind an independent provider.

## Use the callback's binding

Construct clients from the `RuntimeHost` passed to the current tool/service/capability callback. It carries that callback's execution scope. Do not cache it as a process-global client, reacquire the newest graph midway through an operation, or replace a failed call with direct filesystem access.

```ts
import {
  configurationStore,
  type RuntimeHost,
  type ServiceRequirement,
} from "@wuu/plugin-sdk";

// Include this declaration in initialize().required_services.
export const requiredServices = [
  { name: "state.config", major_version: 1, required: true },
] satisfies ServiceRequirement[];

export async function replaceSource(host: RuntimeHost, source: string) {
  const config = configurationStore(host);
  const before = await config.read();
  // source is standard base64 containing the complete replacement bytes.
  return config.compareExchange({ expected_revision: before.revision, source });
}
```

The helpers make one call and preserve errors, including `RuntimeHostServiceError.code`. They add no retry or fallback. `service_unavailable`, cancellation and revocation do not authorize calls through a newer generation. Existing retained work completes against its captured graph; removing a module revokes it across retained generations. Providers remain available while dependent cleanup drains. Ordinary read/write methods are not general-purpose cleanup methods; the lifecycle owner admits required shutdown persistence.

A shutdown deadline bounds the caller's wait, not the lifetime of unfinished cleanup. Keep provider dependencies, registry bindings and package resources retained until cleanup actually finishes; report timeout or pending cleanup explicitly. A timed-out close does not authorize releasing those dependencies or establish that all old effects have stopped. Verify cancellation, delayed cleanup and revocation through the actual product entry point.

Only config `read` and plugin-settings `read` / `read-state` advertise preparation reads. They may validate a candidate without durable writes; the registry still checks preparation admission. Other listed state methods require active admission.

## Bounded document queries and durable outcomes

`StateQuery` intersects `namespace`, optional `prefix`, explicit `keys`, inclusive `start`, and exclusive `end`. Keys sort by UTF-8 bytes; `reverse` reverses that order. An empty `keys` array selects nothing. A positive `limit` is 1–10000; omitted or zero remains unbounded.

```ts
import { documentStore, type RuntimeHost } from "@wuu/plugin-sdk";

export async function readNotes(host: RuntimeHost) {
  const store = documentStore(host);
  const query = { namespace: "research.notes", prefix: "item/", limit: 100 };
  let page = await store.read(query);
  const documents = [...page.documents];
  while (page.next_cursor) {
    page = await store.read({
      ...query, cursor: page.next_cursor, at_revision: page.revision,
    });
    documents.push(...page.documents);
  }
  return documents;
}
```

A cursor is the exclusive last key, not an opaque snapshot token. Continue with unchanged selectors and the returned revision. Any intervening commit anywhere in the store yields `state_conflict`; discard the accumulated pages and restart a pure read from page one. This does not retain old snapshots. Revisions are decimal strings: never convert them to JavaScript numbers.

A transaction supplies a stable `id`, `expected_revision`, and unique writes. The complete store revision must match, including changes in other namespaces and newly inserted keys. A successful commit is atomic and durable before acknowledgement. After a lost response or unknown commit outcome, retry **the exact transaction**, including its ID, expected revision and writes, to retrieve its persisted receipt. Reusing an ID with different intent yields `state_operation_conflict`. A definite conflict permits a new read and a newly constructed transaction; an uncertain write does not.

Cancellation stops waiting and prevents work not yet admitted. It cannot undo a durable commit or an external effect. A storage receipt is not permission to replay inference, HTTP writes, tool effects or message delivery. Keep their own reconciliation evidence.

Exports contain the revision, documents and receipts with intent digests. Migrate all three together. Optional `compact()` returns `StateCompaction` without changing document revision or receipts. Optional `import(snapshot)` returns `StateImportResult`; an acknowledgement alone does not verify a transfer. Providers advertise these only when implemented, using `documentStoreDescriptor({ compact: true, import: true })`. The base contract remains `read`, `commit`, `export`.

The product's document-backed session store includes conversation metadata/history, execution and recovery records, inbox/control state, driver bindings and Project state. Legacy SQLite conversion reads a stopped writer, preserves the source and raw rows, and commits converted documents plus a completion marker together. Unknown nonempty tables and conflicting destination data fail visibly. A completed marker prevents replaying old rows over evolved documents; the legacy source must remain unchanged.

Switching backend configuration alone is not a data transfer. Stop business consumers, export the selected authority, import into the target, verify a full export including receipts, then publish/admit the target before restarting consumers. Native providers also protect the shared durable authority pointer and live memberships; an old handle fails after authority changes. Never run two authorities as concurrent writers or treat restoring the old configuration as undoing data migration. Keep the original store and required WAL files until recovery is verified.

For native directory administration, use `wuu state check/export/import/migrate` with explicit directories; see [the document-store commands](plugin-system.md). External providers must implement the corresponding service contract and transfer procedure rather than relying on native directory commands.

### Authority and deployment topology

A selected provider can advertise `documentStoreDescriptor({ authority: true })` and implement the preparation-safe `authority {}` method. `documentStore(host).authority()` returns `StateAuthority`. This is an explicit **configured deployment**, not a list of topologies a database could support:

```ts
const authority: StateAuthority = {
  version: 1,
  mode: "provider",
  store_id: "account/dataset-stable-identity",
  storage_topology: "multi-host",
  execution_topology: "single-host",
  coordinator: "database-transaction-domain",
  cas: "store",
  durable_receipts: true,
};
```

Authority-aware external providers require the host feature `state.documents.authority.v1`. The host advertises exact names in initialize `supported_host_features`; the public SDK automatically checks that offer for any `state.documents` descriptor containing `authority`, before returning initialize success. It also declares `required_host_features` in the response. A missing offer, another version, or a missing required name rejects initialization: an older v3 host must never silently apply its local journal to provider-owned data. Unknown offered names are ignored unless explicitly required; they do not imply support for known names. Plugins without `authority` keep their previous compatibility. Authors implementing the wire protocol without the SDK must perform the same offer check themselves—required metadata alone is insufficient because old hosts ignore it. Initialization remains declaration-only; perform no business effects before negotiation succeeds.

- **`host-journal`** preserves the existing same-host admission, pending-write recovery and verified migration semantics. Both topologies must be `single-host`. Native SQLite/JSONL providers declare this explicitly. For compatibility, a v1 provider without an `authority` method retains this documented legacy contract; it does not select another backend. An advertised method with wrong schemas, malformed results, failure or unsupported semantics fails closed, never falls back to legacy mode.
- **`provider`** makes the selected backend the durable authority. The host records only identity/topology metadata, never pending transaction payloads or a full document mirror. Ordinary calls and reopen do not export all data or compare against a locally remembered revision. Another authorized storage writer may advance the remote revision; store-wide CAS and durable exact-ID receipts arbitrate conflicts. An empty Wuu home can bootstrap from that selected authority. Other selected providers may still store their own configuration, artifacts or credentials locally; this contract does not relocate them.
- **`store_id`** identifies one logical dataset, not a plugin, URL or incidental process. It must be durable before admission and immutable throughout a captured provider generation. A provider must reject operations against a reset/replaced dataset rather than silently redirecting an old binding. The host pins identity across reopen and fails on a different dataset. Identical copied bytes do not prove one live authority.
- **`storage_topology: "multi-host"`** requires one provider-controlled transaction coordinator, identified by `coordinator`, that atomically compares the entire store revision and stores every write plus its operation receipt. Per-process locks, eventual consistency, separate receipt writes and last-write-wins are insufficient. The coordinator is a storage transaction domain, **not an execution lease**.
- **`execution_topology`** declares the deployment's execution-writer arrangement separately. A cloud database with many readers or data clients can serve a single execution host. The native session domain permits that arrangement but rejects `multi-host` execution during preparation: its OS leases cannot fence processes on different machines. A replacement domain/coordinator must implement distributed execution fencing before enabling that topology. A trusted provider's false topology claim cannot be made safe by this declaration, and remote session routing does not supply that coordinator.

A clean host-journal → provider transition requires all old writers to stop, no unresolved pending transaction, and a complete candidate export matching the journal's document **and receipt** checkpoint. First reopen the original provider to reconcile a lost/uncertain local commit; selecting a remote provider is not permission to guess its outcome. The transition retains the old data and journal as recovery material and publishes only a provider identity pin. It does not erase existing local copies. Subsequent configuration-only changes cannot revive the old journal behind the remote authority.

Provider-owned migrations remain the provider's explicit administration procedure: fence **all** source writers, transfer documents, revision, receipts and logical identity, verify the complete target, then publish the target binding. Preserve `store_id` and coordinator identity only when that logical authority genuinely moves; a new independent dataset must have a new identity. The host does not infer a successful migration from an import acknowledgement or copied bytes. Changing the pinned dataset, returning to host-journal mode, or changing configured topology is rejected rather than automatically re-binding or deleting the pin; a host re-binding administration API is not yet supplied. Runtime consumer calls cannot import over an admitted authority.

Run `node scripts/state-authority-acceptance/run.mjs` for an independent public-SDK process that forwards to an HTTP backend. The repeatable acceptance log covers native session create/reopen without a native document provider, exact retry after a lost durable reply, conflicts, fresh-home bootstrap, stopped verified transfer, disabled/offline failures and absence of a host business-data journal. The fixture is a loopback test backend, not a production database or distributed execution implementation.

### Paged export

`store.export()` still sends `export {}` and returns the complete `StateSnapshot`. A provider that implements paging declares `documentStoreDescriptor({ paged_export: true })`. Only that opt-in changes the existing `export` method's input/output schema identifiers to `state.documents.export.input.v2` and `state.documents.export.output.v2`; the service stays `state.documents@1`. Other methods and legacy descriptors keep their v1 schemas. There is no separate wire method or automatic full-export fallback.

Use `store.exportPage(request)` with the public `StateExportPageRequest` type. It sends `export { page: request }`:

```ts
const page = await documentStore(host).exportPage({
  section: "documents", target_bytes: 1024 * 1024, max_items: 100,
});
```

`StateExportPage` returns `{ format: "page.v1", revision, generation, section, documents, receipts, done }`. The unused section is an empty array. Documents sort by UTF-8 bytes of `(namespace, key)`; receipts sort by strictly increasing integer revision, represented as decimal strings. Never use JavaScript number conversion, locale collation or UTF-16 sorting for these cursors.

Continue documents with `after: { namespace, key }` from the last actual document and pin `at_revision` and `generation` to the first page. After documents finish, export `section: "receipts"` with the same pins, continuing with `after: { revision }` from the last receipt. Both pins are required for every continuation and every receipts request; a pinned first document page must also supply them together. `done: false` requires at least one item and progress beyond the supplied cursor. Revision or generation changes invalidate the scan; discard partial results and restart the read against a consistent authority. The SDK does not retry errors.

`target_bytes` is an integer from 1 to 33554432 (32 MiB), and `max_items` is an integer from 1 to 10000. The byte target covers the actual UTF-8 JSON page and must at least fit its envelope; invalid budgets fail with `invalid_request`. A page with multiple items must fit; a single oversized record may exceed the soft target so paging does not impose a new document size limit. The external process transport's existing 64 MiB frame limit still applies: a record that cannot fit fails explicitly, without fallback. Host checkpoint scans request 32 MiB and 256 items per page by default.

Consume pages incrementally instead of accumulating another full snapshot. Host checkpoint payload memory is `O(page + largest record)`, plus `O(receipt IDs)` metadata for validation. This is not a fixed RAM guarantee: JSONL cold replay of large existing frames remains a separate allocation boundary, and `export {}` still materializes the full snapshot. Transfer verification must cover both complete sections, including receipt digests; a page or import acknowledgement alone is insufficient.

## Credentials, configuration, settings, artifacts and index

| Contract | Observable behavior |
| --- | --- |
| Credentials | `get`, `set`, `delete`, `compare-exchange`. Bytes use standard base64. `null` means absent, `""` means present and empty. CAS compares bytes; `value: null` deletes, and `{ exchanged: false }` is a definite mismatch. `set` requires a non-null value. |
| Configuration | `read`, `compare-exchange`. `source` preserves complete raw bytes in base64 or `null` for absent bytes; `config` is the validated effective product configuration. `expected_revision` compares the source identity. A reply may contain `committed: true` and `activation_error`; report that the write persisted even though applying it failed. |
| Plugin settings | `read`, `update`, `read-state`, `update-state` for `{ subject, scope: "user" \| "workspace" }`. Updates replace complete value maps at `expected_revision`; settings also carry a fingerprint. `settings_conflict` means reread before rebuilding an update. |
| Artifacts | `import`, `resolve`, `read`, `read-range`, `fork`, `delete-thread`. Provider scope comes from an admitted consumer. Read requires a positive `max_bytes` and returns base64 data plus MIME type. Resolve reports whether the provider manages the artifact. Ordinary tool imports use the authenticated gateway. |
| Data index | `host.data.query@2` supplies `call` for queries, `latest-context` for bounded latest-request metadata, and `append-turn` for atomic completed-turn batches. Queries accept a thread, optional event types, turn and limit. Unknown type names are valid filters. Omitted/zero limits have no explicit bound; negative limits fail with `invalid_params`. Events do not grant configuration or credential access. |

A missing native config file reads as `source: null`; a present zero-byte file reads as `source: ""`. Check for `null` before decoding. These values must not be normalized into each other by the client. A config CAS with `source: null` supplies zero replacement bytes: native file mode writes an empty file, rather than deleting it; trusted project mode rejects empty JSON. The CAS response can retain `null` even though the empty file now exists, so it describes returned source bytes rather than file existence.

Configuration revisions and settings revisions are opaque strings, distinct from document-store revision numbers. Neither service offers the document store's transaction receipt protocol. Retry only a definite compare-and-exchange conflict after rereading; reconcile an unavailable/canceled write before attempting another write. Do not log credential bytes, raw configuration or secret-bearing effective configuration.

Native configuration modes preserve their source boundaries: explicit-file mode uses that file; embedded mode starts from the supplied configuration; explicit project mode uses the project's base and settings layers, falling back from `.wuu.json` to `wuu.json` only when the primary is missing. Restricted project overlays on user configuration do not gain permission to replace credentials or grants.

Model authentication and login/refresh state use the captured generation's credential service. The native auth adapter can import legacy account data without overwriting the selected entry; that compatibility behavior belongs to the native implementation. An external credential provider is not silently wrapped with native-file lookups. In-flight login IDs remain associated with their original generation until cleanup.

## Artifact preview and download

`ARTIFACT_STORE_DESCRIPTOR` advertises version `1.2.0`. `artifactStore(host).readRange`
uses `{ thread_id, uri, sha256, offset, max_bytes }`; offsets count decoded bytes,
and `max_bytes` is between 1 and 1,048,576. The result contains base64 `data`,
`mime_type`, `offset`, total snapshot size `total`, and the complete snapshot
`sha256`. Return exactly `min(max_bytes, total - offset)` bytes. Reject an offset
past the end, a changed digest, or an unavailable snapshot. A provider without
`read-range` fails explicitly; the host does not emulate it with a full download.

Artifact IDs and provider URIs are opaque. The desktop never infers storage paths,
workspace roots, execution hosts, or authorization from their structure. A preview
acquires a document-bound transport lease through the source session's authenticated
route. The core matches published artifact metadata, retains the selected provider
for each operation, and fences reads with its captured generation ID. Every range
rechecks session access. Releasing the lease, retiring its renderer document, or
changing the provider generation stops further reads. Previewing does not create
a local cloud-payload cache.

The browser adapter declares its distinct `bounded-memory` source capability:
up to 8 MiB per artifact and 32 MiB across active leases. It verifies the complete
digest before exposing a transient Blob URL, does not persist bytes, and revokes
URLs on teardown. Larger resources and unsupported MIME types fail explicitly;
this is not equivalent to native range streaming. Callers can inspect source
limits through `application.artifacts.capabilities.source`.

On native desktop, only an explicit save, external-open, or reveal action materializes bytes. External
apps receive a private nonexecutable temporary copy; the complete digest is checked
before handing it off. Canceled or failed actions remove their copy. A successful
external handoff retains the copy for the operating system's temporary-directory
lifecycle, so closing Wuu does not remove a document still open in another app.
HTML remains sandboxed regardless of which provider stores it.

## Thread artifact cleanup

`ARTIFACT_STORE_DESCRIPTOR` advertises `state.artifacts@1` version `1.2.0`; version `1.1.0` added `delete-thread` with `{ thread_id }` and result `{ thread_id, deleted: true }`. An admitted consumer calls `artifactStore(host).deleteThread({ thread_id })` on its captured host. Success confirms the selected provider removed the thread's current payloads and manifests; an absent or already empty thread also succeeds. Other threads, fork copies, index data and session documents are outside this operation.

Stop and drain artifact producers before deletion. This operation does not prevent future imports under the same ID. Native import, fork and deletion serialize per destination thread across processes; the lock inode remains. Cleanup is an ordinary admitted service call, without a lifecycle cleanup bypass. If session documents were removed but artifact cleanup fails, report the partial result and retain the same provider/thread for an explicit cleanup retry. Never recreate those documents or delete native paths as a substitute. The helper does not automatically retry unknown outcomes; explicit repetition is safe because deletion is idempotent.

## Completed-turn index

The selected `host.data.query@2` provider owns both completed-turn writes and queries. Version 1 granted read-only authority; version 2 adds append authority, so consumers must explicitly declare major 2. `DATA_QUERY_DESCRIPTOR` advertises `call`, `append-turn`, and `latest-context` at version `2.0.0`; there is no implicit v1 adapter or separate native writer.

```ts
import {
  dataIndex, type DataAppendTurn, type RuntimeHost, type ServiceRequirement,
} from "@wuu/plugin-sdk";

export const requiredServices = [
  { name: "host.data.query", major_version: 2, required: true },
] satisfies ServiceRequirement[];

export async function recordTurn(host: RuntimeHost, batch: DataAppendTurn) {
  const index = dataIndex(host);
  const receipt = await index.appendTurn(batch);
  const facts = await index.query({
    thread_id: batch.thread_id, turn_id: batch.turn_id,
  });
  return { receipt, facts };
}
```

`DataAppendTurn` contains `thread_id`, `turn_id` and `events`. Every event must carry matching IDs and JSON `data`; the batch starts with a `turn` event and ends with `final`. Unknown intermediate event types remain intact. Providers admit and commit the complete batch atomically, never a partial prefix.

`DataAppendTurnResult` carries `version`, both IDs and `committed`. Appending the same committed turn with the same batch returns a committed receipt with `duplicate: true`; different events for that turn fail with `index_conflict`. Cancellation or transport failure can leave the outcome unknown. The SDK makes one attempt per call and never retries an uncertain append. Reconcile against the selected provider before deciding what to do next.

`queryData(host, input)` and `dataIndex(host).query(input)` use the same selected v2 service and preserve the existing filters and event envelope. Results keep provider order; optional filters apply before the limit. `events: null` and an empty array remain distinct wire values. Returned events can be folded into context summaries without opening a native trace file.

`dataIndex(host).latestContext({ thread_id })` calls `latest-context` and returns a `DataLatestContextResult`. It selects the last request in the most recently committed turn that contains a request. Its event array contains at most four events: the matching turn metadata, exactly one `context_requests` entry, optionally the provider state for that same step, and optional final metadata. A later turn without requests never relabels an older request. Tool payloads, checkpoints, answers and earlier steps are excluded by this projection. An empty array means no indexed request exists; errors never become a partial or stale success.

The latest-context result has a 16 MiB budget. Oversized required metadata fails with `limit_exceeded`, without truncation or a full-history fallback. The native provider commits the projection before historical events in the same atomic index document, so normal reads stop at that prefix regardless of history size. Older format-1 documents and legacy-only JSONL are supported within the same 16 MiB read budget; larger old sources fail explicitly until the next ordinary append imports/upgrades them. Offline replay remains available. This bound applies to latest-context reads; native append and diagnostic export still rewrite cumulative history and make no bounded-write performance guarantee.

Both query results and append receipts may include `diagnostic_path`. It is export metadata, not permission or a fallback data source. The selected native provider owns any legacy JSONL import and diagnostic export; an external index never triggers native-file reads or writes. Export failure cannot undo a confirmed append. Disabling the selected index stops its behavior rather than activating native storage. Keep late turn completion bound to its admitted generation until append settles.

## Project coordination protocol

`project.coordination@1` is business policy; `host.managed-session@1` is execution machinery. The native coordinator is selected only in builds with the Project Agent feature. Managed-session mechanisms support ordinary managed sessions independently of that policy. The default coordinator declares `state.documents@1`, `host.managed-session@1` and `host.background@1`; each consumer uses its own captured graph identity.

`projectCoordination(host)` provides typed `session`, `work`, `membership`, `enqueue` and `admission` calls. `ProjectSessionRequest` and `ProjectWorkRequest` retain the existing action vocabulary. Session/work/membership results vary by action and are deliberately `unknown` until the caller validates them. The service's complete descriptor also includes these provider callbacks:

| Methods | Input and result |
| --- | --- |
| `session`, `work` | Command envelope with `actor_id`, optional `call_id`, and `session` or `work`. Result depends on the requested action. |
| `membership` | `project_id`, `session_id`, `action: "adopt" \| "release"`. |
| `admission` | Empty request; returns `{ max_parallel }`. Capacity enforcement stays in the host's membership/lease transaction. |
| `enqueue` | Project/session/client IDs, cause, content, wake flag. Recording input is separate from delivery. |
| `work-list`, `member-work`, `coordinator` | Project lookup envelope; member lookup also has `session_id`. Results are a work list, `{ work, found }`, or `{ project, found }`. |
| `inbox`, `schedule`, `recover`, `capacity` | Session/project envelope; capacity also takes `action` and returns a boolean. Recovery without a project scans the coordinator's inbox. |
| `create`, `turn`, `user-input` | Native thread-start selection, turn completion or chat-message payload inside the command envelope. These are not ordinary `host.session.*` request shapes. |
| `instructions`, `tool-config` | A serialized `session.Session` directly, **without** a command envelope; returns instruction text or `{ allow?, deny? }`. |
| `tool-policy`, `tool-validate` | `{ member, call, read_only, policy }` using native session and tool-call payloads. `ProjectToolPolicy` identifies the write lease and work revision; validation follows OS lease acquisition. |
| `scheduler` | Owner-only, long-lived background callback. The registry authenticates its caller; setting a domain ID cannot start it. |

The [coordination dispatcher](../../../internal/appserver/project_provider.go) and [tool-policy contract](../../../internal/appserver/project_work_tools.go) define the native callback payloads. The SDK's convenience client covers common business commands, not every native session/turn record. A replacement provider must handle the methods its consumers call, preserve cancellation and structured errors, and retain its dependencies until cleanup finishes. Stopping scheduling cancels timers and waits; it does not reroute admitted foreground work to another provider.

`managedSessions(host).create(input)` reuses the public session-creation fields and adds `owner`, `source`, optional role, explicit session ID, shared-workspace session ID and initial inbox input. Explicit identity and initial input commit together. `submit` returns `{ admitted, capacity_full }`: capacity exhaustion is a result to handle, not evidence that the message ran. `allow_start: false` only attempts steering. `interrupt` targets an existing owned-workspace session.

The SDK supports text submission through `ManagedSessionTextMessage`. Its `Role`, `Content`, `ClientID`, and provenance keys preserve the current Go wire casing; `display_content` has an explicit JSON tag. `ManagedSessionInbox` likewise uses `ClientID`, `SessionID`, `Content`, `Wake`, and `Controls`. Do not substitute model-request snake_case message types. Control revisions fence takeover and release, while execution IDs come from the host-authenticated callback.

These contracts describe callable behavior. SDK package tests and the synthetic UI preview do not establish full product migration, real inference, or native macOS UI acceptance. Validate a replacement against the actual selected product graph and retain repeatable evidence before relying on it.

## Verify a replacement in the product

Verify the capability you intend to replace through its actual consumer. Selecting a document store demonstrates storage replacement; it does not establish replacement of session creation, history or recovery policy. Registering an additional tool demonstrates tool contribution; it does not establish replacement of tool selection or dispatch. Rendering another shell demonstrates shell selection; it does not establish independent replacement of each surface inside that shell.

Use synthetic data and record the selected provider, entry point, observed result and repeatable command or artifact. Cover these cases before relying on the replacement:

| Case | Observable result |
| --- | --- |
| Select the replacement | The product operation reaches that provider and returns its result. The previous implementation does not perform the same operation. |
| Remove or fail the selected provider | The consumer reports the unavailable dependency or original error; it does not continue through a native fallback. |
| Change composition during active work | Admitted work keeps its captured binding. New work uses the newly admitted graph; revocation remains effective for retained generations. |
| Cancel and dispose | Waiting stops, dependent cleanup finishes before its provider resources are released, and disposed subscriptions or timers stop producing effects. |
| Supply another owner's identity in request data | Domain fields do not change authenticated caller authority. |
| Replace a stateful provider | The explicit transfer preserves the required data and receipts; a failed preparation leaves the active composition intact. |

Run the checks through every product entry point you ship. A direct SDK client test can verify request encoding and error propagation, but cannot show that a desktop or headless consumer actually routes through that client. Keep those results separate from real inference and native UI acceptance.

## Composable loop policy

`agent.policy@1` selects one provider for scheduling, continuation and recovery.
The supplied module is `wuu.agent.policy.default`. The default loop, child
runners, streaming transport, unary model calls and independent
`host.model.invoke` calls use the captured generation's selection. Removing it
stops those calls; neither errors nor an empty decision select another provider.
Request transformations and context contributions keep their existing ordered
contracts; `agent.compaction` independently owns context replacement.

Export a runtime plugin providing `agentPolicyServiceDescriptor()` and implement
its `evaluate` method. `composeAgentPolicies(strategies)` calls strategies in the
supplied order with isolated input snapshots. Returning `undefined` delegates;
a decision short-circuits. Exhausting the chain, throwing, returning an invalid
action or cancellation fails the operation. A composition provider can declare
other public services as dependencies instead of importing their implementation.
There is no second host-side policy registry.

| Phase | Observation and decision |
| --- | --- |
| `schedule` | Evaluated once before turn effects. `serial` disables streaming tool starts and permits one leaf execution at a time, including nested calls. `parallel_safe` requires `max_parallel` (1–64), shared across turn batches and nested leaves, and retains tool concurrency safety declarations. |
| `continue` | Evaluated after a tool-free response. `stop` ends the turn; `continue` requests another model round within the turn's step and continuation budgets. |
| `retry` | A failed loop round can `fail`, `recover` through bounded context-overflow recovery, or explicitly `retry` a proven unsubmitted request. `max_attempts` is required for retry and its first value pins the total attempt ceiling; later decisions cannot renew it. |
| `transport_retry` | Receives the completed physical attempt's failure, submission count, attempt budget and backoff settings. `fail` terminates; `recover` supplies `recovery_action` (`replay_same_payload`, `wait_then_replay`, `refresh_credential`, or `switch_transport`) and optional `delay_ms`. Adapter support and existing durable workflow budgets remain mandatory. |

A policy cannot replay visible partial output, accepted tool effects, an unknown
submission, or exhausted inference budgets. Transport recovery retains physical submission receipts, including unknown but
potentially billable outcomes; the selected policy may permit a bounded model
reattempt, but cannot replay accepted or uncertain tool effects.
Delay is cancelable and bounded to 60 seconds per decision. Usage and durable
receipts survive failed attempts. Credential refresh, transport changes and
retries still require their existing write-ahead receipts and adapter support.

Complete loop replacements use `driverTurn(host, execution).evaluatePolicy()`
without surrendering their control flow: model calls, tools, checkpoints and
completion remain separate public operations. `settings().has_policy` reports
whether the runner has a selected policy client. Direct embedded Go tests that
construct an uncomposed runner do not prove product provider selection.

`examples/plugins/composable-policy.ts` is a single-file author example. Copy it
outside the repository and run `wuu plugin dev ./composable-policy.ts`, then
select its `agent.policy@1` binding in the desired composition. Run
`node scripts/loop-policy-e2e.mjs` for a repeatable packed-SDK process check with
two concurrent policy implementations, inherited child bindings and removal.
Evidence is written beneath `.tmp/e2e/loop-policy/`; its controlled model verifies
routing and control flow, not model quality or self-improvement.

### Child execution state

The native child-agent domain uses the selected `state.documents@1` provider for
thread metadata, delivery claims, task/run indexes, queue cancellation tombstones,
reports, and terminal/pending worker snapshots. The parent execution captures
this binding; worker follow-ups, recovery and application reads do not reopen a
native file store after provider failure. Disabling the selected provider makes
these operations unavailable even when another native provider is still active.

Each domain mutation compares the provider revision and commits its related
indexes and events atomically. Only a definitive revision conflict is retried.
An uncertain response retains the exact transaction in the error; it is not
permission to repeat a tool effect or reconstruct a write. Worker pending driver
state remains distinct from its last terminal result, so checkpoint recovery
cannot publish an uncommitted result delivery.

Before the first use of this format, stop older Wuu processes that could still
write legacy child files. Migration reads all discovered historical workspace
roots and imports a domain only when the selected provider confirms it is absent.
It never imports on a provider error; subsequent reads ignore stale legacy files.
Original files remain until ordinary session deletion for recovery. Session
deletion leaves empty authoritative images so retrying cleanup cannot resurrect
legacy child state. Switching document providers still requires the complete
stopped-writer transfer described above, including these namespaces and receipts.

Report Markdown files are derived artifacts written after the state commit.
A materialization failure is reported as committed state with incomplete artifact
work, not a rollback. Materialization rereads the current report under its file
lock so a delayed older writer cannot overwrite a newer committed report.

The embedded-browser tab registry also uses the selected document provider. Tab
URLs, retention status, and restart-recovery records migrate once from every
known historical artifact root; deleting a thread writes an empty authoritative
image so an old file cannot resurrect it. Both native and external-engine
conversation toolkits use this path. An unavailable state provider fences browser
host actions, while a persistence failure after a host action is reported as
potentially completed rather than silently replayed or reported successful.

## Engine sessions

`engine.<id>@1` is the execution and administration boundary. The shipped
`wuu.engines.native` module provides the native Wuu loop and external CLI adapters;
`wuu.engine-execution` consumes the selected services. The production engine
registry is an inventory projection, not another provider selector. A replacement
may provide `engine.wuu` without inheriting any native implementation or identity
privileges. Missing or disabled selections never create a substitute engine.

The version 1 `descriptor` response may declare `requires_host_model: boolean`.
The native Wuu loop declares `true`; engines that own model access and credentials
declare `false`, including a model-free replacement of `engine.wuu`. `engine/list`
preserves this client readiness/model-source metadata: `true` selects configured
host models and checks their credential readiness in the default composer;
`false` uses engine-owned model selection. It is not credential validation,
backend authorization, or a new server-side session-creation prerequisite.
Providers retain execution/readiness checks independently of this declaration;
it cannot bypass model authentication or permissions. For older descriptors that omit the field,
clients retain the legacy rule that `wuu` requires host model configuration while
other engines own authentication. Explicit `false` is never treated as omission.

Binding transports model, effort, speed, model options, instructions, permission
mode, MCP endpoints and resume reference together. The callback gateway is bound
to that provider and captured generation. Its opaque token alone grants no access.
The public `host.engine.session` operations deliver events, approval/input
requests and durable native references; `run_loop` delegates to the selected
complete loop driver rather than a built-in algorithm. Capabilities, not names,
select host-managed continuation, canonical history and steering behavior.

Closing a bound session cancels and drains its active run before releasing its
provider generation. A caller's canceled close context stops waiting, not cleanup;
a subsequent close observes the same terminal cleanup. Native warm scopes retain
CLI processes across turns and release them with the owning server. Confirmed
terminal receipts use separate bounded `settle_turn`/`settle_reference` cleanup
calls: they cannot admit a pending turn, launch tools or resume generation. An
uncertain interruption is not silently retried.

The same selected engine service exposes `describe`, `models`, `auth`, `quota`
and `history_fork`; unsupported operations fail explicitly. Model discovery
preserves context-keyed stale/partial results and refresh is explicit. Quota is
opt-in; remote calls cannot start `local-cli` authentication. History forks require
native boundary evidence and never reconstruct an unsupported fork by replaying
transcript effects. Disabled native
engines remain discoverable in settings, but only metadata inspection is allowed.
They cannot launch authentication, quota/model discovery or history operations.
Use `node scripts/engine-provider-e2e.mjs` for the independently installed public
SDK replacement check, and the fake-CLI lifecycle tests for process and durable
cancellation behavior. Neither requires a paid model or a user Codex session.

### Model transport capacity

`model.capacity@1` selects the admission/cooldown strategy; the default module is
`wuu.model.capacity.default`. `host.model.capacity@1`, provided by
`wuu.model.capacity.resources`, is the separately retained physical resource
bound. Embedders explicitly share a `runtime.CapacityResources` owner across
sessions. Derived compositions and generation reloads inherit that owner through
their native factories; an isolated strategy does not create additional physical
slots. Independent owners intentionally isolate resources. This is process-local
coordination, not a claim of cross-process account enforcement.

The physical submission receipt boundary applies both contracts before a native,
SDK receipt, or direct adapter can send. A delegating adapter does not consume a
second slot for its child's send. All adapters share the host-known configured
provider-route bound. Native transports additionally use an opaque credential /
origin account domain, so native provider aliases share capacity. Arbitrary
external account aliases require an explicitly declared common opaque
`capacity_scope`; credentials must never be used as scope identifiers. Multiple
distinct scopes are acquired in deterministic order, and canceled partial
admission releases every acquired lease.

Implicit resource owners retain a root lease for the entire Session/OpenState
lifetime, including periods when capacity modules are disabled. Prepared candidates
and retained generations hold separate leases, so replacement and repair cannot
prematurely dispose their owner. Explicit owners remain caller-owned: close their
sessions/states, then call `CapacityResources.Close(ctx)`. Closing seals admission,
cancels queued requests, and stops policy timers; a wait timeout leaves admitted
physical work owned until its retained references drain.

Physical capacity and callback authority have separate lifetimes. Disable or
logical cancellation does not free an unsettled physical slot. Authoritative
terminal settlement frees that slot immediately, allowing another physical send
within the same callback. The selected service graph and receipt authority remain
retained until the original physical RPC completes or its process termination is
confirmed. Unacknowledged termination is not reported as success. A retirement
wait deadline reports pending cleanup rather than claiming termination; cleanup
settlement remains authorized through the retained scope.

`node scripts/capacity-provider-e2e.mjs` verifies an independently installed public
SDK strategy plus external receipt cancellation, late settlement, and process-tail
termination against a shared one-slot owner, without paid model calls.

Checkpoint continuation is a distinct `agent.policy` phase: `checkpoint_resume`
accepts `resume` or `fail`, not `retry`. Its `completed_messages` counts durable
protocol-complete items, including reasoning-only progress. A resume samples a
new request from that saved history; it never replays the interrupted payload.
The selected policy must authorize it, and the host still rejects missing
checkpoints, tool effects, and exhausted receipt/workflow budgets. A policy that
does not recognize the phase fails closed.

## Independent goal and automation presentation

| Package | Owns | Public dependency or service |
| --- | --- | --- |
| `goal` | Persistent state, continuation, goal model tools and `/goal` prompt | Provides `goal.control@1` |
| `goal-ui` | Composer goal controls and display | Consumes `goal.control@1` |
| `automation` | Durable schedules, runs and background dispatch | Provides `automation.scheduler@1` |
| `automation-client` | The model's `cron` tool | Consumes `automation.scheduler@1` |
| `automation-ui` | Automation catalog and sidebar | Consumes `automation.scheduler@1` |

Remove either UI package without removing the provider's data, tools or ongoing
work. Replace a UI by installing an independent desktop package declaring the
same service requirement; it imports neither the old UI nor runtime source.
Likewise select a compatible runtime provider without copying presentation.
The UI dependencies are optional so disabled/unavailable providers leave a
visible service error and disabled mutations, not a crashing application or
silently substituted scheduler. Successful later reads recover the controls.
The cron tool requires its scheduler and never schedules work locally.

`GOAL_CONTROL_SERVICE_DESCRIPTOR`, `GoalControlRequestV1`, `GoalControlResultV1`
and `GoalSnapshotV1` are exported by the SDK. Declare `goal.control@1` in
`desktop.required_services` and call:

```ts
const result = await api.invokeService<GoalControlResultV1>({
  service: 'goal.control', method: 'request',
  params: { method: 'get_goal', input: { thread_id: threadId } },
});
```

The request methods are `get_goal`, `create_goal` (also requires `objective`),
`pause`, `resume` and `clear`. All require `thread_id`; results contain `goal`,
or `null` when absent/cleared. Creation/resume can start an ordinary session turn.
Completion and blocked status remain model tools, not UI control methods.
Calls carry the authenticated consumer's graph and execution; the UI cannot name
an implementation provider. Goal storage remains owned by the original `goal`
identity; splitting presentation does not move or duplicate it. The old
`plugin.client.request` goal endpoint remains an adapter to the same controls
for older combined-package clients until that v1 capability is retired.

For explicit compositions, select `goal-ui` and/or
`automation-ui` independently from their runtime packages. Existing
combined third-party manifests remain supported. Composition migration preserves
pre-split bundled desktop selections; new explicit compositions do not infer a
UI from a runtime selection. A headless composition needs neither UI package.
