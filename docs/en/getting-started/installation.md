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

## Handle the macOS security prompt

The current preview is not signed with an Apple Developer ID and is not notarized, so
Gatekeeper may block first launch. After confirming that the file comes from the
project's official GitHub Releases, run:

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app
open /Applications/wuu.app
```

This command removes the quarantine attribute from the downloaded app. Do not run it
on an app from an untrusted source.

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
