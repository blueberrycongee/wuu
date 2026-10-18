# Personal production-component fixture

Build from the repository root:

```sh
node desktop/scripts/build-personal-fixture.cjs
```

Open `desktop/out/personal-fixture/index.html`. The generated file is self-contained
and makes no account or external writes. It mounts the actual bundled Personal
contribution with the production PluginHost, Workbench, UIKit, Markdown renderer,
and Wuu styles. Only runtime responses are synthetic. The visible strip labels
this explicitly and provides light/dark, 15/19px, and offline controls.

This is a component acceptance fixture, not an app-server, real model, Electron
lifecycle, or remote-device test. Source changes require rebuilding and reloading.
The plugin itself remains the production implementation; do not copy its markup
into this fixture.

Suggested visual matrix: 1280×900 and 800×800; light and dark; 15px and 19px;
main conversation, specialist conversation with working state, Today, task detail,
Memory correction/forget, Settings, and offline failed send preserving the draft.
Capture original pixels and viewport/font/theme metadata. Review the pixels before
claiming visual acceptance. No synthetic screenshot has been accepted as a baseline.

Behavioral contracts are covered in `PersonalPlugin.test.tsx`; user host routing
and authority-boundary tests live beside AppServerClientPool and userHost.
`PrimaryPluginViewCover.test.tsx` also mounts the production App shell to check
user-space navigation isolation, Projects restoration, the host-isolation gate,
and exact submitted-text provenance. This component fixture does not reproduce
the outer App shell; those DOM tests are not visual acceptance either.

The fixture also mounts the production Calendar confirmation contribution. Its
reader starts unconnected and write-disabled. The clearly labelled synthetic
writer toggle only changes in-memory fixture replies; use Calendar Refresh after
switching it. A synthetic acknowledgement-loss response exercises uncertain
outcomes without contacting any calendar account. Inspect the complete immutable
preview before using the synthetic confirmation control.

Attachment examples use bounded synthetic bytes. Picking a local file reads it
into this fixture's browser memory only; no server or account receives it. The
production picker and chunk-verification code is reused. The sample text download
and attachment-only failed-send recovery are included in the behavior tests.
External-only Personal message Markdown never loads embedded remote resources or
resolves Coding artifact/file paths; verified files use revocable Blob URLs.

Repeatable DOM-only execution (requires the desktop dependencies):

```sh
node desktop/scripts/build-personal-fixture.cjs
node desktop/scripts/smoke-personal-fixture.cjs
```

This writes `desktop/out/personal-fixture/dom-smoke.json` with the synthetic
read-only gate, uncertain confirmation outcome, scoped file link, and Blob cleanup
results. Browser-only APIs are stubbed by jsdom; this is not download-to-disk,
Electron lifecycle, layout, screenshot, or GIF evidence.

Today now reads the exact local-day Calendar cache and exposes empty/stale/error
states. The default fixture remains unconnected. Tasks offer owner and local
updated-date filters explicitly limited to the loaded list. Agent history search
uses persisted timestamp bounds and cursor paging in production; hits retrieve
the original message by owned sequence. The small synthetic history includes
unknown timestamps so that legacy behavior can be inspected. Memory displays
plain provenance identifiers, contribution history, and effective times. Settings
include reversible notification grouping without delaying conversation messages.
