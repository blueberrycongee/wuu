# Installing wuu

The wuu desktop preview supports Apple silicon Macs and includes its own core.
You do not need to install Go or the CLI separately.

## Install the macOS desktop app

1. Open [GitHub Releases](https://github.com/blueberrycongee/wuu/releases).
2. Download `wuu-<version>-mac-arm64.dmg` or `wuu-<version>-mac-arm64.zip`.
3. Move `wuu.app` into `/Applications`.
4. Open wuu.

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

## Computer Use permissions

The current GitHub Release does not include Computer Use or the native CUA
helper. The following permissions apply only to a CUA-enabled source build.

When using Computer Use, grant **Accessibility** or **Screen Recording** access
in System Settings as requested. Wuu links to the settings, but you must grant
access yourself. No developer tools or user-side signing are required.

An update may require authorization again, especially when upgrading from an older
unsigned build. Authorize the current `/Applications/wuu.app`; do not reset all
privacy permissions or delete user data.

## Install the CLI

For terminal or script use, install the Go version required by
[go.mod](../../../go.mod), then build from source:

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
```

If `wuu` cannot be found, add Go's binary directory to `PATH`. If you set `GOBIN`,
use that directory instead:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Once it works, add the setting to your shell startup file. The CLI and desktop core
are independent and may be at different versions. GitHub Releases do not contain
standalone CLI archives. Product CalVer tags are not suitable for
`go install ...@latest`; use the source installation above.

After installing, continue to [connect a model service](model-services.md).
For desktop source builds, see the [development guide](../project/development.md).
