# Release

Tagged releases are published by `.github/workflows/release.yml`.

## Trigger

`VERSION` is the product version source. Do not edit package versions by hand.

1. Add user-visible changes under `CHANGELOG.md`'s `[Unreleased]` section.
2. Run `make release-prepare` to select the next UTC CalVer automatically, or
   run `make release-prepare RELEASE_VERSION=2026.9.2` when the release number
   is already decided. The command updates `VERSION`, the desktop manifest and
   lockfile, and the active native app metadata, then moves the unreleased notes
   into a dated release section.
3. Review the diff, run `make ci release-check`, and commit the release change.
4. After the commit is on `main`, run `make tag-release` and push the annotated
   tag printed by that command.

The workflow refuses to release if the tag commit is not on `main`, if any
generated version differs from the tag, or if the matching changelog section is
missing or empty.

## Version policy

wuu uses Calendar Versioning (CalVer) for product releases:

- `YYYY.M.N` uses the UTC release year, month, and the release sequence within
  that month. For example, `2026.9.1` is the first September 2026 release and
  `2026.9.2` is a later release in the same month.
- `N` resets to `1` when the month changes. Prereleases such as `2026.9.2-rc.1`
  are used when packaged builds need broader
  validation before becoming the current release. Tags with a prerelease suffix
  are automatically marked as prereleases on GitHub.

The product version communicates release recency. It does not define
compatibility for the Extension API, remote protocol, stored data, or migrations;
those contracts must document their own compatibility and migration rules.

The private protocol, remote-core, and mobile packages remain at `0.0.0` until
they have an independent public release contract.

The local `make release-check` gate includes the Go core, desktop and macOS native
helper tests. The release workflow runs Go, desktop and signing tests against the
tagged commit, with the Go test cache disabled; it excludes CUA from the release
build. After `npm ci`, the workflow explicitly installs and verifies the Electron
binary so runner-level install settings cannot leave the test or build steps
with an incomplete Electron package. Release tooling consumes committed module
manifests and does not update `go.mod` or `go.sum`.

Calendar tags have a numeric major component that is not a Go module major
version. Install the standalone CLI from a checked-out source tree with
`make install`; the desktop release remains the primary packaged product.

## GitHub Secrets

The current release workflow publishes only the macOS Electron desktop preview
package. It requires:

- `GITHUB_TOKEN` (provided by GitHub Actions)
- `WUU_RELEASE_CERTIFICATE_P12`, `WUU_RELEASE_CERTIFICATE_PASSWORD`, and
  `WUU_RELEASE_SIGN_ID` for the persistent self-signed identity. See
  [maintainer signing setup](../../../desktop/scripts/RELEASE-SIGNING.md).

The release build sets `VITE_ENABLE_ACCOUNT=false` and
`VITE_ENABLE_REMOTE_CONTROL=false`, so this version is the local,
unauthenticated desktop experience. The sidebar identifies the mode as
“Local mode”, and account, device-linking, phone pairing, and remote-control
settings are hidden. Those surfaces remain available in development builds
while the flow is being completed.

The release also sets `WUU_SKIP_CUA_MAC=1`. Computer Use is not included: the
native CUA helper is neither compiled nor packaged, and the release verifier
rejects either helper if one is present.

The current macOS job requires that self-signed identity, but not Apple Developer
ID or notarization credentials. Missing release signing credentials fail the
build; the workflow does not silently fall back to ad-hoc signing.

The workflow sets `CSC_IDENTITY_AUTO_DISCOVERY=false` so `electron-builder`
does not select an arbitrary runner-local identity. The custom signer uses the
configured certificate. Self-signing is not Apple Developer ID signing or
notarization.

## macOS Gatekeeper

Desktop artifacts attached to the GitHub Release are self-signed preview builds.
After downloading the DMG or ZIP and moving `wuu.app` to `/Applications`,
macOS may block the app because Apple cannot verify the developer.

Use the [installation guide](../getting-started/installation.md): verify the
official download, try opening it, then use **System Settings → Privacy & Security
→ Open Anyway** if blocked. Users should not import a signing certificate or
disable system security globally.

## Output

The macOS desktop job verifies that the tag commit belongs to `main`, checks and
tests the Go core and desktop app, then builds and verifies the self-signed arm64
desktop preview. The workflow verifies that the packaged core version is clean
and that the DMG and ZIP are structurally valid before creating the GitHub
Release.

The app contains its required private `wuu-core` subprocess. No standalone CLI
archive or CUA helper is published; a separately source-installed `wuu` CLI can
coexist with the app and may have a different version.

The final GitHub Release contains only:

- `wuu-<version>-mac-arm64.dmg`
- `wuu-<version>-mac-arm64.zip`
- any matching `.blockmap` files generated by electron-builder
