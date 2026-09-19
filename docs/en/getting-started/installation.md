# Installation

The desktop app includes the Wuu core. You only need a separate CLI installation if you want to run Wuu from a terminal or script.

## Install on macOS

The current release workflow builds an Apple silicon app. Check the assets and notes on [GitHub Releases](https://github.com/blueberrycongee/wuu/releases) for the version you are installing.

1. Download `wuu-<version>-mac-arm64.dmg` or `wuu-<version>-mac-arm64.zip`.
2. Move `wuu.app` to `/Applications` and open it there.
3. Complete the [first-run setup](index.md) to choose an engine and connect a model service.

The release workflow uses a persistent self-signed identity rather than Apple Developer ID signing and notarization. If macOS blocks the app, verify that it came from the official release, then use **System Settings → Privacy & Security → Open Anyway**. Do not disable system security globally or install a certificate to bypass this warning.

## Update the app

Download the new release, quit Wuu with **Cmd+Q**, and replace `/Applications/wuu.app`. Closing a window alone does not quit the application. Open the replacement from Applications rather than running a second copy from the download or mounted disk image.

Settings and conversations are stored outside the app bundle. Keep that data when replacing the app, and read the release notes for any version-specific migration requirements.

## Features that depend on the build

The current public release workflow disables account, remote-control, and Computer Use features. Instructions for those features elsewhere in the documentation apply to builds that enable them; their presence in the source does not mean they are available in a downloaded app.

For a Computer Use-enabled macOS build, screen capture and desktop control may require **Screen Recording** and **Accessibility** permissions. Grant access to the app you actually run. After an update, macOS may ask again; do not delete Wuu data or reset unrelated privacy permissions.

## Install the CLI

Install the Go version required by [go.mod](../../../go.mod), then run:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
```

`make install` uses Go's installation directory. If `wuu` is not found and you have not set `GOBIN`, add the default directory to your shell's `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

If you use `GOBIN`, add that directory instead. The CLI and the core bundled with the desktop are separate installations and can have different versions. The release workflow does not publish standalone CLI archives. Product release tags use calendar versions, so install from a checkout rather than using `go install ...@latest`.

Continue with [model services](model-services.md). To build the desktop itself, use the [development guide](../project/development.md).
