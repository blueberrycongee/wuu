# twin desktop interaction fixture

Build with `node desktop/scripts/build-personal-fixture.cjs`. The generated
`desktop/out/personal-fixture/index.html` runs the selected default Wuu application/surface graph, twin
contribution, native message layout, file cards, history search and composer.
The visible banner identifies all fixture data as synthetic. No real account,
model service or external write is used.

`node desktop/scripts/smoke-personal-fixture.cjs` checks direct main entry,
navigation order, continuous right-panel sections, in-panel task/output details,
main identity and draft retention across routes, and digest-verified attachments.
It omits embedded font bytes because jsdom cannot render them, includes the browser AbortSignal.any shim, and executes the unchanged production runtime bundle. It has no layout or pixel renderer and is not visual evidence.

Rendered acceptance additionally covers light/dark appearance, 15/19px text,
wide/narrow windows, real native input, history search, and scroll continuity.
A separate production Electron/main/preload/Go-host harness is required to prove
real runtime lifecycle, streaming, cancellation and persistent recovery. A fixture
pass alone does not prove those contracts or natural-language model quality.

### Accepted send / history refresh recovery

Use `?sendRefresh=error` to fail one main-history read after each synthetic
send has been accepted. The refresh action must only read history, never send
again. Add `&sendDelay=5000` to delay the synthetic acknowledgement while editing
a newer draft or changing reply targets. `window.__sendRecoveryFixture` exposes
accepted count, refresh-failure count and captured request inputs for QA.
These fixtures do not establish delivery or read receipts from a real agent.

`node desktop/scripts/personal-routines-e2e.cjs` exercises the production routine
dialog against the synthetic transport: revision conflicts, save/cancel/failure,
repeat submissions, history paging/retry, task-detail navigation, date/zone
validation, and keyboard focus. Its report is written to
`desktop/out/routines-e2e/dom-report.json`. This is DOM evidence, not pixel or live
scheduler verification. Routine fixture mutations affect only the current page.
Use `?routineHistory=paged|error|pending|empty` or
`?routineSave=conflict|error|pending` to inspect recovery states.

Use `?routineStopped=1` to inspect a stopped routine: history remains available,
but editing is disabled because stopped routines cannot accept updates.

### Personal action approval cards

Use `?review=ready` to show the current compiled main-chat action card with a
synthetic `write_file` proposal. `empty`, `expired`, `blocked`, `stale`, `denied`,
`revoked`, `uncertain`, `succeeded`, `long`, `lost-ack`, and `stale-response` cover
interrupted and terminal UI states. The preview's Auto Review settings button
opens the actual Personal settings contribution; changes affect memory only.
No native host gate, file handler, model, account, or external service is called.

`node desktop/scripts/personal-approval-ui-e2e.cjs` checks this compiled fixture
in a sandbox-enabled Chromium with a fresh context per scenario. Set
`WUU_REVIEW_PLAYWRIGHT_PATH` to an available Playwright module and
`WUU_REVIEW_CHROMIUM` to an installed Chromium executable when necessary.
`WUU_REVIEW_UI_OUTPUT` selects a result directory. The runner rejects network
requests, verifies version-bound decisions, lost-response read-only recovery,
explicit Auto Review opt-in, terminal states, and main-chat availability, and
captures wide/narrow, light/dark, and large-text screenshots. Its results do not
establish backend authorization, execution or durable restart correctness.

When pixel rendering is unavailable, `node desktop/scripts/personal-approval-dom-e2e.cjs`
checks the same compiled fixture in jsdom and writes a distinctly labeled DOM-only
report. It does not satisfy rendered visual acceptance.
