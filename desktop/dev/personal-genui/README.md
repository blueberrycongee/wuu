# Personal interactive answer fixture

This fixture runs the production bounded renderer and shared PluginUIKit controls.
All data is synthetic. It does not connect an account, call a model, create a task,
or make an external write. The static HTML simulates persistence in browser
storage. The loopback service uses the production surface service with file
storage and a synthetic session-send receiver.

From the repository root, with the existing desktop dependencies available:

```sh
node desktop/dev/personal-genui/build.mjs
node desktop/dev/personal-genui/server.mjs
```

Open the printed loopback URL with `?service=fixture`. Opening
`.tmp/personal-genui/preview.html` directly uses the standalone browser fixture.
`GENUI_OUTPUT` can select a different private output directory. Never point it at
an installed application's data directory.

Run the renderer and persistence checks with the installed Electron and Node:

```sh
GENUI_NODE="$(command -v node)" desktop/node_modules/.bin/electron desktop/dev/personal-genui/e2e.cjs
node --test plugins/personal/src/genui/*.test.mjs
```

The Electron runner records light/dark, narrow, enlarged-text, keyboard-focus,
and unsupported-version screenshots plus `e2e-report.json`. The JSONL runtime
test records `shipped-runtime-protocol.jsonl`. It executes the generated Personal
runtime, including tool registration, history projection, owner routing and
restart recovery. Host services and model output are synthetic. These checks do
not establish a real provider/model loop or deployment acceptance.

The production DTO uses schema version 1. Input changes stay local. Save persists
only fields marked persistent. The “解释这些选择” action freezes their actual state in a Personal-owned outbox. The explicit
client click then supplies the authenticated admission proof to the bound main
session with tools disabled for the entire response turn: text explanation only,
without new UI or task execution. Delivery status comes from the durable host receipt,
not a transient queue ACK. A held receipt requires explicit continuation; an
unconfirmed execution is never replayed. The host owns save/submit labels.
Temporary fields reset on refresh. Neither state category is business state.
Unsupported clients use the history record's text fallback; this preview does
not alter the native phone clients.

History projects UI from the persisted structured tool result and verifies its
tool source against the stored surface. Publication identities include the host
turn and call identity. Reusing a provider call ID in another turn cannot attach
an old surface. No shared history record fields or Coding renderers are changed.

Same-surface snapshots update authoritative revision and delivery state without
remounting the controls. A conflicting local draft stays visible until the user
explicitly keeps it or discards it; an already submitted surface cannot accept it.
Delivery receipt revisions prevent late polling results from hiding a held state.
Expression failures remain local to the affected node. Corrected snapshots can
also recover a renderer boundary. Invalid standalone browser storage is cleared
for this fixture key, and the reset control remains available.

Before freezing a submission, the service budgets UTF-8 bytes of both the final
context and send envelope. A first, definite pre-admission rejection restores
editing. Transport uncertainty or any prior/concurrent attempt retains the
original request identity and submit fence. A legacy oversized G1 outbox is
recoverable only when no possibly accepted attempt is recorded and its context
exceeds the host's hard limit.

Missing surface storage or an unsupported schema keeps a verified text fallback
from this exact Personal tool identity. Other tool logs, mismatched content and
unverified sources remain hidden. This fallback identity currently follows the
host's public tool-name derivation; unknown future naming versions fail closed.

The current authentication evidence establishes host-admitted client transport.
It does not independently prove that a human used the UI. Binding the formal
principal is owned by the unified integration work; this fixture does not supply
that identity or claim that verification.

The current Go registry wraps ordinary provider errors as `service_unavailable`.
Personal recognizes only exact wire messages for the known pre-admission
refusals. Generic availability errors, transport uncertainty, and prior or
concurrent attempts stay fenced. `TestPersonalGenUIRegistryRefusalThroughShippedSDK`
uses the real appserver, registry and shipped JSONL SDK to verify this boundary;
its storage and client proof are test fixtures, with no real provider call.
