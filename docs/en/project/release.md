# Release

The [release workflow](../../../.github/workflows/release.yml) publishes the macOS arm64 Electron desktop preview when a `v*` tag is pushed. It builds from the tagged commit, verifies the package, and then creates a GitHub Release. This page is for maintainers preparing that release; users should follow [installation](../getting-started/installation.md).

## Prepare and tag a release

`VERSION` is the product version source. Prepare a release in a clean checkout with the required [development tools](development.md):

1. Add the user-visible changes to `CHANGELOG.md` under `[Unreleased]`.
2. Run `make release-prepare` for the next UTC CalVer, or set an explicit version with `make release-prepare RELEASE_VERSION=2026.9.3`. Review the generated changes to `VERSION`, the desktop manifest and lockfile, native iOS/Android metadata, and the dated changelog section.
3. Run `make ci release-check` on macOS and complete the relevant app-level acceptance. Commit the reviewed release change and land it on `main` through the normal review process.
4. From that clean release checkout, run `make tag-release`. It validates the version and release notes, creates an annotated tag, and prints the exact push command. Push that tag when publication is authorized.

`make tag-release` does not push anything and rejects a dirty tree or an existing local tag. The workflow independently requires the tag commit to be an ancestor of `origin/main`, all generated product versions to match the tag, and a nonempty matching changelog section. A successful local build does not bypass these checks.

## Product versions and compatibility

Product releases use `YYYY.M.D`: the UTC release year, month, and day, without leading zeros. There is at most one final release per UTC day; published tags are never reused. A prerelease can use a suffix such as `2026.9.21-rc.1`; the workflow marks suffix-bearing tags as GitHub prereleases.

Use `make version-sync` to synchronize metadata from `VERSION`, and `make version-check` to verify it. Do not hand-edit generated desktop or native version fields. Preparing a final release from its matching prerelease carries forward the candidate's notes together with new unreleased notes.

CalVer describes release recency, not Extension API, service, remote protocol, or storage compatibility. Review those contracts and migration requirements separately. Calendar tags are also not Go module major versions: the supported source installation command for the CLI is `make install` from a checkout, not a release-tagged `go install ...@latest` promise.

## Build and verification

The macOS job checks Go modules, formatting, vet, cross-builds, and uncached Go tests. It installs locked desktop and shared Web dependencies, explicitly installs and verifies the Electron binary, and runs desktop and release-signing tests. Build scripts consume the committed module manifests rather than repairing them during release.

The local `make release-check` also runs the macOS native Computer Use helper tests. The tagged release build itself excludes that feature: `WUU_SKIP_CUA_MAC=1` and `WUU_ENABLE_CUA_MAC=0` prevent its inclusion, and the packaged-app verifier rejects either CUA executable if present.

All production desktop builds hide account, remote-control, and subscription-dashboard UI, including local packages built outside this workflow. These features remain development-only even if `VITE_ENABLE_ACCOUNT` or `VITE_ENABLE_REMOTE_CONTROL` is set to `true`; the workflow also sets both to `false`. The subscription page is also excluded from extension-provided settings navigation, and direct navigation falls back to model services. Existing model connections remain available. Source-development capabilities should not be presented as features shipped by this build.

Before publication, the workflow verifies the app's signature and bundle identity, required executables, absence of CUA helpers, and clean packaged-core version. It also runs `hdiutil verify` on the DMG and `unzip -t` on the ZIP. These checks establish packaging properties; they do not replace opening the app and testing affected user flows.

The DMG window layout is configured in `desktop/package.json`. Its background
source is `desktop/build/dmg-background.svg`; after editing it, run
`npm --prefix desktop run dmg-background:generate` on macOS and commit both the
1x and 2x PNGs. Packaging consumes those committed images and combines them into
a HiDPI TIFF, without requiring artwork regeneration on release machines. Keep
the artwork dimensions and icon positions aligned with the DMG configuration.
Open the built DMG in Finder to check text, icon labels, clipping and scrolling
at standard and Retina resolution, then verify copying to Applications and
launching the app. A background-only preview does not validate the Finder layout.

## Signing configuration

The macOS preview uses certificate-free ad-hoc signatures. No Apple Developer membership, signing certificate, or repository signing secrets are required. GitHub supplies `GITHUB_TOKEN` for publication. With `CSC_IDENTITY_AUTO_DISCOVERY=false`, the custom signer seals nested code and the outer app without selecting a local certificate. See the [signing reference](../../../desktop/scripts/RELEASE-SIGNING.md) for optional certificate-backed local builds.

This is an unsigned preview, not Apple Developer ID signing or notarization. Ad-hoc signatures do not establish publisher identity or guarantee permission retention across updates. Users may need **System Settings → Privacy & Security → Open Anyway**, or the app-specific quarantine workaround in the [installation guide](../getting-started/installation.md), after verifying the official download. Users do not need to import a certificate or disable system security globally.

## Published artifacts

The release contains `wuu-<version>-mac-arm64.dmg`, `wuu-<version>-mac-arm64.zip`, and any matching `.blockmap` files produced by electron-builder. The app includes its own private `wuu-core` subprocess. The workflow does not publish standalone CLI archives, Windows installers, native phone apps, or CUA helpers.

A separately source-installed CLI can coexist with the app and may have a different version. When checking a desktop release, inspect the bundled core and installed app rather than assuming `wuu --version` on `PATH` identifies the desktop build.
