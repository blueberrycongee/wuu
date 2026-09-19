# Development

This is the human-facing guide to building and checking the repository. Agent
runtime instructions live in `AGENTS.md`; contributors should not need that file
to discover the normal development commands.

## Requirements

- Go version from `go.mod`
- Node.js 22 or newer (`.node-version` selects the CI baseline)
- npm
- macOS with Xcode/Swift for the Electron package and native CUA helper

The Go CLI and core build on macOS and Linux. The current desktop release is an
arm64 macOS preview. Mobile and remote-control clients are under active
development and are not published as a stable mobile release.

## Setup and daily commands

Run from the repository root:

| Command | Purpose |
|---|---|
| `make setup` | Install locked npm dependencies for desktop, clients, protocol, and docs site |
| `make dev` | Start the real Electron development path |
| `make check` | Check repository metadata, test policy, Go modules/format/vet, and TypeScript types |
| `make test` | Run Go, desktop, plugin SDK, remote-core, and legacy Web/Expo client tests |
| `make build` | Build the Go CLI, Electron renderer/main, and legacy Web/Expo client bundles |
| `make ci` | Run the cross-platform check, test, and build gate |
| `make release-check` | Check release versions and run the Go core and desktop test gates |

Component commands are available when a full run is unnecessary:

```bash
make check-go test-go build-go
make check-desktop test-desktop build-desktop
make check-clients test-clients build-clients
make test-native
make build-macos
```

`make test-native` and `make build-macos` require macOS. The desktop development
launcher builds and starts `wuu app-server` from the current Go source. After
changing Go or Electron main-process code, fully restart `make dev`; the running
subprocess and Electron main process are not hot-reloaded.

`make test-native` tests the desktop CUA helper, not the phone apps. Active phone
development lives in [clients/native](../../../clients/native/README.md) (Chinese),
with SwiftUI on iOS and Jetpack Compose on Android. Use
`bash clients/native/verify.sh all` for its isolated integration checks; see that
README for PostgreSQL, Xcode and Android prerequisites and the remaining release
acceptance work. The older Expo, WebView and Capacitor phone implementations
stopped development on 2026-09-12. Existing client gates and the desktop's shared
Web bundle still consume some of that code; they do not validate the native apps.

For documentation changes, run `make docs-policy-check build-docs`; see
[documentation maintenance](../../../docs/README.md). For reusable renderer
previews and scroll treatment, see [Desktop UI maintenance](desktop-ui.md).

## CI checks

Except for docs-only changes, pull requests and pushes to `main` run:

- **Repository check:** versions, eval records, documentation policy, theme contracts, and merge-gate test policy;
- **Go check:** module consistency, format, vet, Windows/macOS cross-builds and tests; the standalone CLI build also runs on `main`;
- **Desktop check:** install, typecheck, unit tests, and Electron build;
- **Clients check:** protocol/plugin SDK/core/legacy client typecheck, client tests, and Web/Expo bundles;
- **macOS native check:** Swift/native tests on pull requests and a directory-packaged Electron app on `main`;
- **Windows native check:** Windows process/sandbox boundaries and Desktop typecheck on pull requests, with unpacked packaging on `main`. The full Desktop unit suite already runs on Ubuntu.

Tagged releases add self-signed macOS DMG/ZIP verification. GitHub Releases do not
publish standalone CLI archives. See the [release guide](release.md).
The separate documentation workflow checks policy and builds the site for changes
to docs, the site or landing pages; it deploys the site on `main`.

## Product boundaries

- `internal/` and `cmd/wuu/` are the reusable Go core and app-server.
- `desktop/` is the Electron shell and owns native UI, IPC, and packaging.
- `packages/protocol/` is the shared client protocol type source.
- `clients/core/` is the UI-free remote client.
- `clients/native/` contains the active native iOS and Android apps.
- `clients/mobile/`, `clients/mobile-web/`, and `clients/mobile-app/` retain retired
  phone implementations, not the current phone feature roadmap.

Keep Electron APIs out of the Go core. New shells should spawn `wuu app-server`
instead of forking or importing the core.
