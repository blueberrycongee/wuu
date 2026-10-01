# Development

Run Wuu from a source checkout to change the Go core, Electron desktop, plugins, or clients. Commands below run from the repository root. Contribution and review rules are in [Contributing](../../../CONTRIBUTING.md).

## Set up the checkout

Use the Go version in [`go.mod`](../../../go.mod), Node from [`.node-version`](../../../.node-version), and npm. Desktop packages require Node 22 or newer. macOS packaging and the native Computer Use helper also require macOS with Xcode/Swift tooling.

```bash
make setup
make dev
```

Development uses `.wuu-dev/` in the checkout as its Wuu data directory, separating
configuration and runtime state from the installed app and other checkouts.
To intentionally use existing data, set an absolute `WUU_HOME`, for example
`WUU_HOME="$HOME/.wuu" make dev`. Shared-data dogfooding allows the development
build to change installed-app data; older builds may reject newer settings.
The override is also forwarded through the macOS LaunchServices launcher.

`make setup` installs locked dependencies for the desktop, shared client core, retained Web/Expo clients, plugin SDK, protocol package, and docs site. It does not install the native phone toolchains or provision remote services.

`make dev` runs the desktop launcher. It builds the shared Web assets, native helper where applicable, and current Go core and plugin helpers before starting Electron. The app uses that checkout's private `wuu-core`, not a separately installed `wuu` on `PATH`. Renderer changes use Vite updates; restart the launcher after changing Go, native helpers, or process-startup code so the running processes use the new build.

Project Agent is experimental and disabled in default and release builds. It
has no user setting to enable it. Existing project conversations remain readable,
but cannot run or recover queued work in these builds. To develop the feature,
run `npm --prefix desktop run dev -- --project-agent`, or build the CLI with
`go build -tags project_agent -o bin/wuu ./cmd/wuu`. Run its behavioral suite with
`go test -tags project_agent ./internal/appserver`; ordinary Go tests cover the
disabled release behavior. Packaged releases must omit this build tag.

`npm --prefix desktop run test:e2e:project-agent` checks the renderer against
absent, disabled and enabled backend capabilities, saving screenshots and results
under `desktop/out/e2e/project-agent-gate/`.

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

### Streaming renderer diagnostics

Run the synthetic long-answer fixture after installing desktop dependencies:

```sh
(cd desktop && ./node_modules/.bin/electron scripts/streaming-render-perf.cjs)
```

On headless Linux, add `--no-sandbox --ozone-platform=headless` before the script.
The fixture uses production React, the real Markdown renderer and CSS, and no
inference or accounts. It measures synchronous tail commits after 25, 100 and
400 completed paragraphs, plus a separate V8 sampled-allocation pass. It checks
that completed DOM nodes survive and all appended text appears. These diagnostic
measurements exclude provider cadence and physical paint latency; they are not CI
timing gates.

Results and disposable profile files go to `desktop/out/streaming-performance/`;
set `WUU_STREAM_PERF_OUTPUT` to keep separate runs. Set
`WUU_STREAM_PERF_BASE_REF=<commit>` to build just the streaming component from an
earlier revision against the same fixture and dependencies. Alternate baseline
and candidate runs, and compare raw samples and source/bundle hashes in
`results.json`; the checkout commit alone does not identify uncommitted source.

### Session switch performance guard

After installing dependencies, build the core and desktop, then run the real
Electron/main/preload/Go fixture in a graphical session:

```sh
mkdir -p .tmp/performance
go build -o "$PWD/.tmp/performance/wuu-core" ./cmd/wuu
(cd desktop && ./node_modules/.bin/electron-vite build)
WUU_DESKTOP_CORE="$PWD/.tmp/performance/wuu-core" WUU_SWITCH_CHECK_BUDGET=1 desktop/node_modules/.bin/electron desktop/scripts/session-switch-e2e.cjs
```

The fixture opens disposable synthetic conversations without inference. It
records native-click-to-content and draft/send-readiness frame timings, then
counts work through background resume completion and two more animation frames.
Frame opportunities do not prove physical presentation; readiness includes the
input probe's IPC overhead. Initial visits, repeats, pool churn, and archive
blocking remain separate. Wall-clock P50/P75 are diagnostic, never CI gates.

