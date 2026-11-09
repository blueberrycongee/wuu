---
name: change-validation
description: Choose proportionate local tests and CI coverage for a Wuu change or PR, based on affected behavior and blast radius. Use when planning validation, iterating on a fix, or preparing a PR; not a replacement for release verification.
---

# Change validation

Optimize for the fastest check that can expose the actual failure, not the largest
checklist. Read the diff and affected callers before choosing coverage. Classify
by behavior, shared consumers, persistence, and recovery cost—not line count or
file extension. A one-line ownership check can be riskier than a large CSS cleanup.

## Choose a starting point

These are working defaults, not labels to obtain approval for. Reassess only when
the implementation or evidence changes the risk. Mixed changes use the relevant
checks from each row, not every suite at the highest level.

| Change | Useful local evidence | Broaden when |
| --- | --- | --- |
| Instructions / docs only | Read for accuracy, check referenced commands/links, `git diff --check`; `make docs-policy-check` for public docs | Markdown is runtime input (prompts, plugin fixtures), or commands/configuration change behavior |
| Presentation only | Inspect the affected rendered view and interactions; use a matching existing layout E2E if useful | Shared styles affect other surfaces, hit targets/focus/scroll change, or state logic is involved |
| Localized behavior | Exercise the changed user journey and relevant existing behavioral tests; typecheck the affected component for type/API edits | Shared consumers, failures, cancellation, or lifecycle transitions cross the local boundary |
| Contracts / high-impact behavior | Focused integration/E2E for the affected boundary and failure/recovery paths; race checks for concurrent ownership; compatibility/platform checks where consumed | Storage migration, permissions, protocol, provider switching, or packaging crosses additional boundaries |

A CSS adjustment does not need the full local suite or a new source-text test.
A riskier change does not automatically need every platform: select platforms and
consumers that exercise the changed contract. Unknown blast radius means inspect
the callers first; use broader coverage when that uncertainty cannot be resolved.
Escalate on a reproduced regression, unexplained relevant failure, shared API or
dependency change, or inability to exercise an important boundary—not cosmetic
review preferences. Ordinary nits do not introduce new approval gates.

## Select checks from this checkout

Read `AGENTS.md`, `Makefile`, `desktop/package.json`, and the relevant workflow
before relying on a command. Reuse existing behavioral/E2E coverage. Add tests
only under the repository's contract-testing policy; enumerate failure cases
before implementation, and run `make test-policy-check` when changing tests.

- **Toolbar alignment:** render the affected toolbar at the relevant size and
  interaction state. Check additional themes, font sizes, or widths when the changed
  rule can affect them. Do not run all Go tests or package every OS for a spacing
  tweak.
- **Localized renderer behavior:** run a specific existing test with
  `cd desktop && npx --no-install vitest run <test-path>`; add
  `npm --prefix desktop run typecheck` for affected TypeScript contracts.
  Prefer the matching E2E for a multi-step user flow. Electron scripts may require
  a built core/renderer and a display: read the script/workflow prerequisites,
  rather than testing stale output and claiming the current revision passed.
- **Plugin activation:** distinguish a local plugin trial from changes to the host
  lifecycle. Exercise the affected activation, failure, reload/disable, and cleanup
  paths. `npm --prefix desktop run test:e2e:plugin-hot-reload` covers a desktop
  journey; focused `go test ./internal/appserver -run '<existing-test-pattern>'
  -count=1` can target the host boundary. Add `-race` when changing concurrent
  generation/admission/lease behavior. Public compatibility changes also need
  `make test-plugin-compatibility` and the applicable provider conformance coverage.
  A local trial is not a publishing workflow.
- **Personal ownership:** changing scope/owner authorization is a contract change,
  even if the diff is tiny. Exercise same-owner access, cross-owner denial, and
  persistence/restart or concurrent writers when affected. Use the relevant
  existing Go/provider acceptance paths in `.github/workflows/ci.yml`; read their
  build, SDK, database, and environment prerequisites. A screenshot of Personal
  settings alone does not prove ownership isolation.
- **Storage, protocol, build/distribution:** exercise the affected recovery or
  migration and producer/consumer contracts. Use the relevant CI race guards for
  concurrency, and the affected OS packaging checks for native/build changes.
  `make ci` is the broad local check/test/build target, not a required iteration
  loop. For an actual release/install claim, use `release-check` and the release
  guide; local checks do not establish signing or packaged-runtime correctness.
- **Skill wording:** review the decisions it would produce for realistic changes;
  check frontmatter and references. Do not build the product just to edit this
  skill. Executable skill helpers still need their own relevant tests.

## Local iteration, PR CI, and release are different

During iteration, run focused checks on meaningful changes; avoid repeating an
unchanged expensive suite after every edit. Before handoff, ensure the evidence
covers the final relevant code, and rerun checks invalidated by later edits.

For PRs, `.github/scripts/select-ci.mjs` selects jobs from the complete base/head
diff; `.github/scripts/desktop-ci-suites.json` maps targeted desktop suites.
Inspect the actual run: renderer edits can select broader desktop/client coverage
than a local layout check. Agent skill entrypoint Markdown is non-runtime docs;
unknown inputs and executable skill resources retain conservative coverage.
`Repository check` still runs on PRs, and `CI check` aggregates selected jobs.
Other workflows have their own triggers. Branch protection and the actual PR
checks—not this risk table—determine the required remote gates.

Do not bypass required checks, weaken protection, or relabel a skipped check as a
pass. Local evidence can avoid duplicate optional work; it cannot replace a
required remote gate. Conversely, do not dispatch every manual workflow merely
to make a small PR look safer: manual dispatch of the main CI selects full
coverage. Use additional CI only for an uncovered relevant boundary or a missing
local environment. If selection logic itself changes, run:

```sh
node --test .github/scripts/select-ci.test.mjs .github/scripts/workflow-context.test.mjs .github/scripts/go-check-contract.test.mjs
make repository-check
```

Keep authorization unchanged. Report briefly in the PR/handoff: risk and why,
checks actually run with results, relevant CI state, and any unverified boundary
with its reason. A sentence or short list is enough; no new form, scorecard, or
mandatory approval ceremony. Keep logs/screenshots for E2E evidence. Diagnose a
failure before retrying; distinguish an infrastructure block from a product
failure and never present either as successful validation.
