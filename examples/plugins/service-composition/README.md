# Service composition example

Two independently installable runtime packages compose a writing report:

- `text-analysis` provides `example.text.analysis` version `1.0.0` with a `measure` method.
- `writing-report` contributes a tool that calls this service through `host.service.call`.

The consumer declares a required package dependency on `text-analysis-example` (`^1.0.0`) in `plugin.json` and a required service major version (`1`) in its initialization result. These are different contracts: the package declaration determines availability and lifecycle order; the service declaration authorizes and resolves the named API. Neither installs or trusts the other package.

The provider counts Unicode code points, whitespace-separated words, and blank-line-separated paragraphs. This intentionally simple metric is not linguistic word segmentation. It has no mutable state or external effects, so its `concurrentServices` opt-in is safe. Stateful providers must establish their own reentrancy and cancellation policy; the opt-in does not make every callback concurrent.

## Build and inspect

From the repository root, with Node.js 22 or newer:

```bash
npm --prefix packages/plugin-sdk install
npm --prefix packages/plugin-sdk run build
for package in text-analysis writing-report; do
  npm --prefix examples/plugins/service-composition/$package install
  npm --prefix examples/plugins/service-composition/$package run build
  wuu plugin validate examples/plugins/service-composition/$package
done
node examples/plugins/service-composition/smoke.mjs
```

The smoke harness starts both bundled processes and routes one tool call through the provider, then checks invalid input and orderly shutdown. It is a protocol demonstration, not the production host's registry or proof of installation, trust, dependency resolution, or activation failure recovery.

Use the [plugin management flow](../../../docs/en/customize/plugins.md) to install and approve each directory separately, provider first. In a new conversation, ask Wuu to use the writing-report tool on `Hello world.\n\nReady to publish.` (with real blank lines). The report should contain 31 characters, 5 whitespace-separated words, and 2 paragraphs.

Disable the provider and verify that the consumer no longer becomes available at the next runtime adoption boundary. Re-enable it and verify recovery. A provider update rebuilds the runtime generation; a running turn retains its original generation until its work settles. The example has no desktop entry and does not exercise rendered UI.

See the [authoring reference](../../../docs/en/customize/plugin-authoring.md) for package versions, optional dependencies, services, and concurrency boundaries, or [编写插件](../../../docs/zh-cn/customize/plugin-authoring.md) for the Chinese reference.
