# Installation

The desktop app includes the Wuu core. You only need a separate CLI installation if you want to run Wuu from a terminal or script.

## Install on macOS

The current release workflow builds an Apple silicon app. Check the assets and notes on [GitHub Releases](https://github.com/blueberrycongee/wuu/releases) for the version you are installing.

1. Download `wuu-<version>-mac-arm64.dmg` or `wuu-<version>-mac-arm64.zip`.
2. Move `wuu.app` to `/Applications` and open it there.
3. Complete the [first-run setup](index.md) to choose an engine and connect a model service.

The release pipeline requires Apple Developer ID signing and notarization, with tickets stapled to the app and DMG. Check the downloaded version's release notes: older previews used ad-hoc signatures without a verified publisher identity. The following workaround applies only to those older trusted previews. Do not disable system security globally or install a certificate to bypass a warning.

If macOS still blocks the trusted official download after you move it to `/Applications`, remove quarantine only from that app, then reopen it:

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app
```

Ad-hoc signatures do not establish publisher identity or guarantee that macOS permissions persist across updates.

## Update the app

Signed macOS builds with the **Wuu > Check for Updates** menu check GitHub stable releases shortly after launch and every six hours. Confirm the download, then choose **Restart and Install Update** when your work is saved and running tasks can stop. Choosing **Later** leaves the app running; a normal quit does not install the downloaded update automatically. Prereleases and downgrades are not offered.

Older builds without this menu need one manual update: download the new release, quit Wuu with **Cmd+Q**, and replace `/Applications/wuu.app`. Closing a window alone does not quit the application. Open the replacement from Applications rather than running a second copy from the download or mounted disk image. Development and ad-hoc builds do not check for updates.

Settings and conversations are stored outside the app bundle. Keep that data when replacing the app, and read the release notes for any version-specific migration requirements.

## Features that depend on the build

The macOS release workflow includes Computer Use and Phone access for native iOS pairing. Account login remains development-only. Older downloads may have different features; check their release notes.

Computer Use includes its native helpers; no separate installation or environment variables are needed. Screen capture and desktop control require **Screen Recording** and **Accessibility** permissions.

On macOS, first-run setup offers these permissions. You can skip this step and return to **Settings → General → System permissions** later. Choose **Enable**, then drag the app icon from the floating guide into the list in System Settings and enable access. Clicking the icon reveals the running app in Finder if you prefer to add it manually. Wuu refreshes the permission status automatically.

macOS owns the authorization confirmation and may request a restart. After an update, macOS may ask again; grant access to the app you actually run without deleting Wuu data or resetting unrelated privacy permissions.

## Install the CLI

Install the Go version required by [go.mod](../../../go.mod) and Node.js 22.19
or later, then run:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
npm ci --prefix plugins
make install
wuu --version
```

`make install` uses Go's installation directory. If `wuu` is not found and you have not set `GOBIN`, add the default directory to your shell's `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

If you use `GOBIN`, add that directory instead. The CLI and the core bundled with the desktop are separate installations and can have different versions. The release workflow does not publish standalone CLI archives. Product release tags use calendar versions, so install from a checkout rather than using `go install ...@latest`.

The built-in CLI engine now uses isolated programmatic tool calling by default.
Keep Node on `PATH` when upgrading, or set `ptc.node_executable` in your user
configuration. Missing or unsupported Node prevents ordinary tool execution;
explicitly set `ptc.enabled` to `false` if you want direct tool calls instead.
Existing explicit global/family choices remain unchanged. The desktop bundles
its own runtime and needs no separate Node installation.

Continue with [model services](model-services.md). To build the desktop itself, use the [development guide](../project/development.md).
