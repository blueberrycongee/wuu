# Shared native presentation resources

The iOS app embeds the same `WuuMascot`, process summary, and vendored Blobatar code used by the desktop. This surface owns presentation only. SwiftUI still owns navigation, chat layout, scrolling, keyboard, attachment pickers, accessibility, and all account/RPC state; the app consumes `packages/protocol` wire records.

`src/mascot.tsx` renders the conversation activity mascot and the active process row. It accepts activity, provider and model, native size, theme, lifecycle visibility, and reduced-motion preference. `src/process.ts` reuses the desktop `ToolActivityHelpers` and `ProcessSummary` implementations without a browser. iOS runs `NativeUI/process.js` in one off-main JavaScriptCore actor and renders the summary as native text; historical tool groups do not instantiate WebViews. The active operation still uses the shared `WuuMascot`. Process rows show summary text without expansion; SwiftUI owns conversation ordering and execution controls. Neither entry point exposes RPC or native capabilities. The bundled document permits only inline code/styles and embedded image data; external navigation and requests are blocked. The mascot is hidden from accessibility; process summary text remains accessible.

The iOS app consumes a checked-in snapshot, not a live dependency on desktop sources. Desktop changes do not require a native resource refresh. To explicitly adopt desktop presentation changes, run from the repository root after installing the desktop dependencies:

```sh
node clients/native/shared-ui/build.mjs
node clients/native/shared-ui/build.mjs --check
```

When refreshing the snapshot, commit `NativeUI/mascot.html`, `NativeUI/process.js`, and `NativeUI/sources.sha256` together. `--check` verifies that snapshot against current sources as an explicit refresh check; it is not a native build prerequisite. Xcode packages the committed resources without requiring Node or matching desktop source hashes. Licenses are bundled in `../licenses/Notices.txt`. See the [development guide](../../../docs/en/project/development.md#native-phones-and-remote-services) for verification and CI policy.

Reply actions reuse `Copy`, `Split`, and `Check` from `desktop/src/shared/iconArtwork.ts` as native template SVG assets, displayed at 14pt with 32×44pt touch targets and 32pt between button centers. To refresh those assets independently, run `node clients/native/shared-ui/export-icons.mjs`, then `node clients/native/shared-ui/export-icons.mjs --check`, and commit the three `Wuu*.imageset` directories in `ios/App/Assets.xcassets`. Xcode compiles the committed vectors without Node or a WebView.

`src/mermaid.ts` bundles the desktop's pinned Mermaid dependency into a separate offline document for iOS and Android. Each diagram keeps its WebView during streaming; rendering is serialized and paced at 500ms, with the latest source replacing queued deltas. Failed live parses retain the last valid SVG. Stopping retries final syntax immediately and exposes invalid source. Native code owns copy/source controls, theme, text size and lifecycle. The only document-to-native message is a bounded height; CSP and navigation policies prevent external requests and links.

Refresh the diagram snapshot independently with `node clients/native/shared-ui/build-mermaid.mjs`, then run the same command with `--check`. Commit `NativeUI/mermaid.html`, `NativeUI/mermaid.sha256` and `../licenses/Mermaid-Notices.txt` together. The generator includes full licenses for every bundled dependency. No Node installation or CDN access is needed for native builds or rendering.

Run the real-browser check after refreshing (requires desktop dependencies):

```sh
desktop/node_modules/.bin/electron clients/native/shared-ui/verify-mermaid.cjs /tmp/wuu-mermaid-evidence
```

It checks continuous streaming, partial and final invalid syntax, replacements, theme and font changes, multiple diagram types, height reporting and blocked network access. It writes screenshots and a JSON result to the specified directory.

After installing a Debug build, use `xcrun simctl launch <device> ai.wuu.native --native-ui-fixture --native-mermaid-fixture` for a local iOS conversation; add `--native-stream-fixture` for continuous deltas. Android's Debug-only sample uses the real `MessageText` with regular and quoted fences: `adb shell am start -n ai.wuu.nativeapp/.MermaidFixtureActivity`; add `--ez stream true` for streaming. These samples need no account or computer. Check source/copy actions, scrolling, light/dark appearance and accessibility text sizes, and capture screenshots or video with `simctl io` / `adb exec-out screencap`.

Validate changes in the iOS simulator: first presentation, list reuse, working transitions, dark mode, reduced motion, background/foreground, and navigation gestures. Browser/component tests alone do not establish native viewport layout or device performance.
