# Contributing to wuu

Thanks for your interest in contributing. This document explains how to report
issues, suggest features, and submit code changes.

By participating, you agree to follow our [Code of Conduct](./CODE_OF_CONDUCT.md).
This project is released under the [MIT License](./LICENSE); by contributing you
agree your contributions will be licensed under the same terms.

## Reporting bugs

Open a [bug report](.github/ISSUE_TEMPLATE/bug_report.md) and include:

- A clear, descriptive title
- Steps to reproduce, with the exact command or UI flow
- Expected vs actual behavior
- Environment: OS, Go and Node versions, output of `wuu --version`
- Relevant logs (redact API keys and any credentials first)

## Suggesting features

Open a [feature request](.github/ISSUE_TEMPLATE/feature_request.md) describing
the problem you are trying to solve, not just the solution. Small bug fixes,
tests, documentation, accessibility improvements, and focused refactors can go
straight to a pull request. Open an issue first for new product flows, protocol
changes, large dependency or architecture changes, security model changes, and
work that affects compatibility across shells.

## Submitting code changes

### Development setup

- Go: use the version in `go.mod`
- Node.js: use Node 22 or newer; `.node-version` matches CI
- Install repository dependencies: `make setup`
- Start the desktop development path: `make dev`
- Run the cross-platform local gate: `make ci`
- On macOS, also run `make build-macos` when desktop packaging parity with CI is required

See [the development guide](docs/en/project/development.md) for component commands,
supported platforms, CI checks, architecture boundaries, and restart behavior.
Shared project constraints and agent execution instructions are maintained in
[`AGENTS.md`](AGENTS.md). Read its design and visual acceptance requirements
when contributing UI changes.

The current supported build targets are macOS and Linux for the CLI and arm64
macOS for the desktop preview. Mobile and remote control are experimental and
do not yet have a stable public mobile release.

### Onboarding UI preview

After `make setup`, run `npm --prefix desktop run dev:onboarding` to open the
real first-run UI in a standalone Electron window. It starts with clean preview
data, without building or starting the Go core or native helpers. There is no
product preload bridge, and it does not read your Wuu settings, discover local
credentials, or save choices. Use dummy values in model fields.

Renderer edits update through Vite. Reload the window (Cmd+R / Ctrl+R) to start
the flow again; exit the preview and stop the command with Ctrl+C when finished.
Electron uses a temporary profile, removed on normal exit. This tool is for UI
iteration, not end-to-end verification of authentication or persistence. The
entry points live in `desktop/dev/onboarding/`, outside the product build.

### AI-assisted contributions

AI-assisted pull requests are welcome. The author remains responsible for the
design, license compliance, security, and correctness of every line. Review the
complete diff, remove unrelated generated changes, and run the relevant real
tests. Do not submit raw model output or claim checks passed when they were not
run. Add a test only when it protects an important contract or a bug that has
already escaped, and make that test exercise observable behavior. Do not add
tests that read stylesheet source, pin CSS declarations,
snapshot generated theme matrices, or quote prompt phrasing.
`make test-policy-check` rejects new stylesheet-source tests.

### Commit conventions

- One logical change per commit; do not bundle unrelated edits
- Commit messages in English, conventional-commits style:
  - `feat(scope): ...` for new features
  - `fix(scope): ...` for bug fixes
  - `chore: ...` for housekeeping
  - `docs: ...` for documentation only
  - `refactor(scope): ...` for refactors with no behavior change
- Reference the relevant issue or design doc in the body when applicable

### Versions and release notes

- Add user-visible changes to the `[Unreleased]` section of `CHANGELOG.md`.
- Do not edit product package versions by hand; `VERSION` is synchronized with
  `make release-prepare RELEASE_VERSION=<version>`.
- Product releases use UTC CalVer `YYYY.M.D`, without leading zeros for month or day.
  Check API, protocol, configuration, and storage compatibility separately; product
  version increments do not express compatibility.
- Only maintainers create release tags. See [the release guide](docs/en/project/release.md).

### Pull request process

1. Branch from `main`; do not touch unrelated files in the same PR
2. Run the relevant component commands, then `make ci` when practical
3. Open the PR using the [pull request template](.github/PULL_REQUEST_TEMPLATE.md)
4. Address review feedback with additional commits; avoid force-push after review starts
5. A maintainer will squash-merge on approval

## Documentation

- Published pages live under `docs/zh-cn/` and `docs/en/`; only pages listed in
  `docs/site.json` are rendered by the docs site. Every tracked file is public,
  including files outside the site navigation and binary metadata.
- Private architecture research and future plans live outside this public
  repository. Tracked documentation is limited to maintained product,
  development, protocol, security, and release contracts.
- The author changing a behavior updates its documentation, both languages,
  navigation, and links in the same change. Module READMEs explain local setup
  and link to the public contract rather than duplicating it. See
  [documentation maintenance](docs/README.md) for placement and checks.
- Keep one-off reports, raw model output, presentations and their generated
  PDF/PPT/image bundles outside the source repository. Public distribution uses
  an explicitly maintained release channel or a separate materials repository.
  Reusable evaluations follow [the evaluation policy](evals/README.md); keep
  disposable test output in ignored artifact directories, using synthetic data.
- Retain generated assets required by builds only with their source or generator
  and regeneration instructions. Keep licenses, third-party attribution,
  runtime prompts/skills, and useful test fixtures with the code they support.
  Historical documentation must identify its applicable version or retirement
  date and link to its replacement; otherwise remove it.
- Review text, screenshots, attachments and metadata for credentials, personal
  data and nonpublic sources before submission. Report suspected exposure via
  [SECURITY.md](SECURITY.md), without copying sensitive evidence into an issue.
  Deleting a file does not remove Git history or distributed copies; credential
  revocation and historical cleanup require a separate assessment.

## Project structure

- `cmd/wuu/` — CLI entry point and the `wuu exec` / `wuu app-server` subcommands
- `internal/` — Go core: agent runtime, providers, tool loop, sessions, config
- `desktop/` — Electron shell (renderer + main process)
- `packages/protocol/` — shared app-server protocol types
- `clients/core/` — remote-control client core
- `clients/native/` — active SwiftUI iOS and Jetpack Compose Android clients;
  see [build and validation status](clients/native/README.md) (Chinese)
- `clients/mobile/`, `clients/mobile-web/`, `clients/mobile-app/` — retired phone
  implementations; some shared Web code still participates in desktop builds
- `docs/` — Maintained user, protocol, and development documentation; see
  [`docs/README.md`](docs/README.md) for the index
- `desktop/dev/` — reusable, synthetic UI previews; not packaged product entries
