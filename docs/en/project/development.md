# Development

Run Wuu from a source checkout to change the Go core, Electron desktop, plugins, or clients. Commands below run from the repository root. Contribution and review rules are in [Contributing](../../../CONTRIBUTING.md).

## Set up the checkout

Use the Go version in [`go.mod`](../../../go.mod), Node from [`.node-version`](../../../.node-version), and npm. Desktop packages require Node 22 or newer. macOS packaging and the native Computer Use helper also require macOS with Xcode/Swift tooling.

```bash
make setup
make dev
```

`make setup` installs locked dependencies for the desktop, shared client core, retained Web/Expo clients, plugin SDK, protocol package, and docs site. It does not install the native phone toolchains or provision remote services.

`make dev` runs the desktop launcher. It builds the shared Web assets, native helper where applicable, and current Go core and plugin helpers before starting Electron. The app uses that checkout's private `wuu-core`, not a separately installed `wuu` on `PATH`. Renderer changes use Vite updates; restart the launcher after changing Go, native helpers, or process-startup code so the running processes use the new build.

For CLI-only development:

```bash
make build-go
./bin/wuu --help
```

`make install` installs the CLI from the checkout. The current release workflow packages only the macOS arm64 desktop preview; Windows CI and packaging scripts do not imply a published Windows release. See [installation](../getting-started/installation.md) for user-facing availability.

## Choose the relevant checks

| Command | What it checks or builds |
| --- | --- |
| `make check-go` | Module consistency, formatting, vet, Windows/macOS cross-builds |
| `make test-go` | Go tests for the CLI, core, Go plugin SDK, bundled plugins, and prompts |
| `make check-desktop test-desktop` | Desktop TypeScript and launcher/unit tests |
| `make build-desktop` | Shared Web assets and Electron main/preload/renderer bundles; not a packaged app |
| `make check-clients test-clients build-clients` | Protocol/SDK/client types, SDK/client tests, and retained Web/Expo bundles |
| `make test-native` | Desktop macOS Computer Use helper tests, not phone app tests |
| `make build-macos` | Core/helpers, desktop build, and directory-packaged macOS app |
| `make docs-policy-check check-docs build-docs` | Documentation policy, site diagnostics, and generated site/link checks |

`make check`, `make test`, and `make build` aggregate the corresponding repository targets. `make ci` runs all three; it does not include native phone verification, macOS packaging, or the docs-site build. `make release-check` adds version validation, uncached Go tests, desktop tests, and the macOS native helper gate. Use the [release guide](release.md) for publication requirements.

Type checks, unit tests, and successful bundles are different evidence from a working app. For UI changes, inspect the affected rendering and interactions using the [desktop UI guide](desktop-ui.md). For plugin changes, also exercise actual tool calls or rendered contributions; package validation alone does not do that.

`npm --prefix desktop run test:renderer-recovery` intentionally crashes hidden
Chromium renderers to check recovery, retry limits, window isolation, and IPC.
It uses a temporary profile and synthetic content, not your Wuu data. Run it in
a graphical desktop session; it does not validate native dialog appearance or
packaged-app behavior.

## Native phones and remote services

The active phone implementations are SwiftUI on iOS and Jetpack Compose on Android in [`clients/native`](../../../clients/native/README.md) (Chinese). Their dedicated verification command is:

```bash
bash clients/native/verify.sh all
```

Use `ios` or `android` to select one platform. The script starts an isolated PostgreSQL-backed test environment, builds test hosts, and runs platform tests and builds. Follow the native README for PostgreSQL, Xcode, Java, and Android SDK prerequisites. Passing these checks is not real-device or release acceptance.

The older `clients/mobile`, `clients/mobile-web`, and `clients/mobile-app` phone implementations are retired. Some remain in shared Web builds and repository checks; passing those gates does not validate the native apps. Account and relay deployment is separate from local desktop setup; see [remote access](../automation/remote.md).

## CI coverage

[The main CI workflow](../../../.github/workflows/ci.yml) runs repository metadata checks, Go checks/tests, desktop checks/tests/builds, and SDK/client checks/tests/builds. It skips changes confined to `docs/` and `docs-site/`. Go CI supplies PostgreSQL for database-backed coverage.

macOS pull requests test the native helper; pushes to `main` also package a desktop directory. Windows runs selected native process/sandbox tests and desktop type checking, with unpacked packaging on `main`. The full desktop unit suite runs on Ubuntu. These jobs cover different boundaries rather than repeating the same full suite on every OS.

[The documentation workflow](../../../.github/workflows/docs.yml) checks policy and builds the site when documentation, site, landing, or relevant build files change. Pull requests build without deployment; `main` builds deploy to GitHub Pages. Tagged product releases use a separate workflow and do not publish standalone CLI archives.

## Code boundaries

`cmd/wuu` and `internal` contain the CLI and Go core. `desktop` owns Electron main/preload, renderer UI, IPC, and packaging. `packages/protocol` holds shared client protocol types; `clients/core` implements UI-free remote behavior. The plugin SDKs and bundled implementations live in `packages/plugin-sdk`, `packages/plugin-go`, and `plugins`.

Keep Electron APIs in the desktop shell. A new shell should communicate with `wuu app-server` through its [protocol](../integrations/app-server-protocol.md), rather than importing desktop internals or creating a separate core. Update both language versions of affected public docs with behavior changes; [documentation maintenance](../../../docs/README.md) describes placement and checks.
