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

### Live resize layout guard

After a desktop production build, run `npm --prefix desktop run test:e2e:resize-live-layout`
in a graphical session. CI can use `xvfb-run --auto-servernum` with the Electron
script directly; it does not rebuild the renderer. The fixture reuses the long
Markdown history from `resize-e2e-preload.cjs`, with synthetic notifications and
browser-host bounds. It does not exercise a real Go process or embedded browser.

The JSON artifact at `desktop/out/e2e/resize-live-layout.json` records public
theme/syntax tokens, host geometry, intermediate message widths and text line
counts during left/right drags and collapse/expand animations. It also checks
stream-following, paused-reader endpoint anchoring, and submitted-query placement
and holding during reflow. Continuous samples establish live rewrapping; endpoint
checks alone do not prove every painted frame keeps an identical scroll offset.
There is no CI wall-clock performance threshold.

For a same-environment source-built A/B comparison, run the driver against the
baseline first, then the candidate. Set `WUU_E2E_RENDERER` to each built
`out/renderer/index.html` and `WUU_RESIZE_OUTPUT` to separate JSON files. On the
candidate run, set `WUU_RESIZE_COMPARE` to the baseline JSON to compare all public
tokens and settled host geometry. No CSS is injected by this driver. Do not pool
these instrumented layout checks with low-probe timing runs. Linux headless
Electron needs `--ozone-platform=headless --ozone-override-screen-size=1440,1000`;
otherwise the default display may be too small for a meaningful viewport.

For a low-probe panel diagnostic, set `WUU_RESIZE_DIAGNOSTIC=panels` with the
same command and artifact selectors. It reuses the real resize handlers for 81
identical input steps per empty-composer left/right round trip. JSON includes
CDP style/layout counters, action duration, rAF intervals, and observed inline
width changes. It does not read per-frame geometry, inject CSS, profile CPU,
record video, or enforce a latency threshold. Run baseline and candidate
sequentially under the same display conditions, using separate output files;
this mode is independent of the full correctness gate.

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
Keep warmup samples separate from measured results.
Do not pool initial opens with repeats or traced runs with untraced runs.
Headless Linux can use `--no-sandbox --ozone-platform=headless`; its frame cadence
is a property of that rig. Two frames are paint opportunities, not physical
presentation or a claim of 120 Hz. Wall-clock timings remain informational.

### Fresh-process startup diagnostics

The optional `startup-e2e.cjs` driver starts its monotonic clock before spawning
Electron. Prepare disposable data separately with `WUU_STARTUP_PREPARE_ONLY=1`,
`WUU_STARTUP_PREPARE_DIR` set to a new directory, and the usual turn/sidebar
counts. Preparation uses the existing switch fixture, pins every runtime row to
`permission_mode=standard`, selects conversation 1 by default, and writes a
synthetic-fixture marker. It does not import the desktop application. Prepare a
fresh fixture for every trial, using the same seed core and counts; do not pass
a real WUU home or reuse an Electron profile.

```bash
# Dependencies, production bundles and bundled helpers must already be built.
WUU_STARTUP_PREPARE_ONLY=1 WUU_STARTUP_PREPARE_DIR="$PWD/.tmp/startup-a" \
  WUU_SWITCH_TURNS=3000 WUU_SWITCH_SIDEBAR_THREADS=1500 \
  WUU_DESKTOP_CORE="$SEED_CORE" desktop/node_modules/.bin/electron \
  --no-sandbox --ozone-platform=headless desktop/scripts/session-switch-e2e.cjs
WUU_SWITCH_MAIN="$MAIN_BUNDLE" WUU_DESKTOP_CORE="$TEST_CORE" \
  WUU_SWITCH_BUILD_COMMIT="$UI_COMMIT" WUU_SWITCH_CORE_BUILD_COMMIT="$CORE_COMMIT" \
  node desktop/scripts/startup-e2e.cjs .tmp/startup-a .tmp/startup-a-results
```

The measured process uses normal bundled extensions by default. For retained
builds, supply their verified helper paths/source root and preserve the helper
manifest; results record resolved core environments, initialization inventory,
and explicit helper artifact hashes. Safe mode is a separately labeled control.
The configured returning-user fixture is not a pristine first-launch/onboarding
measurement. It uses no real credentials, accounts or inference.

