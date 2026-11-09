# Additive cooperation conformance example

This deliberately small example is one combination of public extension points,
not a catalog of supported products or a prescribed plugin architecture.

- `additive-counter` owns ephemeral process state and the `example.counter` v1
  service (`read`, `add`, cancellable `watch`). It publishes a typed contract as
  `@wuu-example/additive-counter/contract`, an owned `controls` UI slot, and a panel
  contribution to the default navigation.
- `additive-counter-controls` is a separate desktop-only package. Installing it
  adds an action and live display inside A's UI. Neither A nor the default
  product needs an edit, replacement binding, or knowledge of B's identity.
- A second installation identity of the same consumer exercises peer ordering.
  Normal ordering is `order`, then plugin identity, then contribution identity.

Use the public service contract for state and actions and owner-qualified slots
for UI. A slot context is not permission to call a service. Dependencies and
service requirements independently express package compatibility and call
eligibility; npm dependencies only distribute the typed helper. Hooks and
capabilities retain their existing documented contracts; this example does not
add an event registry or expose a private controller.

## Repeatable acceptance

From the repository root, after installing SDK and desktop development dependencies:

```sh
go build -ldflags "-X github.com/blueberrycongee/wuu/internal/version.Version=$(cat VERSION)" -o /tmp/wuu-additive ./cmd/wuu
WUU_ADDITIVE_BINARY=/tmp/wuu-additive node packages/plugin-sdk/test/additive-package.mjs
```

The runner compiles both packages against a packed public SDK. B consumes A's
packed public contract export. It bundles independent runtime/renderer entries,
installs through a real app-server, and loads the returned installed desktop
bytes through the production `DesktopPluginRuntime` and slot renderer. It writes
`result.json`, input SHA-256 identities, renderer logs and a synthetic protocol
transcript under `artifacts/additive-cooperation` (override with
`WUU_ADDITIVE_OUTPUT`). No dependency installation or model request occurs in
this runner. The temporary profile has no user data.

Required dependency failures reject a new installation or incompatible update;
failed preparation preserves the active combination. Optional consumers stay
active and must handle unavailable services explicitly. The fixture covers both,
package/service version separation, peer calls, same-source update, removal,
subscription cancellation and stale actions. The counter resets on each runtime
generation: it is not a durable-state migration example.

This is process/renderer contract acceptance in jsdom. It mounts the provider's
public panel, not the whole product navigation, and does not claim visual,
macOS, real-account inference or persistent-storage acceptance. Existing
`ApplicationShell` and `DesktopPluginRuntime` suites run in the same acceptance
batch to cover render-error boundaries and lifecycle contracts. The integrated
fixture also retains an admitted service task across an update, then cancels it
using its original captured identity.

See the [authoring contract](../../../docs/en/customize/plugin-authoring.md) and
[desktop UI composition](../../../docs/en/customize/desktop-plugins.md).
