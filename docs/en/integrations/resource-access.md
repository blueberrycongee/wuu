# Identity and resource access

Wuu separates three decisions:

- `identity.authentication@1` verifies a transport credential and establishes a principal.
- `resource.authorization@1` decides whether that principal may perform an action on every named resource.
- `security.authorize@1` decides whether an agent may execute a tool. A resource grant does not replace tool execution policy or grant broader filesystem access.

The default resource provider allows trusted local owners and already enrolled owner devices; other principals are denied unless the selected policy grants access. Installing a provider is a trust decision. Providers are not sandboxed or certified by Wuu.

## Authenticate an external connection

Start an app-server in provider mode:

```sh
wuu app-server --config /path/to/config.json --workdir /path/to/workspace \
  --authentication=provider --authentication-audience=my-wuu-host
```

The first input line must be an `authenticate` v1 frame. Send credentials on the private input stream, not command-line arguments, URLs or logs:

```ts
import {authenticationHandshake} from '@wuu/plugin-sdk';
child.stdin.write(JSON.stringify(authenticationHandshake('Bearer', token)) + '\n');
```

The host supplies the configured audience to the selected identity provider. The peer cannot supply an owner flag, trusted principal, or replacement audience. Missing, malformed or rejected authentication ends this connection; it does not fall back to local access. The default `--authentication=local` mode is only for an embedding's trusted local pipe, not a network proxy.

The successful response contains the connection's identity and finite RFC3339 `expires_at`. Identity is `{kind, issuer, tenant, subject}`; `kind` is `user`, `device` or `agent`. Subjects are opaque and are distinct across issuers and tenants. A decoded claim is not verified identity.

An adapter implements the SDK's `identityAuthenticationDescriptor()` service and its `authenticate` method. Input is `{scheme, token, audience}`; output is `{identity, expires_at}`. The adapter owns protocol-specific validation, including signature, issuer, audience, expiry and revocation. OIDC and SAML adapters can implement the same contract, but Wuu does not provide a general OIDC discovery or SAML login implementation.

The host reauthenticates the original credential before protected operations and output/replay. Revalidation uses the currently selected provider, rejects a changed identity, and cannot extend the connection beyond its original expiry. Provider errors, removal and revoked credentials deny access. Reconnect to establish a new authenticated session.

## Select a resource policy

The native provider is `wuu.resource-policy.default`; the native consumer is `wuu.resource-access`. Replace the provider using an ordinary composition service binding:

```json
{
  "service": "resource.authorization",
  "major": 1,
  "provider": "example.enterprise-access"
}
```

The replacement publishes `resourceAuthorizationDescriptor()` and handles `authorize`. Its request contains:

| Field | Meaning |
| --- | --- |
| `principal` | Verified namespace-qualified identity |
| `owner` | Host-established owner authority; never copied from application request parameters |
| `action` | Exact RPC method, `event:<name>`, or `request:<reverse-RPC name>` |
| `resources` | Every resource touched; an allow decision must cover all of them |
| `attributes` | Normalized requested effects, such as provider, model, engine, permission mode and tool bounds; not prompts or credentials |

Return `{allow: true}` or `{allow: false, reason?: string}`. Do not infer access to a named resource from a global grant. `{kind: "global"}` names a process-wide action; `{kind: "session", id: "…"}` names a conversation. Share reads use a distinct `session-share` resource, not an unrestricted grant to the original conversation. Resource IDs are scoped to the responding host; federation identifies its host separately.

Plain `thread/start` requires both the global create scope and `{kind: "workspace", id: "<resolved absolute root>"}`. The host resolves the omitted default and filesystem symlink aliases, then checks the actual root again before creation. A global create grant alone is insufficient. Workspace reads likewise check their resolved root; Git reads also require the actual repository root when it differs. Workspace lists filter each root and current-workspace metadata, with the same scopes retained for replay.

Use `resourceAuthorizationProvider(handler)` to build the SDK descriptor and dispatcher. Policy input includes requested effect attributes so a provider can deny a permission-mode escalation or an unapproved model without replacing the transport. Business adapters may define additional documented, namespaced effect attributes. They must not copy credentials, prompts or arbitrary content into them.

`initialize.resource_access` publishes version 1 and `delegated_actions`: protocol capabilities, not grants. Unsupported legacy mutations fail closed for non-owners even if a policy returns allow. The supported mutation surface is plain `thread/start`, `thread/delete`, `turn/start`, `turn/interrupt`, artifact cancellation and the separately scoped federation envelope. Generic project/handoff/worktree creation, compact, queued turns, model/settings changes and plugin administration are not delegated capabilities. Downstream handlers add capability registration together with resource extraction and effect-admission checks, not a policy bypass.

Unknown app-server actions have no non-owner wildcard fallback. Every new method must declare its resources. Lists and searches filter individual conversation metadata; reads and attachment/artifact requests are checked independently. Notifications and reverse requests require their own actions. A permission to read a conversation does not imply permission to start a turn, approve tools or control local UI.

## Revocation and replay

