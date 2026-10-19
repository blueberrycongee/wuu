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

`make setup` installs locked dependencies for the desktop, shared client core, retained Web/Expo clients, plugin SDK, TypeScript plugins, protocol package, and docs site. It does not install the native phone toolchains or provision remote services.

`make dev` runs the desktop launcher. It builds the shared Web assets, native helper where applicable, and TypeScript plugin runtime bundles and the current Go core before starting Electron. The app uses that checkout's private `wuu-core`, not a separately installed `wuu` on `PATH`. Renderer changes use Vite updates; restart the launcher after changing Go, native helpers, or process-startup code so the running processes use the new build.

On macOS, `make dev` and `npm --prefix desktop run dev` enable Computer Use by default. Set `WUU_ENABLE_CUA_MAC=0` to disable it for a development run. `dev:direct` remains available to explicitly enable it. macOS Accessibility and Screen Recording permissions still apply.

On Linux, the development launcher builds the X11 Computer Use helper and enables
its bundled extension by default. Set `WUU_ENABLE_CUA_LINUX=0` to opt out. Build
only the helper with `npm --prefix desktop run build:cua-linux`; it uses the Go
toolchain and copies the XGB license into the package notices. Run in an X11
session with XTEST and XComposite available. Native Wayland control is not
supported. This does not change which platform installers are published.

macOS source launches preserve the invoking terminal's `PATH`, shell/toolchain
locations, and `WUU_<ID>_BINARY` overrides across LaunchServices. Packaged graphical
launches discover standard user install locations and, when needed, login-shell
paths. After installing a CLI while Wuu is running, use Settings → Agents → Refresh.

Desktop packages bundle a checksum-pinned ripgrep in `Resources/tools` and append that directory to the core's `PATH`, so search tools and agent shell commands have `rg` even when the system lacks it; an `rg` already on `PATH` takes precedence. `make dev` does not download it. Run `npm --prefix desktop run build:ripgrep` once to use the bundled copy in development; otherwise the core uses `rg` from `PATH` or its built-in search fallback.

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

The ten bundled business plugins are TypeScript source under `plugins/<id>/src/`.
`make build-plugins` emits self-contained JavaScript into the embedded package assets;
`make build-go`, `make install`, and desktop core builds regenerate those assets before
compiling Go. The checked-in bundles keep plain Go builds reproducible without an npm
install. `make check-plugins` rejects stale generated bundles.

The desktop supplies its own Electron Node runtime to all Node-based plugins, so an
installed desktop needs no system Node or separate business-plugin helper binaries.
The CLI uses Node from `PATH` unless `WUU_NODE_EXECUTABLE` explicitly selects a runtime.
Local TypeScript-file development requires Node 22.7 or newer (or the desktop runtime).

After `make build-plugins`, run `./bin/wuu plugin dev plugins/todo` to watch a bundled
plugin's source through the public directory-development workflow. Its generated
`plugin.json` and `dist/` copy are ignored locally; edit the canonical manifest and
presentation assets under `internal/plugin/bundled/<id>/`, then rebuild to refresh
those copies. Runtime source saves rebuild automatically. A verified development
generation takes precedence over its installed package while retaining development
provenance. Standalone TypeScript files still use `wuu plugin dev ./extension.ts`
without a manifest or build. See the [plugin quickstart](../customize/plugin-quickstart.md).

`make install` installs the CLI from the checkout. The release workflow packages only the macOS arm64 desktop preview. Linux x64 AppImage/Debian packages are experimental source builds. Windows CI and packaging scripts do not imply a published Windows release. See [installation](../getting-started/installation.md) for user-facing availability.

### Linux desktop packages

On Linux x64, run `npm --prefix desktop run pack:linux` for an unpacked app,
`dist:linux` for local AppImage/Debian packages, or `release:linux` for packages
with artifact verification. All three are experimental and have no public update feed. Install the
same locked dependencies used by `make setup`; builds need `dpkg-deb` and the
standard Electron Linux libraries. Packaging builds the Go core, Linux CUA helper, bundled
plugins, Web assets, native node-pty module and checksum-pinned Linux ripgrep.
The command does not publish anything. Before public Linux releases, verify real
Secret Service integration, installed Debian packages on stock Ubuntu 24.04,
FUSE-mounted AppImage launch, and an actual AppImage update/relaunch cycle.
Launchers use X11 or XWayland with Chromium sandboxing enabled; never disable
system security controls to make them start. Confined tool execution requires
Landlock ABI 3 or later. MCP OAuth requires session D-Bus and an unlocked
Secret Service keyring. Native Wayland and Linux arm64 remain unsupported.

