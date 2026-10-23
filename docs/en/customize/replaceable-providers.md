# Replaceable providers

Wuu selects storage, permission policy, coordination and application-shell implementations through the same versioned service graph. Native modules and process plugins declare the same service descriptors and dependencies. Selecting a provider does not migrate its data or grant a caller another module's authority.

Use [product composition](plugin-system.md#services-and-executions) to select modules and bindings, [runtime authoring](plugin-authoring.md) to implement a process plugin, and the [UI SDK](ui-sdk.md) for a renderer shell. All TypeScript names below are exported from `@wuu/plugin-sdk`; source-checkout imports are unnecessary.

## Services and boundaries

| Service | Supplied implementation | Public author entry |
| --- | --- | --- |
| `state.documents@1` | `wuu.state.sqlite`, `wuu.state.jsonl` | `documentStore`, `documentStoreDescriptor`, `StateQuery` |
| `state.credentials@1` | `wuu.credentials.os`, `wuu.credentials.file` | `credentialStore`, `CREDENTIAL_STORE_DESCRIPTOR` |
| `state.config@1` | `wuu.config.local` | `configurationStore`, `CONFIG_SOURCE_DESCRIPTOR` |
| `state.plugin-settings@1` | `wuu.settings.local` | `pluginSettingsStore`, `PLUGIN_SETTINGS_STORE_DESCRIPTOR` |
| `state.artifacts@1` | `wuu.artifacts.local` | `artifactStore`, `ARTIFACT_STORE_DESCRIPTOR` |
| `host.data.query@1` | `wuu.index.trace` | `queryData`, `DATA_QUERY_DESCRIPTOR` |
| `security.authorize@1` | `wuu.permissions.default` | `authorize`, `AUTHORIZATION_SERVICE_DESCRIPTOR` |
| `project.coordination@1` | `wuu.project-coordination` | `projectCoordination`, `PROJECT_COORDINATION_DESCRIPTOR` |
| `host.managed-session@1` | `wuu.managed-sessions` | `managedSessions`, `MANAGED_SESSION_DESCRIPTOR` |
| `ui.shell@1` | `wuu.default-shell` or a renderer plugin | `APPLICATION_SHELL_SERVICE`, `ApplicationGenerationAPI` |

Descriptors belong in a provider's `provided_services`; consumers declare `required_services` with the service name, `major_version: 1`, and `required: true` when absence must block activation. Merely importing a client grants no call authority. Renderer services belong in the package's `desktop.services` declaration.

The low-level state services are for implementations and admitted consumers. Ordinary plugins use `host.settings.get/list`, `host.storage.*`, `host.credentials.request`, and `importArtifact(host, input)` for their own settings, opaque storage, authenticated requests and tool artifacts. Those gateways retain ownership checks and route to the selected backend. A supplied `subject`, `owner`, `plugin_id` or `session_id` is domain data, never proof of identity.

The host retains execution admission, cancellation, lifecycle order, OS locks, permissions enforcement, managed-session creation and transport. The selected permission provider decides allow/deny; an allow decision does not disable native sandbox enforcement. Coordination providers own role policy, instructions, work transitions and scheduling. A shell owns layout and presentation while application actions remain host services. Disabling a provider stops its behavior; a failed selection does not silently activate the native implementation.

Renderer consumers declare `desktop.required_services` and call `api.invokeService({ service, method, params }, options)`. The host chooses the provider from that consumer's dependency and binds plugin identity, generation and execution. Options carry workspace selection and cancellation. `invokeRuntime` remains an opaque request to the same plugin's runtime; it does not select a service implementation. Keeping a UI consumer independently selectable from its business provider lets the same UI use a replacement without starting the old provider's behavior.

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

## Credentials, configuration, settings, artifacts and index

| Contract | Observable behavior |
| --- | --- |
| Credentials | `get`, `set`, `delete`, `compare-exchange`. Bytes use standard base64. `null` means absent, `""` means present and empty. CAS compares bytes; `value: null` deletes, and `{ exchanged: false }` is a definite mismatch. `set` requires a non-null value. |
| Configuration | `read`, `compare-exchange`. `source` preserves complete raw bytes in base64 or `null` for absent bytes; `config` is the validated effective product configuration. `expected_revision` compares the source identity. A reply may contain `committed: true` and `activation_error`; report that the write persisted even though applying it failed. |
| Plugin settings | `read`, `update`, `read-state`, `update-state` for `{ subject, scope: "user" \| "workspace" }`. Updates replace complete value maps at `expected_revision`; settings also carry a fingerprint. `settings_conflict` means reread before rebuilding an update. |
| Artifacts | `import`, `resolve`, `read`, `fork`. Provider scope comes from an admitted consumer. Read requires a positive `max_bytes` and returns base64 data plus MIME type. Resolve reports whether the provider manages the artifact. Ordinary tool imports use the authenticated gateway. |
| Data index | `host.data.query` method `call` accepts a thread, optional event types, turn and limit. Unknown types match nothing. Omitted/zero limits have no explicit bound; negative limits fail with `invalid_params`. Events are facts, not configuration or credential access. |

A missing native config file reads as `source: null`; a present zero-byte file reads as `source: ""`. Check for `null` before decoding. These values must not be normalized into each other by the client. A config CAS with `source: null` supplies zero replacement bytes: native file mode writes an empty file, rather than deleting it; trusted project mode rejects empty JSON. The CAS response can retain `null` even though the empty file now exists, so it describes returned source bytes rather than file existence.

Configuration revisions and settings revisions are opaque strings, distinct from document-store revision numbers. Neither service offers the document store's transaction receipt protocol. Retry only a definite compare-and-exchange conflict after rereading; reconcile an unavailable/canceled write before attempting another write. Do not log credential bytes, raw configuration or secret-bearing effective configuration.

Native configuration modes preserve their source boundaries: explicit-file mode uses that file; embedded mode starts from the supplied configuration; explicit project mode uses the project's base and settings layers, falling back from `.wuu.json` to `wuu.json` only when the primary is missing. Restricted project overlays on user configuration do not gain permission to replace credentials or grants.

Model authentication and login/refresh state use the captured generation's credential service. The native auth adapter can import legacy account data without overwriting the selected entry; that compatibility behavior belongs to the native implementation. An external credential provider is not silently wrapped with native-file lookups. In-flight login IDs remain associated with their original generation until cleanup.

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
