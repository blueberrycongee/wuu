# Installing wuu

wuu currently ships as a macOS desktop preview and as a command-line tool you can
install with Go. The desktop app suits interactive work; the CLI suits terminals,
scripts, CI, and other agents.

## Install the macOS desktop app

The current GitHub Release provides an arm64 DMG and ZIP for Apple-silicon Macs:

1. Open [GitHub Releases](https://github.com/blueberrycongee/wuu/releases).
2. Download `wuu-<version>-mac-arm64.dmg` or `wuu-<version>-mac-arm64.zip`.
3. Move `wuu.app` into `/Applications`.
4. Open wuu.

The desktop package bundles the private core it needs at runtime; you do not need to
install the `wuu` CLI separately.

## Open the macOS preview

The preview uses a persistent self-signed identity, without Apple Developer ID or
notarization. After verifying the download is from the official GitHub Release,
try opening `/Applications/wuu.app`. If macOS blocks it, use **System Settings →
Privacy & Security → Open Anyway**. Do not install a certificate or disable system
security globally.

## Update from GitHub Releases

1. Download the new DMG or ZIP from the official release page.
2. Quit Wuu with **Cmd+Q** and wait for it to exit. Closing its window is not quitting.
3. Replace `/Applications/wuu.app` with the downloaded app. Keep the same name and
   location; do not run a second copy from the DMG or Downloads.
4. Open `/Applications/wuu.app`. Conversations and settings remain in Wuu's user
   data; do not delete that data to upgrade.

Wuu waits for its core and live computer-use preview to stop before quitting. The
CUA helpers live inside the app bundle and are replaced with it; there is no
separate privileged helper or login service to uninstall.

## Computer Use permissions

Computer Use is included on macOS. On first use, grant the requested
**Accessibility** or **Screen Recording** permission in System Settings. Wuu
reports missing access and links to the relevant settings; it cannot grant these
permissions itself. No developer tools or user-side signing are required.

Release signing keeps the app identity stable across builds, but does not promise
permission retention on every macOS version. The first update from an older
unsigned/ad-hoc build may require authorization again. If an upgrade loses access,
follow the reported permission's settings link and authorize the current
`/Applications/wuu.app` entry. Do not reset all privacy permissions or delete user
data. Old entries in the system permission list are separate from installed files;
remove only a confirmed obsolete entry if needed.

## Install the CLI

Build the CLI from a checked-out source tree. Product releases use CalVer tags,
which are not Go module major versions:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
```

GitHub Releases do not contain standalone CLI archives. The CLI installed with Go and
the core bundled inside the desktop app are independent: they can coexist and may be
at different versions.

### `wuu: command not found`

Confirm that Go's binary directory is on `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

If this makes `wuu` findable, add the equivalent line to your shell startup file.

## Run from source

To participate in development, clone the repository and run the CLI directly:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
go run ./cmd/wuu --version
```

For the desktop development environment and the full verification commands, see the
[development guide](../project/development.md).

## Next step

After installing, continue to [connect a model service](model-services.md).
