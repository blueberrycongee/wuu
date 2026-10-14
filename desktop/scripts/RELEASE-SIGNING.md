# macOS release signing

The [release guide](../../docs/en/project/release.md#signing-configuration)
defines the required GitHub Secrets, verification checks, and publication policy.
`npm run release:desktop` requires Developer ID signing and notarization; it
never falls back to ad-hoc or self-signed identities. `pack:mac` and `dist:mac`
are ad-hoc local development commands, not distributable releases.

## Local release verification

Provide the pinned `WUU_RELEASE_SIGN_ID`, `WUU_RELEASE_TEAM_ID`, and
`WUU_RELEASE_KEYCHAIN` containing the certificate and private key. Set
`APPLE_API_KEY` to the protected team API key file, with `APPLE_API_KEY_ID` and
`APPLE_API_ISSUER`. Do not put private material or environment files in Git.

Run `npm run release:desktop` from `desktop`. The command builds the arm64 app,
signs nested code with hardened runtime and secure timestamps, notarizes and
staples the app, and packages DMG and ZIP. It also notarizes and staples the DMG,
then verifies the applications extracted from both final artifacts. A successful
run writes `release/verification.json` with checks and final artifact hashes.

## Acceptance

Run `npm run test:release-signing` to check missing-credential rejection and
development signing integrity. The nonpublishing Release CI run is the real
Developer ID and notarization gate; fixture tests do not replace it.

On a clean supported Mac, download a verified artifact, move the app to
Applications, and launch through Gatekeeper. Test a second build from the same
team and check session data and permissions. Migrating from an old ad-hoc build
may require permission authorization again. Never reset TCC as an upgrade step.
Record the macOS versions tested; signature checks alone do not establish
permission retention or correct application behavior.
