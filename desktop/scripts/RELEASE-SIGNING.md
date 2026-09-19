# macOS preview release signing

GitHub Releases distribute a DMG and ZIP for manual replacement of
`/Applications/wuu.app`. The current public preview contains the private core and
excludes CUA (`WUU_SKIP_CUA_MAC=1`). See the [release contract](../../docs/en/project/release.md).
No launch daemon, privileged installer, user certificate import, or per-version
helper directory is installed.

## One-time maintainer setup

Create a dedicated, long-lived **Code Signing** self-signed identity in Keychain
Access (Certificate Assistant → Create a Certificate). Export that identity,
including its private key, as a password-protected PKCS#12 (`.p12`) archive. On a
local signing Mac, trust this dedicated certificate for Code Signing in Keychain
Access. CI configures temporary code-signing trust on its ephemeral runner. Keep an
encrypted backup outside the repository. Do not reuse the development certificate,
regenerate the identity each release, or distribute its private key to users.

Configure these repository Actions secrets using `gh secret set`:

- `WUU_RELEASE_CERTIFICATE_P12`: base64 of the exported archive.
- `WUU_RELEASE_CERTIFICATE_PASSWORD`: its export password.
- `WUU_RELEASE_SIGN_ID`: the certificate's 40-character SHA-1 fingerprint, as
  reported by `security find-identity -p codesigning`. This is an identity selector,
  not a checksum for release downloads.

The import step creates an ephemeral runner keychain and allows `codesign` to use
its private key. Code-signing trust is installed only on the signing runner;
recipient Macs do not import the certificate. The release job deletes the archive
immediately and removes the temporary trust and keychain in an `always()` step. Missing credentials
fail the release instead of silently creating a new identity or falling back to
ad-hoc signing. Preserve the same certificate and key between runner instances.

For a local release build, import the same identity into a local keychain and set
`WUU_RELEASE_SIGN_ID` (and `WUU_RELEASE_KEYCHAIN` if using a dedicated keychain),
then run `npm run release:desktop` from `desktop`. `npm run pack:mac` without the
identity is available for local ad-hoc packaging checks only.

The electron-builder custom signer signs nested code and the outer app with the
same identity. `verify-mac-release.cjs` checks the outer signature, expected leaf
certificate, stable bundle ID, executable permissions, and the absence of CUA
helpers when `WUU_SKIP_CUA_MAC=1`. For a CUA-enabled local build it instead checks
both physical helper copies. Self-signing is not Developer ID signing or notarization; Gatekeeper
may still require Open Anyway.

## Signing regression test

Run `npm run test:release-signing`. On GitHub Actions it creates, imports, trusts,
and removes a temporary test identity, without using production secrets. Locally,
set `WUU_RELEASE_SIGN_ID` to an existing code-signing identity to exercise changed
builds without modifying trust settings; otherwise that integration case is skipped.
The test verifies the second build satisfies the first build's designated requirement.

## CUA-enabled local build acceptance

This checklist applies only to separately built CUA-enabled apps, not the current
GitHub Release. Test on a clean supported Mac with no development certificate installed:

1. Download the DMG, install to `/Applications`, open through Gatekeeper, and
   authorize Accessibility and Screen Recording as requested by actual tasks.
2. Exercise semantic observation, screenshot, input, and live preview using a
   configured BYOK model. Record which app entries macOS assigns the permissions to.
3. Quit with a live preview and running task; verify core and native helper
   processes exit and staged windows are restored.
4. Download a second build with changed app and helper code, signed with the same
   identity. Replace the app in Finder, restart, and repeat the tasks. Check for
   unexpected prompts, duplicate entries, stale processes, and stale executables.
5. Repeat after reboot and test migration from an older ad-hoc build. That first
   identity transition may require authorization again. Do not reset TCC as an
   upgrade step; that would destroy the permissions being tested.

A passing code-signature check does not prove TCC permission retention. Record the
macOS versions tested. Certificate replacement, OS changes, user revocation, and
historical TCC entries can require reauthorization. Investigate a reported old
entry separately from a running process or an old app bundle; never delete all
matching apps or user data automatically.