Initial dispatch attributes express supplied intent. Before creating a plain thread or admitting a turn, the host resolves defaults and stored provider/model/permission mode/engine, then checks those effective attributes again. The resolved attributes stay on response/replay guards. Policies with ceilings should deny missing attributes rather than assume a match. Generic RPC tool-bound fields are not supported or trusted; an adapter exposes tool bounds only when it enforces them.

Positive decisions are not cached by the host. It checks the selected policy before dispatch and before emitting results, checks each list row and event, and keeps each replayable frame's original principal/action/resources/attributes. A replaying transport rechecks that authority before delivery. Revoked frames are dropped; clients should request a fresh authorized snapshot rather than relying on a cached grant.

Delete rechecks after its lifecycle lease wait, and turn admission rechecks after durable refresh and hooks, outside the thread mutex and before the user append. Authentication is bounded by its expiry throughout policy waits. These are explicit admission boundaries, not distributed rollback transactions. Revocation prevents new admissions and further data delivery. It does not undo work already accepted or guarantee termination of an external operation whose backend cannot cancel. Explicit operation cancellation remains separate. Federation additionally removes ambient owner authority from delegated actions and applies the receiver's configured execution limits.

## Reproducible provider acceptance

The [independent access provider](../../../examples/plugins/independent-access/provider.ts) verifies synthetic Ed25519-signed JWTs against a pinned public JWK and owns its own grant/revocation state. It imports only the packed public SDK. This is a contract fixture, not a production identity provider.

```sh
go build -o bin/wuu ./cmd/wuu
node packages/plugin-sdk/test/resource-access-package.mjs
```

The test installs the independent package, drives the real authenticated executable, and checks scoped reads/list metadata, requested permission escalation, revocation, owner impersonation, unavailable policy and invalid signatures/issuer/audience/expiry. It writes sanitized protocol evidence and executable/package hashes under `.tmp/e2e/resource-access/`. No enterprise account or inference service is used.

## Shared desktop transport boundary (v1)

A delegated remote controller uses the existing desktop process and original
session owner, not a second app-server with an owner default. The public
`TrustedTransportPeer`, `TrustedTransportDispatch`, and receipt types in
`@wuu/protocol` describe the private embedding hop:

1. The remote host verifies the encrypted device key against persisted enrollment.
   Its secret-authenticated loopback handshake binds a principal, owner status,
   unique peer id, and a private live-validation capability. None comes from RPC
   parameters. Missing provenance or protocol negotiation fails closed.
2. The desktop rejects remote `transport/*` calls and forwards ordinary core RPCs
   through `transport/dispatch` on its explicitly trusted local stdio. Core binds
   provenance immutably per peer, checks the selected resource policy, and
   revalidates the original authentication before each protected operation.
3. The capability is a loopback `POST /v1/validate` with a random bearer secret;
   only HTTP 204 means valid. Redirects, unavailable validators, and revoked or
   expired enrollment deny. Tokens never reach a remote controller or renderer.
4. Core returns a private receipt for the exact response authorization closure.
   `transport/check` re-evaluates it, including the original action, resource set,
   effect attributes, and any operation-specific checks. `transport/event` binds
   each subscriber's current policy before publishing. The desktop cache and Go
   replay spool retain and recheck these receipts; they do not substitute a
   connection-level owner check. Missing or evicted receipts deny delivery.
5. `transport/release` revokes a peer's binding and receipts without taking
   ownership of session lifetime. Registries and queues are bounded; exhaustion
   expires authority, never grants it. Phone reconnects retain the existing
   bridge connection; an expired bridge obtains fresh authorized snapshots.

These are embedding hooks, not an alternative login or an owner-selectable public
endpoint. Provider-authenticated connections, ordinary paired connections, and
nested forwarded requests cannot invoke them. A replacement trusted host can
implement this small protocol without copying Electron's bridge. It must preserve
live authentication, per-frame receipts, and fail-closed replay semantics.

Delegated desktop OS commands and legacy plugin renderer calls currently fail
closed because those adapters do not yet expose scoped provider contracts. This
boundary is infrastructure for downstream compositions, not a cloud-agent UI or
sharing product. Existing owner-paired desktop behavior remains available.

Run `node desktop/scripts/trusted-desktop-e2e.mjs` with `WUU_AUTH_BINARY` pointing
to the built core. The acceptance builds an independent public-SDK policy in an
isolated home, then exercises encrypted device traffic through the production
bridge/pool and an existing local session. It records `result.json` and
`go-test.log`; the synthetic home and private transport capabilities are not
publication artifacts.

Desktop forwarding captures the original local process and connection generation
for dispatch, response checks, event checks, and release. Event authority comes
from the actual emitting connection, not a later workdir lookup. A selected
remote GUI connector cannot receive local validation capabilities. Trusted shell
adapters may register fixed cleanup when issuing an owned operation; retirement
runs that zero-argument cleanup on the captured generation before stopping it,
with a bounded wait. Data remains fenced after a route change, and cleanup cannot
change its method, parameters, or target to the replacement connection.
