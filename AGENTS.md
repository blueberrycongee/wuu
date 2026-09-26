# Wuu contributor and agent instructions

These instructions apply throughout the repository. Read applicable instructions
in subdirectories before changing their files. Keep personal preferences and
machine-specific setup in user-level configuration.

## Scope and authorization

- Read the relevant code before proposing or making changes. Trace the complete
  flow, module boundaries, shared contracts, callers, and downstream effects.
- Reviews, audits, explanations, and proposals are read-only unless the user
  also requests implementation. A proposed plan does not authorize execution.
- Complete authorized work. Make routine implementation decisions independently;
  ask when ambiguity materially changes scope, architecture, security, risk, or
  authorization. Explain meaningful tradeoffs and recommend a path.
- Commit, push, PR mutations, release, and deployment actions require explicit
  authorization or an established workflow for those specific actions in the
  current task. Authorization does not carry over to unrelated work.
- Once commits are authorized, commit each independently validated step as a
  coherent, reviewable change. Commit authorization alone does not authorize push.
- Never merge or enable auto-merge without an explicit request for that PR.
  Passing CI, approval, or a request to continue is not merge authorization.
- Never force-push unless explicitly authorized to rewrite the published history
  of the specific branch. Report a non-fast-forward rejection instead of forcing.

## Implementation and review

- For maintenance, prefer focused changes and established conventions. When a
  redesign or compatibility break is explicitly requested, evaluate the design
  from its requirements rather than silently imposing compatibility constraints.
- Apply a corrected design criterion across the relevant scope, not only the
  first example. Avoid duplicate logic and fixes that move failures to another layer.
- Require a concrete benefit for helpers, abstractions, and special cases. Prefer
  simple code and a few coherent abstractions over speculative infrastructure.
- Keep a helper or extra module when it has at least two call sites, or when the
  inlined logic is too long to read in one pass. A short operation used once stays
  at the call site. Add an interface, base type, or registry only after a second
  concrete implementation exists.
- Define a constant used by one module in that module. Write a policy or default
  once in the layer that owns it and pass that value down. Do not copy it into a
  lower-layer constant or factory default.
- A defensive branch stays only when removing it would break a real failure path.
  Delete debugging-only catch blocks, checks for impossible states, and tests
  that exist only to preserve that scaffolding once the contract is settled. A
  missing field on a known type fails visibly.
- Keep parameters that the function actually uses. When an established interface
  requires an unused parameter, preserve the signature and state that constraint
  instead of discarding the value to satisfy a type checker or linter.
- Isolate a temporary compatibility workaround and name its removal condition in
  one comment.
- Follow the language's own visibility, error handling, and control flow. Go
  returns early on errors. Match the surrounding file's types, imports, and
  logging.
- Judge public APIs from the caller's perspective: discoverability, misuse
  resistance, error semantics, configuration, compatibility, and evolution.
- Base repository claims on inspected code, tests, configuration, and history.
  For third-party behavior, prefer official sources matching the relevant version.
  Identify uncertainty and conflicts rather than presenting assumptions as facts.
- Review the relevant scope systematically. Report concrete bugs, security issues,
  and logic errors with their impact and evidence. Do not invent findings or
  substitute cosmetic suggestions for defects. Say "no issues found" when appropriate.
- Challenge proposals when evidence warrants it. Avoid reflexive agreement.

## Working tree, validation, and handoff

- Preserve unrelated staged, unstaged, and untracked work. Stage explicit paths or
  selected hunks; do not include another task's changes in a commit.
- Use `gh` for GitHub operations. Write code comments and commit messages in English.
  Use conventional commit messages; follow [CONTRIBUTING.md](CONTRIBUTING.md) for
  contribution workflow and the [development guide](docs/en/project/development.md)
  for build and test commands.
- Run checks appropriate to the change before declaring it complete. Distinguish
  tests and builds from rendered UI, simulator, real-device, and production checks.
- Prefer end-to-end tests as the primary testing mechanism. Use them to verify
  complex features work end to end. At the end of an E2E run, produce a
  verifiable and repeatable artifact (logs, screenshots, recordings, or other
  evidence a reviewer can re-check).
- Never write unit tests after the implementation. If a system must be tested in
  isolation, first enumerate the ways it could fail, then write the code against
  those failure cases.
- Add a focused automated test only to protect an important contract: a public
  API, protocol, persistence or migration rule, security or permission boundary,
  recovery behavior, or a concrete bug that has already escaped. Prefer extending
  an existing behavioral or E2E suite over adding a new unit suite. Design tests
  around observable behavior and the failure cases callers depend on.
- Skip a new test when the change has no such contract, when the goal is
  coverage, or when the test would mirror literals, mappings, obvious control
  flow, implementation details, or a removed feature. Absence is a contract only
  when callers rely on that absence.
- Coordinate concurrency deterministically instead of relying on sleeps when
  practical.
- Keep merge-gate tests on behavior, protocols, recovery, and public contracts.
  Do not add tests that read stylesheet source, pin CSS declarations or class
  lists, snapshot generated theme matrices, or assert prompt/i18n wording. Run
  `make test-policy-check` when changing tests; existing stylesheet-read
  exceptions do not authorize new ones.
- Comments explain rationale, invariants, safety constraints, or external quirks.
  Public API documentation describes observable contracts, not incidental internals.
- For long tasks, preserve the overall objective and report material progress.
  Handoffs state results, validation, remaining limitations, and required user input.
  When committing or pushing, report the commit hash and the verified outcome.

## UI design and visual acceptance

- Use shared components and design variables for equivalent controls. Preserve
  consistent typography, icon treatment, spacing, corners, and interaction states.