On Linux the driver establishes a 1440×1000 headless display before window
creation. The product chooses its own initial window size; the harness neither
resizes nor manually shows it after navigation. A zero viewport fails as an
invalid rig. Results preserve display/work area, zoom, visibility and compositor
state. The endpoint requires the selected history's final marker, native trusted
input into a visible editable composer, and its draft plus enabled Send over two
animation frames. Physical presentation and OS-cold launch are not claimed.

`startup-results.json` distinguishes parent-spawn, window/navigation, active and
background core requests, main IPC, history DOM, and typed readiness. Core
response bytes and main IPC timings overlap; do not add them. Sync preference
IPC timings cover only the main handler, not renderer roundtrips. Retrospective
PaintTiming entries retain their renderer navigation clock. Renderer CDP counters
start after debugger attachment; earlier parse/evaluation and the first React
commit are uncovered. Helper CPU/IO is unavailable where child enumeration fails.
Keep fresh-process runs separate from ordinary switch, streaming, profiled, and
older diagnostic harness results. Compare the same exact UI when isolating a
core-only change, retain all repetitions, and use the final main revision as the
optimization baseline.

## Native phones and remote services

The active phone implementations are SwiftUI on iOS and Jetpack Compose on Android in [`clients/native`](../../../clients/native/README.md) (Chinese). Their dedicated verification command is:

```bash
bash clients/native/verify.sh all
```

Use `ios` or `android` to select one platform. The script starts an isolated PostgreSQL-backed test environment, builds test hosts, and runs platform tests and builds. Follow the native README for PostgreSQL, Xcode, Java, and Android SDK prerequisites. Passing these checks is not real-device or release acceptance.

The iOS build packages the committed mascot and process-summary resource snapshots. It does not require those snapshots to match the latest desktop sources. Adopting desktop presentation changes is an explicit iOS update; the [shared renderer README](../../../clients/native/shared-ui/README.md) describes regeneration, checking, and visual acceptance.

The older `clients/mobile`, `clients/mobile-web`, and `clients/mobile-app` phone implementations are retired. Some remain in shared Web builds and repository checks; passing those gates does not validate the native apps. Account and relay deployment is separate from local desktop setup; see [remote access](../automation/remote.md).

## CI coverage

[Native mobile](../../../.github/workflows/native-mobile.yml) runs iOS core integration and unsigned simulator/device builds on pull requests and `main` pushes that change the iOS client, shared native fixtures, remote protocol, or Go dependencies. Manual `workflow_dispatch` also runs the existing Android integration, Release builds and lint. These checks do not cover device signing, store distribution, or physical phone interaction.

[The main CI workflow](../../../.github/workflows/ci.yml) runs repository metadata checks, Go checks/tests, desktop checks/tests/builds, and SDK/client checks/tests/builds. It skips changes confined to `docs/` and `docs-site/`. Go CI supplies PostgreSQL for database-backed coverage.

Desktop CI installs the Electron binary once before parallel unit tests. The
Electron package downloads on first import, so concurrent workers must not race
to extract into the same installation directory.

The `Go check` summary requires both Go jobs to succeed. It always runs for pull
requests, so a failed, skipped, or cancelled dependency cannot pass that gate.
For post-merge `push` runs only, cancelling the whole workflow skips or cancels
the summary unless a Go dependency actually failed; a real failure still fails it.
This exception does not apply to pull requests because GitHub accepts a skipped
required check for merging. It does not change repository protection rules.

macOS pull requests test the native helper; pushes to `main` also package a desktop directory. Windows runs selected native process/sandbox tests and desktop type checking, with unpacked packaging on `main`. The full desktop unit suite runs on Ubuntu. These jobs cover different boundaries rather than repeating the same full suite on every OS.

[The documentation workflow](../../../.github/workflows/docs.yml) checks policy and builds the site when documentation, site, landing, or relevant build files change. Pull requests build without deployment; `main` builds deploy to GitHub Pages. Tagged product releases use a separate workflow and do not publish standalone CLI archives.

## Code boundaries

`cmd/wuu` and `internal` contain the CLI and Go core. `desktop` owns Electron main/preload, renderer UI, IPC, and packaging. `packages/protocol` holds shared client protocol types; `clients/core` implements UI-free remote behavior. The plugin SDKs and bundled implementations live in `packages/plugin-sdk`, `packages/plugin-go`, and `plugins`.

Keep Electron APIs in the desktop shell. A new shell should communicate with `wuu app-server` through its [protocol](../integrations/app-server-protocol.md), rather than importing desktop internals or creating a separate core. Update both language versions of affected public docs with behavior changes; [documentation maintenance](../../../docs/README.md) describes placement and checks.
