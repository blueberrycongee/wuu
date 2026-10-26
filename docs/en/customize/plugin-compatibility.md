# Plugin compatibility and upgrades

A newer Wuu product version does not imply a compatible plugin API. Product
releases use UTC CalVer; SDK packages, service contracts, runtime lifecycle,
composition and persisted data have separate versions. Check the contracts a
plugin actually uses, not just `minimum_wuu_version`.

## Current boundaries

| Layer | Supported boundary |
| --- | --- |
| Package manifest | Schema 1. Package dependency ranges constrain package versions, not service or product versions. |
| Process transport | `wuu-plugin-v1`, with transport `protocol_version: 1`. |
| Capability negotiation | Versions 1, 2 and 3; an omitted/zero version means 1. Unsupported explicit versions are rejected, never downgraded. Version 1 cannot declare capabilities; version 3 is required for service declarations. |
| Generation lifecycle | Version 2: initialize declarations, prepare dependencies/state, activate after commit, then shutdown. Product generation preflight rejects lifecycle 0/1 and unknown versions even when the capability version is supported. |
| Services | A consumer declares a name and major; a selected provider must implement that major. There is no promise that every earlier major remains available. For example, settings gateways and `state.documents` use major 1; the current `driver.turn` uses major 4 and driver profile services use major 2. |
| Composition | Versions 1–3. Version 1/2 compatibility projection is read-only; version 3 requires explicit dependencies. |
| Desktop | Each public snapshot/action contract has its own version. Runtime compatibility does not prove a desktop contribution still renders or behaves correctly. |

The SDK package remains `0.1.0`; that number alone does not identify its source
revision or all supported contracts. Retain the package lock and artifact hash
for a distributed plugin. Single-file development uses the host's embedded SDK;
rebuilding a plugin against today's SDK is not an old-plugin compatibility test.

For example, the repository-distributed SDK at commit
`08000266921da772f2ca999b8687f34fb4dbc911` has lifecycle 2 and can use the current
settings gateways without adopting newer optional response fields. Older
lifecycle-1 bundles require an author update before generation admission. This
does not mean every API from that revision is supported: required service majors
are still checked independently.

## Maintaining and retiring contracts

- Preserve the observable behavior of a supported major: existing valid calls,
  result fields, errors, cancellation, ownership and cleanup. New optional fields
  must not become mandatory for old callers. Use an explicit capability or a new
  method when callers must negotiate new behavior; do not silently emulate it
  with an unselected provider.
- An incompatible service change needs a new service major, not a product-version
  bump as a substitute. An additive change does not need an artificial breaking
  major. Change lifecycle or capability protocol only when that contract changes.
- Before retiring a supported contract, document the replacement, affected
  versions, last compatible host/SDK revision, migration steps and recovery limits
  in the public contract and release notes. Mark deprecated authoring APIs with
  the replacement. Keep the retained regression fixture while support is claimed;
  removal must be an explicit reviewed compatibility change, not a regenerated
  baseline that hides the break.
- Retain tests for rejection after retirement. An unsupported contract must name
  its failed version or dependency and stop before effects; it must not select a
  default provider. These rules are not an indefinite support or security-fix
  promise for every historical plugin or SDK release.

The currently accepted capability versions and lifecycle 2 are not marked for
removal. Experimental contracts and previously removed service majors do not gain
a compatibility guarantee from sharing SDK version `0.1.0`.

## Safe upgrade and recovery

1. Keep the previous executable, plugin artifacts, composition source and a
   consistent backup of provider-owned history, settings, bindings and checkpoints.
2. Prepare the candidate against the selected providers. A failed preparation
   keeps the active generation. An activation failure after durable commit reports
   the committed state and diagnostics; it is not an automatic rollback.
3. Keep admitted work on its captured generation. Same-ID updates may overlap
   while it drains; removal or disable revokes that plugin and required consumers.
   Restoring a selection does not undo completed external effects.
4. For composition 1/2, inspect the `migration:v1-to-v3` / `migration:v2-to-v3`
   projected providers before explicitly writing version 3. Startup does not
   rewrite the old source. Explicit exclusions, replacements and empty selections
   remain explicit.
5. Change state providers only through their migration contract. Preserve source
   data and reject conflicting destination state or unknown nonempty legacy
   tables. Do not erase history, invent checkpoint identities or resume an
   external driver through the native default. A downgrade needs compatible data
   and the prior provider/binding set, not just an edited version number.

See [composition and persistence contracts](plugin-system.md) for the actual
provider transfer and checkpoint requirements.

## Retained-artifact gate

Run `node scripts/plugin-compatibility-e2e.mjs` with Go from `go.mod` and Node
22.7+. No SDK build, package installation, model credentials or inference is
required. It verifies hashes before using the retained historical npm-pack
payload and exact shipped TODO JavaScript. Provenance records the public source
revision and compiler; it does **not** assert that this package was published to
the npm registry.

The gate exercises current Go process/configuration services, preparation,
activation, invocation, failed upgrades, reload, revocation, unsupported versions,
missing providers, additive fields, and existing composition/journal migration
regressions. It writes `report.json`, protocol traces and Go JSON test logs under
`.tmp/plugin-compatibility/`; `WUU_COMPATIBILITY_EVIDENCE` overrides the location.
CI retains that evidence. Desktop rendering, upstream service behavior and every
historical API remain separate acceptance work.
