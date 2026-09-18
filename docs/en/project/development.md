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
| `make test` | Run Go, desktop, remote-core, and mobile tests |
| `make build` | Build the Go CLI, Electron renderer/main, and mobile web export |
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

## CI checks

Pull requests and pushes to `main` run:

- **Repository check:** versions, eval records, and merge-gate test policy;
- **Go check:** module consistency, format, vet, tests, and CLI build;
- **Desktop check:** install, typecheck, unit tests, and Electron build;
- **Clients check:** protocol/core/mobile typecheck, client tests, and mobile web export;
- **macOS native check:** Swift/native tests on pull requests and a directory-packaged Electron app on `main`;
- **Windows native check:** Windows process/sandbox boundaries and Desktop typecheck on pull requests, with unpacked packaging on `main`. The full Desktop unit suite already runs on Ubuntu.

Tagged releases add unsigned macOS DMG/ZIP verification. GitHub Releases do not
publish standalone CLI archives. See the [release guide](release.md).

## Product boundaries

- `internal/` and `cmd/wuu/` are the reusable Go core and app-server.
- `desktop/` is the Electron shell and owns native UI, IPC, and packaging.
- `packages/protocol/` is the shared client protocol type source.
- `clients/core/` is the UI-free remote client.
- `clients/mobile/` is the Expo mobile shell.

Keep Electron APIs out of the Go core. New shells should spawn `wuu app-server`
instead of forking or importing the core.
