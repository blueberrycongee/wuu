# Provider-owned authority acceptance

Run from the repository root with the repository Go/Node versions and installed
SDK dependencies:

```sh
node scripts/state-authority-acceptance/run.mjs
```

The runner packs the public SDK, installs it into an isolated temporary plugin,
typechecks both TypeScript processes, and runs the actual runtime composition test
with the Go race detector. Logs and tool versions remain under
`.tmp/e2e/provider-authority/run-*/` for review.

`provider.ts` is a stateless HTTP adapter using only the public SDK.
`backend.ts` implements storage independently of Go on a separate server path.
Its one server serializes storage operations and atomically publishes documents,
identity and receipts. It deliberately drops one response after durable commit.
The acceptance uses no selected native document provider and checks native
session creation/reopen, CAS and intent conflicts, lost-response retry, remote
restart, fresh-home bootstrap, verified stopped transfer, revocation, offline
failure and absence of business payloads or a local authority journal in Wuu home.

This is a synthetic loopback fixture, not a production remote database: it has no
network authentication, replication, distributed execution coordinator or
multi-server transaction implementation. Its JSON value handling follows the
existing external fixture and does not preserve arbitrary raw number lexemes.
Production adapters must provide authenticated transport, validate all requests,
atomically enforce global CAS and durable receipts at their chosen backend, and
fence reset identities and all writers during migrations. The configured single
execution host is independent of concurrent storage-client support.

See the [authority contract](../../docs/en/customize/replaceable-providers.md#authority-and-deployment-topology).