`npm --prefix desktop run test:e2e:linux-package` opens both extracted packages
in an X11/XWayland session, checks their production renderer and preload, and
saves screenshots/logs under `desktop/release/linux-package-evidence/`. CI uses
`xvfb-run --auto-servernum` and keeps Chromium sandboxing enabled. This is not
package-manager installation, a FUSE mount check, a native Wayland test, or a
complete installed upgrade. Linux packaging CI also requires all five existing
Landlock filesystem boundary cases to pass without skips.

### Native steering end-to-end check

Build the core, then run `WUU_E2E_CORE=bin/wuu node scripts/native-steering-e2e.mjs`
with Node 22.13 or newer. The fixture starts the real stdio app-server, a controlled
native subprocess, and a disposable SQLite store. It checks same-turn steering,
turn-ID fencing, delayed acknowledgement, rejection, cancellation, persistence
faults, restart recovery, and unsupported-protocol fallback. It saves results,
protocol transcripts and the tested binary hash under `.tmp/e2e/native-steering/`;
set `WUU_STEER_E2E_OUTPUT` to choose another output directory. It uses synthetic
data and no external inference, and does not validate desktop rendering or an
installed native engine.

After building the desktop renderer, run
`cd desktop && electron scripts/native-steering-e2e.cjs` to inspect restored
unconfirmed input in the production UI. It checks read-only recovery, complete
clipboard content, ordinary held input controls, and wide/default-font and
narrow/large-font layouts. Results and screenshots are saved to
`desktop/out/e2e/native-steering/`; set `WUU_STEER_UI_OUTPUT` to override it.

## External engine discovery smoke check

On macOS, build the desktop and core, then exercise the real settings refresh
flow through LaunchServices with a system-only PATH and disposable user data:

```bash
go build -o desktop/build/bin/wuu-core ./cmd/wuu
go build -o desktop/build/bin/fakecodex ./internal/codexengine/testdata/fakecodex
(cd desktop && npx electron-vite build)
open -n -W -a "$PWD/desktop/node_modules/electron/dist/Electron.app" \
  --env PATH=/usr/local/bin:/usr/bin:/bin \
  --args "$PWD/desktop/scripts/engine-discovery-e2e.cjs" --use-mock-keychain
```

