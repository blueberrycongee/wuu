# Independent composition acceptance

This suite runs public SDK consumers outside the checkout, publishes single-file
plugins with `wuu plugin dev`, and drives the real stdio app-server. It uses
disposable profiles/workspaces and loopback-only synthetic inference. It never
opens the user's Wuu profile or calls an external model.

Build the SDK and core using the development guide, then run on Linux (physical
process teardown evidence uses `/proc`):

```sh
WUU_E2E_CORE="$PWD/bin/wuu-architecture" \
  node scripts/external-composition-acceptance/run.mjs --external-backend --delegation --policy
```

`--prepare-only` packs the current SDK and typechecks the fixtures without
launching the core. `--external-backend` also includes the independent execution
backend. `--policy` runs `TestDefaultPolicyExternalComposition` against the same
prepared SDK consumer directory; it requires the Go toolchain. A prebuilt SDK
tarball can be selected with `WUU_EXTERNAL_COMPOSITION_SDK_TARBALL`.
`--delegation` additionally exercises a filesystem-only audit decorator over an
explicit same-world peer, a memory filesystem with no process capability, and an
explicit native-worker delegate owning three TERM-resistant managed processes.
For focused diagnosis, `--backend-protocol-only` checks the actual Node worker's
authentication, operation receipts and physical teardown, while
`--backend-integration-only` checks its app-server process/filesystem paths.
`--delegation-only` runs the decorator and memory-filesystem paths alone.
These subset runs record their exact scope and do not stand in for the full run.

The existing public-package helper extracts a real SDK package into a directory
under the OS temporary directory. It reuses installed third-party development
dependencies rather than installing duplicate copies. The fixtures import only
`@wuu/plugin-sdk` and Node built-ins; they do not import Wuu internal modules.
`WUU_EXTERNAL_COMPOSITION_FIXTURE` in the preparation output identifies that
directory for focused Go acceptance runs.

## Behavioral checks

- An independent engine runs under the minimal composition with the native
  engine, loop, tools, model and agent-policy modules absent. It needs no host
  model configuration or inference request.
- A session-binding compare-and-exchange changes future turns, while an already
  admitted turn drains in its original composition. Failed preparation leaves
  the active composition intact. Failure and cancellation do not replay work.
- A disposed durable binding remains unavailable after restart instead of
  silently reverting to the default engine.
- An independent `agent.tools` provider replaces the removed native tools
  provider. Its public `host.process` calls run in the session's selected worker
  workspace, not the plugin/host workspace. Process receipts retain their
  environment when defaults change, deduplicate identical requests and reject
  changed intent. Pre-aborted requests and remote failures do not run locally.
- After a new graph generation is published, an admitted callback and separate
  background process still use the original environment. Settings refresh is
  not mistaken for plugin removal. Actual app-server shutdown stops the process.
- The bundled Monitor independently consumes the same remote process contract
  through actual start, read and stop operations.
- With `--external-backend`, the same process checks run against a Node backend
  implementing the public worker protocol instead of the Go worker. Actual
  `read_file` and `write_file` calls also reach this backend without touching a
  host-side sentinel.
- With `--delegation`, an audit decorator adds filesystem behavior without
  copying the selected peer's process implementation. Real file operations run
  once and keep their session/environment identity; process operations remain
  delegated. A second backend replaces files with memory-only state and rejects
  shell execution instead of mixing it with an unrelated physical filesystem.
  A separate real adapter delegates through the SDK to the native worker and
  leaves three TERM-resistant managed processes running until transport EOF,
  without a host plugin stopping them first. The adapter, native worker and all
  three managed processes must disappear without harness termination; durable
  terminal receipts must remain.
- With `--policy`, an independent policy replaces the removed default policy
  while the native loop/tool/session consumers remain. Its stop, failure and
  removal behavior controls real HTTP admission without native-policy fallback.

## Evidence and limits

Each run saves `report.json`, source revision and dirty state, fixture/binary/SDK hashes,
app-server request/response transcripts, stderr, control/model observations,
and `/proc` command lines, PGIDs and start times before and after shutdown
under `.tmp/e2e/external-composition/run-*`. Set
`WUU_EXTERNAL_COMPOSITION_OUTPUT` to choose another evidence directory. A failed
run retains partial evidence and never reports later checks as passed. All core
processes in one run use a byte-identical snapshot of the selected binary, even
if another development lane rebuilds the original output concurrently.
Every app-server must exit with code 0 on stdin EOF. Harness rescue at 15 seconds
(TERM) or 20 seconds (KILL) is a hard failure, never a successful shutdown. Failure
evidence is saved before fixture cleanup; cleanup signals only processes created
by this run whose captured command line, PGID and start time still match.

The command backend uses a distinct workspace on the test machine. This verifies
the remote protocol and captured routing, not SSH connectivity, cloud
provisioning, filesystem isolation, or production credentials. The example Node
backend is trusted test code, explicitly requires unconfined shell permission,
implements only the exercised operations, and is not an OS sandbox. Its process
journal preserves operation IDs for reconciliation but does not authorize
signalling a PID recovered from an earlier transport.

Process acceptance alone does not establish complete filesystem, PTY, native
engine, artifact-transfer or desktop execution-world consistency. The optional
filesystem scenario covers only its explicit read/write path. UI fixture sources
under `ui/` have separate renderer acceptance; this script does not claim visual
acceptance or arbitrary stateful-provider hot migration.