CI checks repeated large-fixture switches against the resume-call, layout, and
style-recalculation ceilings in
[`session-switch-budget.json`](../../../desktop/scripts/session-switch-budget.json).
These counters include the observer and input probe. The ratchet rejects higher
ceilings, removed counters, or a changed workload. It uploads raw results, a
report, logs, and a final synthetic screenshot as `session-switch-evidence`.

For diagnostics, omit `WUU_SWITCH_CHECK_BUDGET` and set `WUU_SWITCH_TURNS=3000`,
`WUU_SWITCH_ROUNDS`, or `WUU_SWITCH_INIT_DELAY_MS=600`. `WUU_SWITCH_TRACE=1`
records a Chromium trace with action/content/interactive marks; do not pool
traced runs with timing baselines. `WUU_SWITCH_OUTPUT` selects the evidence
directory, excluding the temporary profile and database. `WUU_SWITCH_MAIN`
selects another built main bundle and its adjacent preload/renderer for A/B
checks. Results record the loaded artifact hashes; the checkout commit alone
does not identify an externally selected build.

For subscription-history navigation diagnostics, set
`WUU_SWITCH_SUBSCRIPTION_TURNS=30000` and omit `WUU_SWITCH_CHECK_BUDGET`.
This adds about 2 GiB of disposable history and two subscription services with
isolated credentials and disabled external engines. It checks cross-project new
drafts while an opt-in statistics response is held at IPC, then after an injected
IPC failure, and verifies the real backend's request attribution and token totals.
`results.json` includes the separate startup-to-conversation frame measurement,
`subscriptionResults`, database size, observed process
spawns, navigation RPC timings, and statistics response timings before the gate.
These are RPC envelopes, not isolated SQL timings. A first project visit is not
necessarily a cold process start; keep those measurements separate. The fixture
also saves `subscription-navigation.png`. The held response tests navigation
independence, not a database lock or an account-service outage.

### End-to-end journey diagnostics

The same fixture always records startup through a restored conversation and a
native typed draft with Send enabled for two animation frames. Startup begins at
main-bundle import, after synthetic data seeding, so it excludes the Electron
executable launch. Use fresh fixture profiles and alternate independently built
baseline and candidate bundles. Filesystem caches are not flushed.

Set `WUU_SWITCH_PACED_STREAM=1` to send a real composer message through the
production preload, main process and Go core to a local synthetic SSE provider.
It streams formatted paragraphs, code and tables in 256-byte chunks with 16 ms
absolute-deadline pacing. The fixture verifies persisted text, native input,
first and final rendered response markers, and an unsent draft typed during
streaming. `paced-stream-results.json` includes raw frame gaps, provider write
times, renderer event times, long tasks, DOM mutations, CDP work/heap metrics and
Electron process CPU/memory snapshots. These are separate clock domains: compare
intervals within each domain, not raw renderer and host timestamps. Process
snapshots cover Electron, not Go-core CPU or total process-tree memory. Heap
snapshots are ungc'd observations, not allocation counts or leak proof.

For focused attribution, add `WUU_SWITCH_STREAM_ONLY=1`. It retains startup,
opens long conversations 1 then 5 natively, and checks the snapshot protocol
before streaming; the recorded cached panes should match the full journey's
0/1/5 population. Other switch/churn scenarios are omitted and are not claimed
as coverage. Keep this workload separate from full-journey results.
`WUU_SWITCH_BUILD_COMMIT` identifies the selected UI build, and
`WUU_SWITCH_CORE_BUILD_COMMIT` identifies a separately selected core for hybrid
UI/core comparisons. Both executable/bundle hashes remain recorded. Retained
builds outside a Git checkout need the explicit UI commit; their source-change
state is null, so retain their verified build manifest alongside the results.

Typing stays at the fixed one-third provider-write point. Results record its
zero-based chunk index, emitted/received character progress, rendered text size,
renderer input/frame offsets, host dispatch-to-frame envelope, and overlapping
long tasks. These distinguish event handling from host/IPC scheduling and stream
phase without changing the trigger to favor a result. `WUU_SWITCH_CPU_PROFILE=1`
and `WUU_SWITCH_TRACE=1` now include paced streaming. Such outputs are labeled
`profileOnly`; analyze them separately, never as ordinary timing samples.

