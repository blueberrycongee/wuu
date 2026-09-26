# Shared native mascot renderer

The iOS app embeds the same `WuuMascot`, process summary, and vendored Blobatar code used by the desktop. This surface owns presentation only. SwiftUI still owns navigation, chat layout, scrolling, keyboard, attachment pickers, accessibility, and all account/RPC state; the app consumes `packages/protocol` wire records.

`src/mascot.tsx` renders the conversation activity mascot and the active process row. It accepts activity, provider and model, native size, theme, lifecycle visibility, and reduced-motion preference. `src/process.ts` reuses the desktop `ToolActivityHelpers` and `ProcessSummary` implementations without a browser. iOS runs `NativeUI/process.js` in one off-main JavaScriptCore actor and renders the summary as native text; historical tool groups do not instantiate WebViews. The active operation still uses the shared `WuuMascot`. Process rows show summary text without expansion; SwiftUI owns conversation ordering and execution controls. Neither entry point exposes RPC or native capabilities. The bundled document permits only inline code/styles and embedded image data; external navigation and requests are blocked. The mascot is hidden from accessibility; process summary text remains accessible.

The iOS app consumes a checked-in snapshot, not a live dependency on desktop sources. Desktop changes do not require a native resource refresh. To explicitly adopt desktop presentation changes, run from the repository root after installing the desktop dependencies:

```sh
node clients/native/shared-ui/build.mjs
node clients/native/shared-ui/build.mjs --check
```

When refreshing the snapshot, commit `NativeUI/mascot.html`, `NativeUI/process.js`, and `NativeUI/sources.sha256` together. `--check` verifies that snapshot against current sources as an explicit refresh check; it is not a native build prerequisite. Xcode packages the committed resources without requiring Node or matching desktop source hashes. Licenses are bundled in `../licenses/Notices.txt`. See the [development guide](../../../docs/en/project/development.md#native-phones-and-remote-services) for verification and CI policy.

Validate changes in the iOS simulator: first presentation, list reuse, working transitions, dark mode, reduced motion, background/foreground, and navigation gestures. Browser/component tests alone do not establish native viewport layout or device performance.
