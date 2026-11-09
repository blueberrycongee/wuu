# Minimal session composition

`composition.ts` selects the shipped version-4 `minimal` profile and a complete
independent `engine.wuu` provider. `engine.ts` imports only `@wuu/plugin-sdk`; it
returns deterministic echoes and supports `/wait` until the turn is interrupted.
It does not call a built-in loop, model, or tool implementation.

The profile is the same session-host bundle used by the default product, without
its execution and presentation providers. It explicitly selects local documents,
session semantics, configuration and owner authorization. These remain ordinary
replaceable services, not privileged defaults. The echo example is not an AI
model and does not implement arbitrary tool execution, compaction or model roles.

## Repeatable acceptance

Build the Go executable with the repository's normal build command, then run:

```sh
make build-go
WUU_MINIMAL_BINARY="$PWD/bin/wuu" \
  node packages/plugin-sdk/test/minimal-package.mjs
```

The harness reuses installed SDK dependencies offline, packs current SDK source,
compiles these two files in a separate directory against that public package, and
installs the engine through `wuu plugin install`. No SDK source internals or Go
embedding APIs are available to the fixture. It exercises the real JSONL
app-server: create, send, persisted history, provider update during an admitted
turn, cancellation, cleanup, restart, and failure after disabling the selected
provider. It also runs a frozen explicit version-3 product selection, checks
read-only authority migration, excludes each authority module, and replaces its
policy with an independent public provider. Version-4 omissions remain errors.
It saves the resolved live graph, protocol transcript, lifecycle events,
SDK/binary hashes and result under `.tmp/e2e/minimal-composition/run-*`.

`WUU_MINIMAL_OUTPUT` changes the evidence directory. `WUU_MINIMAL_SDK_ROOT` selects
a checkout with installed SDK dependencies; `WUU_MINIMAL_SDK_TARBALL` reuses an
existing package and records its hash instead of validating current SDK source.
A failing run keeps its evidence and exits nonzero. This is runtime acceptance,
not rendered desktop or real-model acceptance.

For a build-free one-file plugin workflow and the actual config JSON, see the
[composition guide](../../../docs/en/customize/plugin-compositions.md#minimal-session-host)
([中文](../../../docs/zh-cn/customize/plugin-compositions.md#最小会话宿主)).
