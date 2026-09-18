# Shared native avatar renderer

The native collaboration pages embed the same `AgentAvatarMark`, `ChannelGroupAvatar`, `WuuMascot`, and vendored Blobatar code used by the desktop. This surface owns presentation only. Compose and SwiftUI still own navigation, chat layout, scrolling, keyboard, attachment pickers, accessibility, and all account/RPC state. The host owns room membership, message sequencing, replies, and recovery; both clients consume `packages/protocol` wire records.

`src/mascot.tsx` accepts the public agent/room presentation fields, activity status, native size, theme, lifecycle visibility, and reduced-motion preference. `src/process.ts` reuses the desktop `ToolActivityHelpers` and `ProcessSummary` implementations without a browser. iOS runs `NativeUI/process.js` in one off-main JavaScriptCore actor and renders the summary as native text; historical tool groups do not instantiate WebViews. The active operation still uses the shared `WuuMascot`. Process rows show summary text without expansion; SwiftUI owns conversation ordering and execution controls. Neither entry point exposes RPC or native capabilities. The bundled document permits only inline code/styles and embedded image data; external navigation and requests are blocked. Avatar records never load URLs. Decorative avatars are hidden from accessibility; process summary text remains accessible.

Run from the repository root after installing the desktop dependencies:

```sh
node clients/native/shared-ui/build.mjs
node clients/native/shared-ui/build.mjs --check
```

Commit `NativeUI/mascot.html`, `NativeUI/process.js`, and `NativeUI/sources.sha256` together with changes to their inputs. Gradle and Xcode verify the recorded source and output hashes before building, without a Node dependency in the native build environment. Licenses are bundled in `../licenses/Notices.txt`.

Validate changes in both native simulators: first presentation, list reuse, small group avatars, custom images, working/reply transitions, dark mode, reduced motion, background/foreground, and navigation gestures. Browser/component tests alone do not establish native viewport layout or device performance.

Android Compose falls back to pre-rendered idle `AgentAvatarMark` PNGs in
`android/app/src/main/res/drawable-nodpi/mascot_abstract_*.png` for timeline-sized
marks and for WebViews older than Chromium 80 (API 28 ships 69). Regenerate with:

```sh
bun clients/native/shared-ui/scripts/render-static-balls.mjs
```