For a populated sidebar, add `WUU_SWITCH_SIDEBAR_THREADS=1500` (30 additional projects) or
`5000` (50 additional projects). These are metadata-only synthetic histories with pinned,
archived, scratch and legacy cwd-associated sessions; they do not simulate live
running processes. This opt-in workload is separate from the existing work-count
budget and cannot run with `WUU_SWITCH_CHECK_BUDGET=1`.

For each baseline/candidate run, use the same final harness, fixture counts,
window size, dependency versions and machine, with matching full desktop and Go
builds selected by `WUU_SWITCH_MAIN` and `WUU_DESKTOP_CORE`. Record at least
three alternating pairs and retain every raw result, log and artifact hash.
The scoped CI comparison retains an initial warmup pair separately before those
three pairs; do not pool warmup samples with measured results.
Do not pool initial opens with repeats or traced runs with untraced runs.
Headless Linux can use `--no-sandbox --ozone-platform=headless`; its frame cadence
is a property of that rig. Two frames are paint opportunities, not physical
presentation or a claim of 120 Hz. Wall-clock timings remain informational.

## Native phones and remote services

The active phone implementations are SwiftUI on iOS and Jetpack Compose on Android in [`clients/native`](../../../clients/native/README.md) (Chinese). Their dedicated verification command is:

```bash
bash clients/native/verify.sh all
```

Use `ios` or `android` to select one platform. The script starts an isolated PostgreSQL-backed test environment, builds test hosts, and runs platform tests and builds. Follow the native README for PostgreSQL, Xcode, Java, and Android SDK prerequisites. Passing these checks is not real-device or release acceptance.

The iOS build packages the committed mascot and process-summary resource snapshots. It does not require those snapshots to match the latest desktop sources. Adopting desktop presentation changes is an explicit iOS update; the [shared renderer README](../../../clients/native/shared-ui/README.md) describes regeneration, checking, and visual acceptance.

The older `clients/mobile`, `clients/mobile-web`, and `clients/mobile-app` phone implementations are retired. Some remain in shared Web builds and repository checks; passing those gates does not validate the native apps. Account and relay deployment is separate from local desktop setup; see [remote access](../automation/remote.md).

## CI coverage

[Native mobile](../../../.github/workflows/native-mobile.yml) runs only through manual `workflow_dispatch`, not on pull requests or pushes. It retains core integration checks, unsigned iOS/Android Release builds, and Android lint. Native phones are outside the current release scope; restore automatic coverage before bringing them into that scope. Desktop delivery does not require mobile validation.

[The main CI workflow](../../../.github/workflows/ci.yml) runs repository metadata checks, Go checks/tests, desktop checks/tests/builds, and SDK/client checks/tests/builds. It skips changes confined to `docs/` and `docs-site/`. Go CI supplies PostgreSQL for database-backed coverage.

macOS pull requests test the native helper; pushes to `main` also package a desktop directory. Windows runs selected native process/sandbox tests and desktop type checking, with unpacked packaging on `main`. The full desktop unit suite runs on Ubuntu. These jobs cover different boundaries rather than repeating the same full suite on every OS.

[The documentation workflow](../../../.github/workflows/docs.yml) checks policy and builds the site when documentation, site, landing, or relevant build files change. Pull requests build without deployment; `main` builds deploy to GitHub Pages. Tagged product releases use a separate workflow and do not publish standalone CLI archives.

## Code boundaries

`cmd/wuu` and `internal` contain the CLI and Go core. `desktop` owns Electron main/preload, renderer UI, IPC, and packaging. `packages/protocol` holds shared client protocol types; `clients/core` implements UI-free remote behavior. The plugin SDKs and bundled implementations live in `packages/plugin-sdk`, `packages/plugin-go`, and `plugins`.

Keep Electron APIs in the desktop shell. A new shell should communicate with `wuu app-server` through its [protocol](../integrations/app-server-protocol.md), rather than importing desktop internals or creating a separate core. Update both language versions of affected public docs with behavior changes; [documentation maintenance](../../../docs/README.md) describes placement and checks.
