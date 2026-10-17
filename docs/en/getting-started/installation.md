# Installation

The desktop app includes the Wuu core. You only need a separate CLI installation if you want to run Wuu from a terminal or script.

## Install on macOS

The current release workflow builds an Apple silicon app. Check the assets and notes on [GitHub Releases](https://github.com/blueberrycongee/wuu/releases) for the version you are installing.

1. Download `wuu-mac-arm64.dmg` or `wuu-mac-arm64.zip`.
2. Move `wuu.app` to `/Applications` and open it there.
3. Complete the [first-run setup](index.md) to choose an engine and connect a model service.

The release pipeline requires Apple Developer ID signing and notarization, with tickets stapled to the app and DMG. Check the downloaded version's release notes: older previews used ad-hoc signatures without a verified publisher identity. The following workaround applies only to those older trusted previews. Do not disable system security globally or install a certificate to bypass a warning.

If macOS still blocks the trusted official download after you move it to `/Applications`, remove quarantine only from that app, then reopen it:

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app
```

Ad-hoc signatures do not establish publisher identity or guarantee that macOS permissions persist across updates.

Download filenames omit the version; the app and release notes still identify it, and automatic updates use version metadata and verified archive hashes. Older releases may have a version in their filenames.

## Install on Linux

Choose a release containing `wuu-linux-x64.AppImage` and `wuu-linux-x64.deb`.
Older macOS-only releases, including v2026.10.18, do not contain Linux packages.
Linux x64 requires an X11 or XWayland graphical desktop. Initial desktop
validation uses Debian 13; release checks run extracted packages on Ubuntu
22.04. The launchers select X11; native Wayland and arm64 are not supported
by these packages.

Download only from the official release. `SHA256SUMS-linux` checks downloaded
bytes; it is not a publisher signature. Linux packages do not use Apple's
Developer ID signing or notarization.

For Debian, download the `.deb`, then install or upgrade it:

```bash
sudo apt install ./wuu-linux-x64.deb
```

Open Wuu from the desktop application menu. From a terminal, use
`wuu --ozone-platform=x11`. The Debian package installs the desktop entry,
Electron libraries and private core; Node and Go are not required.

Ubuntu 24.04 installation has not yet been validated. The `.deb` includes a
Wuu-specific AppArmor profile for `/opt/wuu/wuu` to permit the user namespaces
Chromium needs, but that installed path requires separate acceptance. AppImage
and extracted-package launches are not supported on stock Ubuntu 24.04.

For AppImage, keep the file in a user-writable directory, then run:

```bash
chmod +x wuu-linux-x64.AppImage
./wuu-linux-x64.AppImage
```

AppImage needs unprivileged user namespaces, the host's GTK 3, NSS, ALSA
libraries and FUSE 2 (`libfuse2` or
`libfuse2t64`, depending on the distribution). If FUSE is unavailable, extract
and launch without changing sandbox settings:

```bash
./wuu-linux-x64.AppImage --appimage-extract
./squashfs-root/AppRun
```

The extracted directory has no in-app updater; replace it manually. Chromium's
sandbox stays enabled. If the host prevents sandboxed startup, use a supported
desktop configuration rather than adding `--no-sandbox` or disabling system
security controls.

Read-only and Standard tool execution require a kernel with enabled Landlock
ABI 3 or later. Wuu refuses confined commands when this capability is missing;
installing an AppImage does not supply a missing kernel sandbox. MCP OAuth
additionally needs a running session D-Bus and an unlocked Secret Service
keyring (for example GNOME Keyring); it has no plaintext credential fallback.
Native macOS Computer Use and app snapshots are unavailable on Linux.

## Update the app

Signed macOS builds with the **Wuu > Check for Updates** menu check GitHub stable releases shortly after launch and every six hours. Confirm the download, then choose **Restart and Install Update** when your work is saved and running tasks can stop. Choosing **Later** leaves the app running; a normal quit does not install the downloaded update automatically. Prereleases and downgrades are not offered.

Older builds without this menu need one manual update: download the new release, quit Wuu with **Cmd+Q**, and replace `/Applications/wuu.app`. Closing a window alone does not quit the application. Open the replacement from Applications rather than running a second copy from the download or mounted disk image. Development and ad-hoc builds do not check for updates.

Release AppImages offer **Help > Check for Updates** and the sidebar update
indicator, with the same download/restart consent as macOS. Keep the AppImage
in a writable directory. Debian packages and extracted AppImages update
manually; quit Wuu and install the next `.deb` or replace the extracted app.
An installed AppImage-to-AppImage upgrade requires separate acceptance with
two release versions; a package smoke check does not establish that result.

Settings and conversations are stored outside the app bundle. Keep that data when replacing the app, and read the release notes for any version-specific migration requirements.

## Features that depend on the build

The macOS release workflow includes Computer Use. Phone access depends on the release build flag; check its release notes. Account login remains development-only. Older downloads may have different features; check their release notes.

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
