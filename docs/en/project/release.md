# Release

The [release workflow](../../../.github/workflows/release.yml) publishes the macOS arm64 Electron desktop preview when a `v*` tag is pushed. It builds from the tagged commit, verifies the package, and then creates a GitHub Release. This page is for maintainers preparing that release; users should follow [installation](../getting-started/installation.md).

## Prepare and tag a release

`VERSION` is the product version source. Prepare a release in a clean checkout with the required [development tools](development.md):

1. Add the user-visible changes to `CHANGELOG.md` under `[Unreleased]`.
2. Run `make release-prepare` for the next UTC CalVer, or set an explicit version with `make release-prepare RELEASE_VERSION=2026.9.3`. Review the generated changes to `VERSION`, the desktop manifest and lockfile, native iOS/Android metadata, and the dated changelog section. Then condense the dated section into the published release-note format below.
3. Run `make ci release-check` on macOS and complete the relevant app-level acceptance. Commit the reviewed release change and land it on `main` through the normal review process.
4. From that clean release checkout, run `make tag-release`. It validates the version and release notes, creates an annotated tag, and prints the exact push command. Push that tag when publication is authorized.

### Release-note format

The dated changelog section becomes the GitHub Release notes verbatim, so keep it short. `[Unreleased]` can stay detailed while work lands; `release-prepare` moves it into the dated section, where it is replaced with:

```markdown
## [2026.9.29] - 2026-09-29

### Contributors

@maintainer, @contributor

### Highlights

- One line per user-visible theme, at most about six bullets.
```

List the GitHub logins of authors of PRs merged since the previous tag (`gh pr list --state merged --search "merged:>=<previous tag date>"`), maintainer first. Group changes by user-visible theme instead of listing commits, name the most important fixes in one bullet, and omit test, CI, and refactor-only work. Leave out features hidden from production builds.

`make tag-release` does not push anything and rejects a dirty tree or an existing local tag. The workflow independently requires the tag commit to be an ancestor of `origin/main`, all generated product versions to match the tag, and a nonempty matching changelog section. A successful local build does not bypass these checks.

## Product versions and compatibility

Product releases use `YYYY.M.D`: the UTC release year, month, and day, without leading zeros. There is at most one final release per UTC day; published tags are never reused. A prerelease can use a suffix such as `2026.9.21-rc.1`; the workflow marks suffix-bearing tags as GitHub prereleases.

Use `make version-sync` to synchronize metadata from `VERSION`, and `make version-check` to verify it. Do not hand-edit generated desktop or native version fields. Preparing a final release from its matching prerelease carries forward the candidate's notes together with new unreleased notes.

CalVer describes release recency, not Extension API, service, remote protocol, or storage compatibility. Review those contracts and migration requirements separately. Calendar tags are also not Go module major versions: the supported source installation command for the CLI is `make install` from a checkout, not a release-tagged `go install ...@latest` promise.

## Build and verification

The macOS job checks Go modules, formatting, vet, cross-builds, and uncached Go tests. It installs locked desktop and shared Web dependencies, explicitly installs and verifies the Electron binary, and runs desktop and release-signing tests. Build scripts consume the committed module manifests rather than repairing them during release.

Both `make release-check` and the release workflow run the macOS native Computer Use helper tests. The release command builds and packages `wuu-cua-mac` and `wuu-cua-mac-pip`; the packaged app enables Computer Use from these bundled executables. Release verification requires both helpers, their matching architectures, signatures, hardened runtime, and secure timestamps. Screen Recording and Accessibility authorization remain macOS permission checks on the installed app.

Production desktop builds expose Phone access for native iOS pairing. The release workflow enables `VITE_ENABLE_REMOTE_CONTROL`; builders can set it to `false` to hide the entry. Account login remains development-only even if `VITE_ENABLE_ACCOUNT=true`, and the workflow sets it to `false`. The iOS app still requires a separate build and signing for device installation; enabling desktop pairing does not publish a mobile store release. Model connections and the subscription dashboard remain available in production. Project Agent requires the development-only `project_agent` build tag, which release builds omit. Source-development capabilities should not be presented as features shipped by this build.

