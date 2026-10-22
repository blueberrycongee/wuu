# twin desktop interaction fixture

Build with `node desktop/scripts/build-personal-fixture.cjs`. The generated
`desktop/out/personal-fixture/index.html` runs the actual Wuu App shell, twin
contribution, native message layout, file cards, history search and composer.
The visible banner identifies all fixture data as synthetic. No real account,
model service or external write is used.

`node desktop/scripts/smoke-personal-fixture.cjs` checks direct main entry,
navigation order, continuous right-panel sections, in-panel task/output details,
main identity and draft retention across routes, and digest-verified attachments.
It has no layout or pixel renderer and is not visual evidence.

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