- Use one UI font-size baseline for sidebars, prose, menus, settings, and controls.
  Keep code size independent across code blocks, editors, and diffs. Preserve saved
  preferences. Current defaults are 14px UI and 11px code; these are reference
  values, not fixed sizes that override user preferences.
- Establish hierarchy with position, spacing, type size, and weight. Default text
  should remain regular; use emphasis selectively. Keep secondary text readable.
- Distinguish row spacing, heading-to-content spacing, content-to-action spacing,
  and group spacing. Define each relationship once rather than accumulating gaps
  across wrappers. Include invisible drag targets and placeholders in measurements.
- As text grows, adapt row heights and necessary spacing. Scale icons according to
  their role rather than enlarging every element mechanically. Never hide layout
  problems by shrinking text, tightening letter spacing, or reducing contrast.
- Align peer labels, icon columns, and trailing actions. Indentation expresses
  hierarchy and must not depend on whether an item is running or unread.
- Reserve the actual footprint of status indicators and actions plus a reading
  gap. Check combinations such as selected, running, unread, forked, hovered,
  keyboard-focused, disabled, and dragging when those states can coexist.
- Keep compact controls usable and reading areas comfortable. Adapt mobile layout
  and interaction to the device; do not merely shrink the desktop interface.
- Use whitespace and alignment before adding containers. Add borders, backgrounds,
  and shadows only to express grouping, interaction boundaries, elevation, or
  deliberate brand identity. Use semantic colors consistently.
- Include only text that helps users understand, decide, or act. Avoid repeating a
  label in a subtitle or hint. Retain explanations for non-obvious consequences
  and restrictions. Remove redundant copy, wrappers, and decoration before handoff.
- Motion explains changes and provides feedback. Keep frequent actions responsive,
  avoid layout jumps and moving click targets, and respect reduced-motion settings.
- Inspect actual rendering after UI changes. Cover affected themes, default and
  large font sizes, wide and narrow windows, long and empty content, scrolling,
  fixed regions, menus, and relevant state combinations. Check alignment,
  readability, clipping, truncation, overlap, and focus visibility.
- Tests do not replace visual acceptance. State precisely what was inspected and
  what remains unverified; do not claim full coverage from one screenshot.

## Product versioning

- Product releases use UTC CalVer `YYYY.M.D`, where `YYYY`, `M`, and `D` are the
  release year, month, and day in UTC, without leading zeros for month or day.
  Prereleases append a suffix such as `-rc.1`.
- `VERSION` is the product version source. Use `make version-sync` to synchronize
  desktop and active native iOS/Android metadata; do not edit generated versions.
- Use `make release-prepare` for the next UTC CalVer, or pass
  `RELEASE_VERSION=YYYY.M.D`. Run `make version-check` before tagging.
  Release tags are `vYYYY.M.D`; product releases do not use SemVer bump rules.
- Product versions express release recency. Check and document Extension API,
  protocol, storage, and migration compatibility separately. `minimum_wuu_version`
  accepts the same CalVer values as the product version.
- CalVer tags are not Go module major versions. Install the CLI from a checkout
  with `make install`; do not promise `go install github.com/blueberrycongee/wuu/cmd/wuu@latest`
  for a CalVer release tag.

## Extension architecture

- Wuu is a decentralized trusted-extension system. Installation grants trust and
  enables the extension. Same-source updates retain trust; source changes require
  confirmation. Do not add approval, inspection, restart, or per-update ceremonies
  unless the threat model changes.
- Wuu owns a small, public, versioned Extension API, loading, lifecycle, safe mode,
  diagnostics, and cleanup. It does not audit, certify, sandbox, rank, host, or
  maintain third-party extensions.
- Keep the Go core and production product. Do not pursue a whole-product rewrite
  or a TypeScript kernel. Direct execution and reload of one local TypeScript file
  is the development floor; builds, manifests, packaging, installation, and
  validation must remain optional for that path.
- The kernel owns shared mechanisms and invariants: lifecycle, persistence,
  execution, recovery, credentials, permissions, windows, and host UI boundaries.
  Plugins own business state, prompts, tools, background policy, and presentation.
  Disabling a plugin must stop its product behavior.
- Bundled and community extensions use the same public contracts. Do not introduce
  product-ID checks, privileged APIs, private shortcuts, duplicated plugin state,
  or silent host fallbacks. Capabilities with legitimate alternative implementations
  should be replaceable extensions; core mechanisms need not be plugins.
- Add host capabilities or public abstractions only for demonstrated needs shared
  by at least two plausible extensions. Internal machinery is not automatically a
  public compatibility promise.
- Fix domain-specific failures in the plugin and shared lifecycle or contract
  failures in the host. Isolate temporary compatibility workarounds and document
  their removal conditions.
- Treat reference implementations as prior art, not specifications. Verify their
  assumptions, invariants, costs, and failure conditions before borrowing a design.
  Preserve licenses and attribution; do not expose private reference paths or
  identities in public material.

## Documentation and communication

- Public product, development, protocol, and design documentation belongs in
  `docs/`; follow [docs/README.md](docs/README.md). Keep English and Chinese public
  pages aligned. Only pages registered in `docs/site.json` appear on the docs site.
- Keep sensitive research, private reference material, and unpublished plans out
  of tracked public files. Use the maintainer-designated private documentation
  location when available; public contributors must not depend on that checkout.
- Write plainly and lead with the result or main point. Give each paragraph one
  purpose; use lists and tables for genuinely parallel or comparative information.
  Explain necessary technical terms and avoid filler, repeated conclusions, and
  unsupported claims. Follow the user's language for discussion.
