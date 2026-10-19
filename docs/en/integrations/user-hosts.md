# Isolated user hosts

A user host binds one user space and product domain for its entire lifetime. Personal agents use this host rather than a project workspace. Existing Coding hosts keep their current configuration, tools and connection lifecycle.

## Activation and declared isolation

The default `logical_memory` user-host mode separates Personal and Coding memory APIs, injected context, history, indexes and plugin storage by the host-bound product domain. It does not promise operating-system or Shell confidentiality between processes running under the same OS identity. Valid user-space/domain binding remains required; a client cannot select a foreign memory domain.

Internal SDK embeddings can explicitly select `UserHostIsolationMode: "verified_host"`. That stronger mode requires a trusted `UserHostIsolationVerifier`; missing or failed verification returns `user_host_isolation_required` before private state is created. Unknown modes are rejected. A separate directory or caller assertion is not an attestation, and the logical mode never labels the host verified.

`initialize` returns `user_host_isolation: { memory: "logical", host: "unverified" }` for ordinary user hosts. A successfully verified embedding reports `host: "verified"`. These additive capabilities are separate from the immutable `user_space_id` and `product_domain`. Ordinary Coding initialization and permissions are unchanged.

The `app-server` and `remote host` commands accept `--user-space` and `--product-domain` with the default logical-memory policy. A user host cannot inherit a workspace identity or borrow the Coding desktop execution bridge. Model access, connector grants, remote pairing, notification credentials and deployment remain separate setup responsibilities.

## Identity and storage

The binding is supplied by the host and returned by `initialize` as `user_space_id` and `product_domain`. Clients validate it before calling Personal services. Extension inventory changes do not change this binding.

For a Wuu home, user data is separated as follows:

- `user-spaces/<user-space>/<domain>/state`: sessions, private plugin storage, memory and managed artifacts
- `user-spaces/<user-space>/<domain>/files`: permitted user working files and explicit attachment working copies

User hosts load an explicit absolute configuration or the user configuration. They do not inherit project instructions, global Coding skills, MCP servers, hooks or external execution environments. Protected private-state paths are excluded from Personal file and process tools. Installed extensions remain trusted code; this is not an untrusted-extension sandbox.

twin exposes one persistent main conversation per Personal user space. Its main Session ID is reused across navigation, reconnect and restart; it is not rotated or rebuilt by joining other sessions. Hidden task executions retain separate Session IDs and requirements versions. Missing main sessions report recovery errors instead of silently creating replacements. Historical identities and task data are retained, while current questions and results return to the main conversation.

## Persistent execution and client lifecycle

The internal SDK creates one `UserHost` execution authority per user runtime. It binds session routing before any client connects. Multiple authenticated transports attach to that authority; closing a client connection or sending protocol `shutdown` only detaches that client. Already admitted work can finish while no client is connected.

Connection-local RPC IDs and cancellation are isolated from other clients. Business request IDs remain unchanged so reconnect retries can recover the same durable receipt. EOF revokes live foreground submission authority, including contexts detached by a callback. It does not undo an already admitted turn.

A slow consumer has a bounded queue and is disconnected without blocking other clients or background completion. The host allows at most 16 simultaneous client transports. The application owner must cancel or close its transports and wait for their `Serve` calls to return before calling SDK `Runtime.Close`. Closing the authority ends its background execution; it is distinct from closing a window.

User-host transports expose `initialize`, `plugin/client/request`, `plugin/client/cancel`, user-question decision methods, device-bound push registration and connection shutdown. Generic Coding thread, workspace, storage, configuration and package-mutation APIs are unavailable on this transport. Desktop extension management retains its normal host context. The Coding account-history exporter detects user-host scope and stops; it does not export Personal data or interpret its inventory as Coding deletions.

Non-interactive remote hosts do not provide generic `ask_user` interaction. A plugin-specific confirmation contract, such as Calendar's immutable operation preview, is a separate client operation.

The persisted owner admits user attention through `session.attention` under its current notification and quiet-hours policy. Raw terminal events do not authorize Personal push. `Runtime.SubscribeUserAttention` is a host-lifetime observer independent of phone attachment. Its bounded, content-free startup buffer rechecks current ownership, generation and policy before replay; it is best-effort notification transport, not a durable provider-delivery receipt. Closing or disabling the owner suppresses late attention.