Before publication, the workflow verifies the app's signature and bundle identity, required executables including both CUA helpers, and clean packaged-core version. It also runs `hdiutil verify` on the DMG and `unzip -t` on the ZIP. These checks establish packaging properties; they do not replace opening the app and testing affected user flows.

The DMG window layout is configured in `desktop/package.json`.
`desktop/scripts/generate-dmg-background.cjs` draws the background around the
icon positions in `dmg.contents`, using the face and colours in
`assets/app-icon-source.json` and the product's Blobatar agent renderer. After
changing any of these, run `npm --prefix desktop run dmg-background:generate`
and commit both the 1x and 2x PNGs.
Packaging consumes those committed images and combines them into a HiDPI TIFF,
without requiring artwork regeneration on release machines.

The background is a wordless drag cue on white: a slingshot beside the app
fires Wuu on a dotted arc that splits into its colourful agents, and they dive
into a block fort built around Applications. Finder does not scale the picture
when the window is resized; it anchors it at the top left and fills the rest
with white in Light Mode, so the picture stays white to its edges and no
artwork touches them.

Finder imposes four constraints on the artwork, which the script encodes. The
window takes the size of the 1x background, and `dmg.window` is ignored while a
background is set. Finder draws the picture below the title bar without
shrinking it, so a strip at the bottom as tall as the title bar (32 pt on macOS
26) stays hidden. Over a background picture, Finder draws icon labels in black
in both Light and Dark Mode, so the areas under the icons must stay light.
Selecting an icon draws a translucent box about 72 pt around its centre, so
artwork stays out of that box and its label rather than tucking behind an icon.

Open the built DMG in Finder to check icon labels, both selection states,
clipping and scrolling at standard and Retina resolution, then verify copying
to Applications and launching the app. A background-only preview does not validate the Finder layout.

## Signing configuration

`npm run release:desktop` requires a pinned Apple Developer ID Application identity,
hardened runtime, secure timestamps, and App Store Connect team API credentials.
Missing credentials or any signing, notarization, stapling, or Gatekeeper failure
stops the job before artifact upload or publication. Local `pack:mac` and `dist:mac`
remain ad-hoc development builds and are not release commands.

Configure these repository Actions Secrets. Never commit their values:

- `WUU_RELEASE_CERTIFICATE_P12`: base64-encoded P12 including its private key.
- `WUU_RELEASE_CERTIFICATE_PASSWORD`: P12 export password.
- `WUU_RELEASE_SIGN_ID`: certificate SHA-1 fingerprint.
- `WUU_RELEASE_TEAM_ID`: Apple developer team identifier.
- `APPLE_API_KEY_P8`: App Store Connect team API private key in PEM format.
- `APPLE_API_KEY_ID` and `APPLE_API_ISSUER`: that key's ID and team issuer ID.

Use a dedicated team API key with Developer access for notarization. The runner
imports the certificate into a temporary keychain without adding trust roots and
removes the keychain and API key in an `always()` cleanup step. Retain encrypted
backups outside the repository; GitHub Secrets cannot be read back.

### Public material and private credentials

| Material | Storage and publication |
| --- | --- |
| Workflow code, signing scripts, secret names, and placeholder examples | Commit to the public repository. |
| Verified DMG/ZIP files, checksums, and `verification.json` | Publish after the release gates pass. Check diagnostic logs for credentials and private paths before sharing them. |
| Signing certificate subject, Team ID, public key, and certificate fingerprint | Public metadata embedded in signed apps; the developer name on the certificate is visible to recipients. These values do not grant signing access. |
| P12/PFX exports containing private keys, their passwords, P8 API keys, keychains, and credential handoff archives | Keep outside Git and release assets. Use the local keychain or restricted private storage, and the designated Actions Secrets for CI. |