Read `desktop/out/e2e/engine-discovery/result.json` for the result (`open` does not
forward the app's exit status). The check covers startup detection, installation
while running, the real Settings refresh button, a native Codex protocol turn,
new shell directories, and explicit disable. It uses a synthetic Codex peer and
requires no system-wide Codex installation; it does not test upstream login or
inference. Use `open --env WUU_ENGINE_DISCOVERY_OUTPUT=/absolute/output` to keep
separate evidence folders.

## Choose the relevant checks

| Command | What it checks or builds |
| --- | --- |
| `make check-go` | Module consistency, formatting, vet, Windows/macOS cross-builds |
| `make test-go` | Go tests for the CLI, core, runtime boundaries, and prompts |
| `make check-plugins build-plugins` | TypeScript plugin types, generated bundle freshness, and runtime packages |
| `make test-plugins` | Plugin source contracts, plus shipped plugin JSONL lifecycle, tools, persistence, and background behavior with disposable host fixtures |
| `make check-desktop test-desktop` | Desktop TypeScript and launcher/unit tests |
| `make build-desktop` | Shared Web assets and Electron main/preload/renderer bundles; not a packaged app |
| `make check-clients test-clients build-clients` | Protocol/SDK/client types, SDK/client tests, and retained Web/Expo bundles |
| `make test-native` | Desktop macOS Computer Use helper tests, not phone app tests |
| `make build-linux` | Linux x64 core, desktop build and directory package |
| `make build-macos` | Core/helpers, desktop build, and directory-packaged macOS app |
| `make docs-policy-check check-docs build-docs` | Documentation policy, site diagnostics, and generated site/link checks |

`make check`, `make test`, and `make build` aggregate the corresponding repository targets. `make ci` runs all three; it does not include native phone verification, macOS packaging, or the docs-site build. `make release-check` adds version validation, uncached Go tests, desktop tests, and the macOS native helper gate. Use the [release guide](release.md) for publication requirements.

Type checks, unit tests, and successful bundles are different evidence from a working app. For UI changes, inspect the affected rendering and interactions using the [desktop UI guide](desktop-ui.md). For plugin changes, also exercise actual tool calls or rendered contributions; package validation alone does not do that.

For SDK tool scheduling, build the TypeScript SDK with
`npm --prefix packages/plugin-sdk run build`, then run
`node scripts/plugin-tool-scheduling-e2e.mjs`. It exercises the official
TypeScript SDK and bundled subagent and peer tools over real JSONL subprocess
pipes with synthetic host services. It requires no Go build or model credentials.
Results and wire traces are retained in `.tmp/plugin-tool-scheduling/results.json`;
`WUU_E2E_OUTPUT` selects another artifact directory. This verifies the SDK process
boundary, not the full Go host, inference provider, or desktop UI.

`npm --prefix desktop run test:renderer-recovery` intentionally crashes hidden
Chromium renderers to check recovery, retry limits, window isolation, and IPC.
It uses a temporary profile and synthetic content, not your Wuu data. Run it in
a graphical desktop session; it does not validate native dialog appearance or
packaged-app behavior.

`npm --prefix desktop run test:e2e:stream-settlement` checks that finishing a
reply preserves its rendered paragraph, heading, list, quote and code geometry.
It covers early and late final-answer classification in both themes, wide/narrow
windows and 14/15/20px text. It starts its own fixture server and temporary
Electron profile; no running desktop or model service is needed. Linux CI runs
it with `xvfb-run --auto-servernum` and `-- --no-sandbox`. Measurements and
screenshots go to `desktop/out/stream-settlement/` (`WUU_SETTLEMENT_OUTPUT`
overrides this). Desktop CI runs the same gate and uploads its evidence.

`npm --prefix desktop run test:e2e:stream-code-cursor` checks where the
streaming cursor paints when a fenced code block ends a live reply: open and
closed fences, with and without a language, empty and nested in a list. The bar
must follow the last code character inside the code card, stay outside the
copied `<code>` text, and keep the card height when the reply settles. It uses
the same self-started fixture server across both themes, 1000/420px windows and
15/20px text. Measurements and screenshots go to `desktop/out/stream-code-cursor/`
(`WUU_CODE_CURSOR_OUTPUT` overrides this).

### Next-message suggestion acceptance

`make test-plugins` includes the shipped next-message JSONL runtime and desktop
provider lifecycle contracts. For the real plugin → Go model service → HTTP
transport chain, build the core with `make build-go` and run
`node scripts/next-message-model-e2e.mjs`. This starts only a loopback synthetic
provider, checks token/input limits, cancellation and accounting, and writes
receipts under `.tmp/e2e/next-message-model/` (`WUU_NEXT_MESSAGE_E2E_OUTPUT` overrides it).

After `npm --prefix desktop run build`, run
`cd desktop && electron scripts/next-message-e2e.cjs`. The fixture mounts the
production App and plugin with synthetic turns and mocked model replies. It
checks keyboard behavior, workspace routing, settings and light/dark,
wide/narrow, default/large text, retaining screenshots and a captured-frame
recording. Linux needs a graphical display such as Xvfb; use `--no-sandbox` only
in a disposable test environment that requires it. Set `WUU_NEXT_MESSAGE_E2E_OUTPUT`
to choose the evidence directory. These checks do not evaluate live-model
suggestion quality or provider billing.

### Live activity preview acceptance

Run `node desktop/scripts/activity-preview-e2e.cjs` after installing desktop dependencies. It bundles the production Electron preview shell and browser/native content adapters, opens isolated browser pages, and starts a synthetic native-frame host subprocess using JSONL commands and binary AVC session output. It verifies independent targets in one session, the five-card visible limit and overflow replacement, promotion, session visibility, dialog suppression, individual dismissal, automatic-preview preference, native authority updates across retained targets, stale callbacks, frame acknowledgement, capture-error recovery, and teardown. Results, transport logs, appearance frame samples, stack geometry and screenshots are saved under `desktop/out/activity-preview/`; `WUU_ACTIVITY_PREVIEW_OUTPUT` selects another directory. macOS also records composed stack screenshots in light/default-text and dark/large-text layouts. Headless Linux uses software rendering and a synthetic display automatically.

Build the core, then run `WUU_BROWSER_OWNERSHIP_CORE=bin/wuu node desktop/scripts/browser-task-ownership-e2e.cjs` to check real Go execution ownership and Chromium input. Stop must interrupt the current run; a later user instruction must work with new authority while old execution IDs remain rejected. The fixture also covers browser task isolation, retained pages, takeover during pending input, and cleanup. Evidence is written under `desktop/out/browser-task-ownership/`.

These checks do not verify ScreenCaptureKit, native application input, macOS permissions, or packaged signatures. On a Computer Use-enabled macOS build, also check browser → native app → browser switching, two windows of one app, Terminal and Wuu as ordinary targets, hidden/minimized windows, target closure, Screen Recording denial/revocation, and takeover during pending input. Hide and Stop must leave the observed applications running; extension disable or replacement must retire its stream. Check both themes, default and large text, reduced motion, and drag/resize near the composer. `node desktop/scripts/inspect-cua-pip.cjs` records process/window diagnostics and captures for a running native preview; a running helper alone is not proof of live pixels.

### Linux Computer Use acceptance

Build the core with `go build -ldflags "-X github.com/blueberrycongee/wuu/internal/version.Version=v$(cat VERSION)" -o desktop/build/bin/wuu-core ./cmd/wuu`, the helper
with `npm --prefix desktop run build:cua-linux`, and the desktop with
`cd desktop && npx electron-vite build`, then run
`node desktop/scripts/linux-cua-e2e.cjs` from the repository root in an isolated
X11 session with XComposite, XTEST, a window manager, compositor, and FFmpeg.
The Linux acceptance workflow provides Xvfb, Openbox and xcompmgr. It exercises
production helper input, exact-window capture and the production PiP factory;
activity is supplied directly to the observation coordinator.

The same run samples an animated preview for 30 seconds, a 2560×1600 target for
15 seconds, hidden-preview idle for 8 seconds, and resumed capture for 10 seconds.
Native actions include the host's canonical-target discovery calls. It records
main-loop delay, renderer responsiveness, video cadence, input response latency,
per-process CPU and RSS samples, memory trends, and repeated track teardown and
recovery in `desktop/out/e2e/linux-cua/performance.json`. `results.json`, screenshots
and a GIF retain functional and visual evidence. `WUU_LINUX_CUA_OUTPUT` selects
the evidence directory.

The checked budgets detect stalls and resource-growth regressions; CPU samples
are reported rather than treated as a portable benchmark. CI records its virtual
display, CPU, Electron version and software rendering.

A second process imports the production desktop main, preload and renderer with
an isolated profile and a scripted loopback provider. The real composer starts a
Go app-server turn through the bundled Linux CUA plugin. The provider boundary
must receive target IDs, screenshot images and fresh snapshot metadata, including
`after=vision`; its returned pixels must also show the fixture's synchronous
post-click paint, not the previous state. An animated target separately verifies
the bounded settling timeout. Actual Go activity notifications create the PiP;
closing it must release capture while two native clicks continue and the turn
completes and persists. `full-app.json` records these checks and a 10-second
application responsiveness sample; `full-app-live.png` and `full-app-desktop.png`
show the actual application. No credentials or live inference are used. This
source-built acceptance does not verify an installed package, physical Linux
hardware, live-model behavior, or performance parity with a Mac.

### Native history protocol E2E

Build the CLI with `make build-go`, then build the local protocol fixture with
`go build -o bin/fakecodex ./internal/codexengine/testdata/fakecodex` and run
`node scripts/native-history-e2e.mjs`. The driver starts real Wuu and fake Codex
processes with disposable stores; it uses no vendor credentials or inference.
It covers native forks, edit cutoffs, transaction failures, unsupported replies,
legacy identities, child/parent isolation, and restart recovery. Results and raw
protocol logs are written under `.tmp/e2e/native-history/`; set
`WUU_HISTORY_E2E_OUTPUT` to select another artifact directory. This checks the
app-server protocol, not desktop rendering or a live vendor CLI.

### MCP connection lifecycle

After building the core, run the loopback lifecycle fixture:

```sh
go build -o bin/wuu ./cmd/wuu
node scripts/mcp-connection-generations-e2e.mjs
```

It starts real app-server processes with disposable state and synthetic MCP
servers. Deterministic response barriers cover Disconnect, Connect, Refresh and
shutdown during background initialization and tool discovery, including later
explicit reconnects and unaffected servers. It also fills the four-connection
startup limit, then verifies that newer Disconnect, Connect and Refresh actions
supersede startup work still waiting in the queue. It does not use real credentials
or validate the desktop UI. `WUU_E2E_CORE` selects another core binary;
`WUU_MCP_E2E_OUTPUT` selects the evidence directory. The default is
`.tmp/e2e/mcp-connection-generations/`, containing JSON RPC transcripts, server
requests, binary hashes and pass/fail receipts. Existing manager tests additionally
cover concurrent operations that the serial app-server RPC dispatcher cannot
issue together, plus permanent revocation and retired-client callbacks.

### MCP authentication across reloads

Run `go run ./scripts/mcp-auth-reload-e2e` to exercise the shared runtime and
app-server protocol against a loopback OAuth/MCP service. It saves synthetic
credentials in a disposable file store, then covers authenticated startup,
manager replacement, a login started before replacement, a retained foreground
tool call, disable/re-enable, and adding the first MCP server after empty startup.
It checks that managers and transport sessions remain generation-owned and that
RPC responses do not expose credentials. No model or external engine is invoked.

The fixture uses the runtime's explicit OAuth-service injection; it does not test
macOS Keychain or the desktop UI. Receipts, connection identities and RPC output
are saved under `.tmp/e2e/mcp-auth-reload/`; set `WUU_MCP_AUTH_E2E_OUTPUT` to use
another evidence directory.

### Provider latency evaluation

The built-in provider path minimizes setup and upload work independently: one
streaming HTTP client per provider owns its pool; Codex, Grok Build and SuperGrok
credential adapters share that pool while rebuilding authentication per request. Custom HTTP transports
retain their proxy and deadline behavior. Codex `auto` can reuse a prewarmed
WebSocket and send a history delta on compatible continuations. Gzip remains an
explicit endpoint experiment for HTTP bodies; API compatibility alone cannot
establish support. These mechanisms preserve payload meaning, cancellation and
submission accounting. The [model-service guide](../getting-started/model-services.md#streaming-latency)
describes their operating boundaries.

Run the reusable real-provider evaluation with a configured provider identifier:

```sh
mkdir -p .tmp/provider-latency
go run ./scripts/provider-latency-e2e -provider openai-codex -transport sse \
  -records 320 -repeats 2 -turns 2 -label candidate \
  > .tmp/provider-latency/candidate.jsonl
python3 scripts/provider-latency-e2e/score.py .tmp/provider-latency/*.jsonl
```

This uses normal local credential resolution and sends only generated repository
records. It can incur charges. It does not edit saved configuration. `-records 0`
measures short histories; `-tool` executes a synthetic tool and checks its result.
It forces the first tool call by default; add `-force-tool=false` to test normal
auto choice on endpoints that reject forced calls. Keep those policies separate.
Compare `-transport websocket` (full context) with `-transport auto` (eligible
deltas), and add `-prewarm` to measure send latency after setup. Prewarm duration
is reported separately. `-gzip-endpoint` accepts one exact request URL for that
process only; omission clears any inherited gzip opt-in for the evaluation.
Failures stop dependent turns in that conversation but preserve other repeats.

JSONL includes the fixture hash, model, workload, deadline, correctness, first
text, first usable text/tool call, completion, DNS/TLS and HTTP timing, body sizes
when HTTP exposes Content-Length, connection reuse, WebSocket replay state,
submission counts and reported token usage. It excludes credentials, raw errors,
prompts and model reasoning. Missing timing fields are unknown, not zero-cost
work. This covers the real agent loop, tool execution and provider; desktop IPC
and painting are outside the measurement. Request-written time includes local
preparation, connection and upload; first HTTP byte also includes server work.
The probe does not claim to isolate server queueing from model prefill.

Build baseline and candidate with the same runner, dependencies and machine.
Keep the source revision, patch, runner and executable hashes with the reports.
For linked worktrees, prefer `go build -buildvcs=false` with
`-ldflags='-X main.sourceRevision=<commit> -X main.sourceDiff=<patch-sha256>'`:
verify the declared source against the checkout, and use `none` only for an
unmodified provider baseline. Interleave A/B/B/A runs with equal prompts, effort,
deadlines and tool policy. Separate first and subsequent turns, provider/model,
short/long histories and tool loops. Do not combine WAN measurements with body
pacing, injected network delay or profiled runs. Report prompt-cache usage because
backend caches and load can dominate the result.

The scorer reports correctness as `100 × passed / planned`. A pass requires the
expected answer, tool result where applicable, no truncation and the expected
submission count. An extra recovered submission fails this strict contract even
if the eventual answer is correct. Missing dependent turns remain in the planned
denominator. The latency index is `100 × baseline / candidate` using mean
first-text time, charging a full deadline for failed or unrun turns. The baseline
is uncompressed SSE with label `baseline`, matched by workload and turn; it must
pass its correctness gate before an index is emitted. Higher than 100 means
faster for that sample. Also inspect successful P50/P90, completion and failures;
a small sample does not establish tail latency or a general product score.

Use `-list-providers` to list configured names and models without credentials.
For command scheduling, add `-shell-workload -repeats 3 -timeout 120s` with either
configured DeepSeek or Codex. Add `-shell-delay 0s` for the short-command control.
This separate workload ignores the synthetic
history/tool/prewarm flags. A real model runs an allowlisted PTC program with a
25-second shell fixture and an independent read, then reads the final result
in a second turn. The runner verifies one launch, both answers, tool counts,
completion ownership and output before/after a background handoff. It reports
`ready_ms` for the first answer separately from `total_ms` for final verification,
plus per-tool execution and `queue_wait_ms` (unknown on older baselines).
Completion reaches the runner through the real process manager; the runner then
starts the second model turn. Desktop notification delivery is outside this probe.
The transport scorer does not score this workload. Compare its JSONL records by
provider and revision; an earlier first answer does not mean the build finished
sooner. Use the same runner on both revisions and retain failures in the report.

Local raw reports belong in ignored output directories. Public performance claims
require a complete clean-source record under [evals](../../../evals/README.md).
CI should gate deterministic connection reuse, payload/redirect contracts,
prewarm/delta behavior, cancellation and recovery, rather than live wall-clock
thresholds or credentials.

### Workspace PDF preview

Run `npm --prefix desktop run test:e2e:workspace-preview` in a graphical session
to exercise the real renderer and PDF.js with generated PDFs. It checks retries,
named and explicit destinations, keyboard/middle-click navigation, page bounds,
source replacement, retained hidden panels, and close/retry races. External-link IPC is recorded rather
than opening the system browser; selected real destination responses are delayed
to check stale results. JSON, synthetic PDFs, and screenshots are written under
`desktop/out/e2e/workspace-preview/`. No installed profile or Go core is used.

Without a graphical session, run
`node desktop/scripts/workspace-preview-e2e.cjs --pdf-fixtures-only` to verify the
same PDF bytes with real Node PDF.js. This checks parsing, named/page-reference
resolution, and unsafe action data, but does not verify rendering or clicks.

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
An isolated runner that exports source without `.git` also supplies
`WUU_SWITCH_SOURCE_COMMIT` and `WUU_SWITCH_SOURCE_CHANGES` for the harness source.
Retain the exported source and its hash manifest alongside the results; omitted
source-change information is recorded as null, not as a clean checkout.

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

### Desktop diagram rendering

The desktop and shared Web renderer use [Pele](https://github.com/obsidianmd/pele)
for `mermaid` fences. Native iOS/Android and the documentation site still use
Mermaid.js. Keep the desktop Mermaid dependency while the native bundle generator
uses it; changing the desktop lockfile requires refreshing its recorded hash with
`node clients/native/shared-ui/build-mermaid.mjs`.

Pele has its own layout and uses Wuu's CSS theme tokens. Mermaid `themeVariables`,
arbitrary HTML labels, and external Mermaid plugins are not equivalent to the
previous renderer. Links and images in diagram source are disabled. Explicit
flow directions are preserved; diagrams fit the existing preview surface, with
click-to-enlarge and standalone SVG export.

After `npm --prefix desktop run build`, run
`cd desktop && npx electron dev/pele/verify.cjs` for desktop acceptance. It loads
the actual Markdown component across light/dark, 15/20px and wide/narrow cases,
checks streaming/error recovery and exported colors, then opens the production
desktop renderer with a synthetic conversation. JSON results, screenshots and an
exported SVG are written to `desktop/out/pele-acceptance/`.

The same run compares Pele and Mermaid.js in one Electron document: three warmup
iterations and ten measured iterations per source, alternating engine order.
Timings include SVG generation, DOM insertion and forced layout, but exclude
module loading, paint and model latency. It also compares minified standalone
engine bundles, including gzip sizes; these are not whole-app package sizes.

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

[The main CI workflow](../../../.github/workflows/ci.yml) selects jobs from the complete PR diff, including deleted files and both sides of renames. Repository metadata checks remain lightweight and always run on PRs. Documentation-only and native-phone-only changes skip the main product suites; native phones keep their dedicated workflow.

Renderer changes run Desktop and Clients because the Web client imports renderer sources. Other desktop changes run Desktop, Clients, and native OS checks. Shared protocol, client core, Go, plugins/SDK, dependency locks outside these scoped directories, build scripts, and unknown paths conservatively run all suites. Runtime Markdown under prompts or plugins is not exempt. Go CI supplies PostgreSQL for database-backed coverage.

Desktop validation runs six independent Linux jobs in parallel: unit/type checks and renderer recovery; file/response selection; conversation corpus replay; image/stream/resize layout; session/core interactions; and shell/onboarding/settings. Each job has its own build, Electron profile, Xvfb display, and evidence uploads. A failed group does not cancel the other groups. `Desktop check` remains the stable summary and rejects any failed or cancelled selected group.

For changes only to standalone E2E entrypoints listed in [the suite map](../../../.github/scripts/desktop-ci-suites.json), CI selects the union of their groups without unrelated Clients/native checks. Shared helpers, preloads, fixtures, production code, styles, dependencies, CI configuration, and unknown paths keep the conservative full-group fallback. This avoids assuming that component filenames describe their complete dependency graph. No test commands, assertions, scenario matrices, or evidence uploads are removed; manual runs execute every group. Extend the map only after verifying that an entrypoint is not a shared helper. Run `node --test .github/scripts/select-ci.test.mjs` to verify selection against temporary Git changes, including deletions and mixed scopes.

For PRs modifying only existing renderer TypeScript/TSX modules (plus optional documentation), the unit job selects tests through parsed import/export dependencies, including re-exports, static dynamic imports, configured Vite aliases, and setup-file dependencies. Vitest supplies the actual test inventory. Tests with computed loading, filesystem/process execution, parse failures, or unresolved local imports are retained conservatively. No affected consumer means the full unit gate runs. Selection evidence is uploaded as `unit-dependency-selection`; test execution failures are never retried as a way to pass.

Styles, assets, test setup, runtime plugin modules, new/deleted/renamed files, dependencies, configuration and mixed scopes retain full unit coverage. Type checks, development-launcher checks, serialized Git-service tests and the existing E2E groups remain in place. The E2Es load the complete App and share its dependency closure; static imports do not justify skipping a group simply because a test does not open a particular page. Run `node --test desktop/scripts/ci-unit-dependencies.test.mjs` after installing Desktop dependencies to check the dependency-selection contract.

Desktop CI installs the Electron binary once before parallel unit tests to avoid concurrent extraction into the same directory. macOS and Windows retain their platform-specific tests on affected PRs. macOS also checks packaging when native resources or packaging inputs change.

Main pushes do not repeat Go, Desktop, Clients, or native test suites. When changed paths select native checks, they run macOS/Windows packaging and the Linux packaging, sandbox and launch gates. Manual `workflow_dispatch` runs all checks and all three platform packages. Direct main pushes therefore do not receive the PR test coverage; use a PR or manually run CI when full validation is needed.

`Go check` accepts skipped Go jobs only when path selection explicitly excludes them. `CI check` requires successful selection and every selected job to succeed; unexpected skips, failures, and cancellations fail the summary. These summaries do not change repository protection rules.

[The documentation workflow](../../../.github/workflows/docs.yml) checks policy and builds the site when documentation, site, landing, or relevant build files change. Pull requests build without deployment; `main` builds deploy to GitHub Pages. Tagged product releases use a separate workflow and do not publish standalone CLI archives.

## Code boundaries

`cmd/wuu` and `internal` contain the CLI and Go core. `desktop` owns Electron main/preload, renderer UI, IPC, and packaging. `packages/protocol` holds shared client protocol types; `clients/core` implements UI-free remote behavior. The official TypeScript plugin SDK lives in `packages/plugin-sdk`; bundled implementations live in `plugins`. `internal/sdk` is the internal application-embedding facade for the Go host, not a plugin authoring API. Other language runtimes can implement the same language-independent JSONL process contract.

Keep Electron APIs in the desktop shell. A new shell should communicate with `wuu app-server` through its [protocol](../integrations/app-server-protocol.md), rather than importing desktop internals or creating a separate core. Update both language versions of affected public docs with behavior changes; [documentation maintenance](../../../docs/README.md) describes placement and checks.

### Conversation corpus acceptance

Run `npm --prefix desktop run test:e2e:conversation-corpus` to build the current
checkout and replay twelve sanitized local conversation excerpts through the
existing UI tour. macOS uses the checkout's Wuu Dev host with an existing signing
identity (`Wuu Dev Signing` by default, or `WUU_DEV_SIGN_ID`). If none is available,
set `WUU_DEV_SIGN_ID=-` for ad-hoc signing. Acceptance never creates a signing key,
imports a certificate or changes keychain trust. Linux needs a graphical
session or `xvfb-run --auto-servernum`; CI supplies `-- --no-sandbox`.
`npm --prefix desktop run test:corpus` checks the package's sharing boundary.

The package lives in `desktop/scripts/fixtures/conversation-corpus/corpus.json`.
It preserves original wording, Markdown, whitespace, response order and tool-call
counts, with targeted replacements for identifying names, private services,
credential fragments, paths and historical references. Original session IDs,
provider configuration, hidden reasoning, tool arguments/results and attachments
are not included. Tool calls execute only reads of a disposable synthetic file.
Model labels describe the recorded source; replay uses one scripted local
provider, so this does not verify vendor protocols or current model behavior.

The runner pauses at partial and complete target responses and before final
completion, captures light/dark, 15px/wide and 20px/narrow appearances, then
captures the completed answer and reloads the shell to verify persisted history.
Streaming chunks and timing are simulated; original token timing was unavailable.
Each checkpoint binds the expected response step to its protocol item and exact
source text, then scrolls its rendered marker into view and checks its clipped
geometry. Process history is expanded only when the sample includes tool calls.
Page overflow, missing/offscreen content, missing history and renderer errors fail the run. Screenshots
and geometry remain evidence for visual review, not an automatic aesthetic score.

Each run writes screenshots and `corpus-report.json` below
`artifacts/conversation-corpus/`. `WUU_TOUR_OUTPUT` sets an explicit destination;
`WUU_TOUR_MATRIX` overrides appearances, for example `light:15:1280,dark:20:720`.
Use `WUU_TOUR_ONLY=sample-04` to reproduce one sample; the report records the
selected IDs. `WUU_CORPUS_TEXT_ONLY=1` replays just each selected sample's final
answer as a single no-tool turn; reports label this `final-answer-only`, rather
than the original response sequence. CI also runs this regression with sample-02
and uploads both runs in `conversation-corpus-evidence`.
`WUU_CORPUS_SKIP_BUILD=1` reuses a prepared build; `WUU_TOUR_CORE` selects its core.
Only use these overrides when the binaries match the checkout under review.

To add a sample, review the complete local excerpt first, apply targeted
replacements consistently, and compare Markdown structure, newline counts and
length changes locally. Keep raw sources and replacement maps outside the public
checkout. The validator rejects unexpected fields, common secrets and non-example
HTTP URLs; it cannot establish that arbitrary prose is safe to share. Review each
new sample manually. Keep source claims in sample text as historical fixture
content, not current product documentation. No real account or model key is needed.