Notification grouping defaults to off. A configured window (up to 15 minutes) coalesces pending attention separately for each responsible agent, using the first admitted item's fixed deadline. Conversation results remain individual and immediately readable. The host retains at most 128 in-memory batches and a latest-message locator; shutdown can lose pending best-effort notifications. It rechecks the latest item's current policy at flush and relay replay, and conservatively suppresses the batch if that locator is no longer authorized. Phone providers receive one generic agent notification, not a precise per-item digest. Changing the setting to zero affects new admitted attention; already pending windows otherwise keep their original deadline.

Personal history search uses the public conversation projection, with optional inclusive `time_min` and exclusive `time_max` RFC3339 bounds applied before pagination. It searches visible user/assistant text, not hidden reasoning or execution-tool records. Returned sequence addresses navigate to the original main-conversation message; missing legacy timestamps remain unknown. Default Coding trace search is unchanged.

## Foreground input and attachments

A client submits exact user input in the host-owned `user_input` envelope. Plugin method input contains attachment references only; it cannot supply a file path or substitute bytes. Admission binds the plugin, literal prompt, business request ID, ordered filenames, media types and SHA-256 digests. It can authorize only the bound send. Background callbacks cannot synthesize user authority.

The host accepts up to 16 attachments and 20 MiB total decoded bytes per submission. Personal text is limited to 64,000 UTF-8 bytes. Native clients retain their smaller transport limits. Empty text is valid only with a selected attachment.

History exposes attachment metadata, not local paths or inline file bytes. `host.session.attachment.read` resolves an owned persisted record and returns bounded base64 chunks, with offsets measured in base64 characters. Personal exposes agent- and task-scoped adapters. Clients verify returned metadata and the final digest before showing a preview or download. Native and desktop rendering do not automatically load model-supplied remote images or arbitrary local URLs.

## Desktop surface

twin appears directly below Automations in Wuu's existing navigation. Opening it continues the main conversation. Its right panel lists ongoing work, recent activity and recent outputs continuously, without category tabs. Opening details changes that panel, never the conversation. Routine controls use the existing Automation binding; changing a schedule updates that same routine and binding. Personal notification and quiet-hour preferences live in Wuu's shared settings.

## Business contracts and external services

The main assistant and hidden task executions share the Personal Memory service with attribution and versioned corrections. Memory can be queried, corrected or forgotten in conversation; there is no separate desktop memory-management page. Forgetting redacts the memory record and prevents its future synthesis use; it does not claim to erase source conversations, user files or backups. Coding memory remains separate.

Task completion verifies persisted successful execution evidence. Main-conversation delivery is tracked separately as pending, submitted, delivered or failed; a completed task does not by itself mean its main-conversation reply has appeared. These checks verify attribution and state transitions, not the substantive correctness of an external result. Task stop persists targeted cancellation intent, including descendant work and queued results; it does not stop sibling tasks. Stopping the current main reply requires its explicit `turn_id`; delayed requests and retries cannot target a newer reply. Already accepted genuine user inputs remain queued. Recovery may remain pending while the execution host is unavailable. Waiting-user questions have their own durable delivery, and an answer updates the original task rather than opening an execution chat.

The bundled Calendar adapter starts unconnected. The read and write interfaces accept a trusted, user-space/account-bound provider for an already authorized grant; tool input and plugin configuration cannot supply credentials. The shipped default does not wire an authorization provider or writer. An embedding must supply that integration after separate account authorization; source adapter tests do not complete this setup. Its write contract requires a full immutable preview, explicit client confirmation, exact revision and digest, and read-back verification. A lost acknowledgment produces an uncertain outcome that is reconciled without blindly replaying a write. Provider-injected tests do not establish a real OAuth or account outcome.

See the [app-server protocol](app-server-protocol.md), [plugin authoring](../customize/plugin-authoring.md), and [native client setup](../../../clients/native/README.md) for the underlying integration surfaces.

Calendar proposals are reachable from the main conversation without replacing its Session. The review overlay retains the full immutable preview and exact revision/digest confirmation. Personal exposes only a read-only operations-list adapter; confirmation and cancellation remain trusted Calendar client operations. With no authorized provider, the UI reports the disconnected/read-only state and cannot confirm a write.