An encrypted P12 or a ZIP containing it is still private material. Base64 encoding
is not encryption. Ignore rules reduce accidental staging but do not protect a
file already committed or added with `git add -f`. Never paste credentials into
PR descriptions, issues, build logs, or public artifacts. See
[GitHub's secrets guidance](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets)
and [Apple's signing identity guidance](https://developer.apple.com/documentation/Xcode/sharing-your-teams-signing-certificates).

Reuse the configured Developer ID identity across releases; do not generate a new
certificate for each build. Re-exporting the same certificate with a new P12
password does not change its fingerprint. If replacing the CI export, update the
P12 and password secrets together before running the workflow. Certificate renewal
is a deliberate credential change: update the pinned fingerprint with the new
certificate, retain the team and bundle identity, and repeat signing, notarization,
and update acceptance before publishing. Stable signing does not by itself prove
that macOS permissions survive an update.

### Artifact verification

The app is notarized and stapled before creating DMG and ZIP archives. The final
DMG is then signed, notarized, and stapled. Verification mounts that DMG and
extracts that ZIP, checks both apps' Developer ID identity, runtime flags,
timestamps, ticket, Gatekeeper assessment, and version. `verification.json`
records the commit, version, checks, notarization submission ID, and final hashes.
These checks do not prove UI behavior or permission retention across updates.

App and DMG uploads return a submission ID before polling begins. The pipeline
records each ID and archive SHA-256 in `release/notarization/`, uploads the
submission receipt before polling begins, and retries transient
query failures against that same ID, never by uploading again. Five consecutive
network failures, a non-transient query failure, rejection, or a 30-minute polling
limit stops the job. The workflow saves the signed app before contacting Apple,
then saves the signed DMG, ZIP and update metadata before DMG notarization. These
immutable checkpoints include source commit, version and SHA-256 hashes and are
retained for 30 days. They contain no signing credentials.

Use **Re-run failed jobs**, or `gh run rerun <run-id> --failed`, to resume the same
run. Successful build jobs are not repeated. Failed app/DMG jobs download their
original signed checkpoint and the latest receipt from an earlier attempt of that
run. Archive hashes must match before receipt reuse. Accepted submissions skip
submission and polling; stapling, ticket validation and Gatekeeper still run.
Receipts are retained as `app-submission-<attempt>` / `dmg-submission-<attempt>`
before waiting, then `app-receipts-<attempt>` / `dmg-receipts-<attempt>` on success
or failure. Recovery uses the newest attempt, preferring its final receipt. If
an earlier Apple step started but no receipt survived, recovery stops for Apple
history reconciliation instead of assuming nothing was submitted. An upload with an unknown outcome is recorded before the
request and blocks automatic resubmission until Apple history is reconciled.
Expired checkpoints require a new build; do not attach an old receipt to new bytes.

After acceptance, App and DMG ticket stapling and validation retry transient
network failures up to three total attempts per operation, waiting 30 seconds
between attempts. This also covers validation of the apps extracted from the
final DMG and ZIP. Retries query the ticket for the same artifact without
resubmitting it for notarization. Other failures and exhausted retries stop the
build; ticket validation and Gatekeeper assessment must still pass.

Use the Release workflow's manual dispatch with an empty `release_tag` for
verification without publication. To finish an existing tag with newer release
tooling, dispatch from `main` with `release_tag=vYYYY.M.D`. The tag must resolve
to a commit on main, and its product versions must match. Product sources stay at
the tag; release tooling is checked out separately, so no source overlay dirties
the packaged core. Evidence records the product commit, not the tooling commit.
This explicit dispatch publishes after all gates pass and does not move the tag.
Tag pushes also publish. Validation runs create no Release.

To reuse signed bytes with newer tooling, also set `checkpoint_run` to a completed
Release run ID. The prepare job imports its furthest saved checkpoint: a signed
app skips building/signing, and a signed distribution also skips app notarization
and packaging. The source run must belong to this repository's Release workflow,
come from a tag push or manual dispatch, and use tooling from the history of
`main`. The checkpoint must match the selected product commit and version, bind
every consumed archive and update manifest, and contain the exact signed DMG
submitted to Apple. A mismatched or expired checkpoint fails rather than silently
rebuilding or mixing artifacts.

Imported checkpoints and the latest receipt are saved in the new run as
`resume-<attempt>-signed-app` or `resume-<attempt>-distribution` before any Apple
work. This permits recovery from a recovery run without depending on a hidden
chain of earlier runs. A newer receipt from the current run takes precedence.
Accepted submissions remain accepted. Ambiguous receipts, or missing receipts
after an earlier Apple step started, never cause a fresh upload automatically. Every recovery still runs the same ticket,
Developer ID, Gatekeeper, mounted-DMG and extracted-ZIP validation before
publication. Submission steps have a 12-minute limit, and polling/package or
polling/verification steps have a 40-minute limit within their 60-minute jobs,
leaving room to retain receipts and clean up credentials after a step failure.

`checkpoint_run` does not authorize publication by itself: `release_tag` retains
its existing meaning. For an older tagged product, provide its matching tag;
the run will publish only after the gates pass. A partially uploaded
draft can be resumed; an already-public Release is never overwritten.
See the [signing reference](../../../desktop/scripts/RELEASE-SIGNING.md).

## Automatic updates

Release builds embed an `app-update.yml` GitHub provider for `blueberrycongee/wuu`.
The desktop uses electron-updater 6 with stable releases only, no downgrades,
explicit download consent, and explicit restart consent. Development and ad-hoc
builds do not carry this configuration and never check for updates. Background
checks start 30 seconds after launch and repeat every six hours while idle. A
new release appears beside the account control at the bottom of the sidebar;
click to download, watch the progress ring, then click the ready indicator to
confirm restart. Failed downloads remain available for retry. Background checks
do not open dialogs. The native Wuu menu also displays download progress and
offers manual checks or restart. Restart first drains the same child-process
cleanup used by ordinary application quit.

The release verifier checks the embedded provider in both archives, and refreshes
the generated `*-mac.yml` file sizes and SHA-512 hashes after final signing and
stapling. The workflow uploads that metadata with the ZIP and DMG. A tagged run
creates a draft Release, uploads all assets, and only then publishes the Release
so clients cannot discover an incomplete update. Non-tag verification runs still
create no Release. Keep the bundle identity and Developer ID team consistent.

An older build without the updater requires one manual installation. A complete
installation acceptance test needs two signed versions and an update feed;
controller tests or a successful package build do not prove an installed upgrade.

## Published artifacts

The release contains `wuu-mac-arm64.dmg`, `wuu-mac-arm64.zip`, and matching ZIP `.blockmap` files produced by electron-builder. DMG blockmaps are excluded because signing and stapling modify the disk image after electron-builder creates its blockmap. The app includes its own private `wuu-core` subprocess. The workflow does not publish standalone CLI archives, Windows installers, native phone apps, or separate CUA downloads; CUA helpers are bundled inside the macOS app.

Artifact filenames omit the version; the app bundle, release tag, and update metadata retain it. GitHub download URLs include the release tag, so the same filename identifies a different asset in each release. Updates compare metadata versions and verify archive hashes, not filename dates. Older releases keep their original versioned filenames. The first update from a versioned filename may download the full ZIP if the previous blockmap is not cached; archive hash verification still applies.

A separately source-installed CLI can coexist with the app and may have a different version. When checking a desktop release, inspect the bundled core and installed app rather than assuming `wuu --version` on `PATH` identifies the desktop build.

### Read-only app snapshot helper

macOS builds separately compile and package `Wuu App Snapshot.app` for explicit one-window attachments. A small private Node-API module pins target metadata synchronously at shortcut dispatch; the helper validates that exact target before reading content. Neither component has a Computer Use input or MCP interface. The bridge’s pinned Node-API headers dependency retains its complete upstream MIT notice beside the packaged binary. The packaged-app verifier checks both snapshot binaries, matching app architectures, and nested signatures alongside the bundled Computer Use helpers. The macOS CI job compiles the helper and bridge, verifies signatures/architecture and bridge loading in Node/Electron, and reads permission status without capturing a screen or requesting permissions. Real macOS permission, revocation, shortcut, and window-capture acceptance is a separate gate.
