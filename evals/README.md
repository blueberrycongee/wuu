# Public evaluations

This directory is the evidence base for evaluation claims made by wuu. It is
separate from `bench/`, which is ignored and may contain incomplete local runs,
private repositories, secrets, or exploratory judgments.

No README, release note, or product page should quote an evaluation result
unless the supporting record is committed under `evals/results/`.

## What may be published

A public result must let another person understand what ran and inspect the
evidence. Each result directory contains:

- `result.json`, conforming to [`result.schema.json`](result.schema.json)
- the exact prompt or case definition
- the raw machine-readable report produced by `wuu eval --json` or an
  equivalent runner
- grader output and any scripts needed to reproduce the score
- hashes for every artifact listed by `result.json`

Do not publish API keys, credentials, personal data, private repository
content, hidden model reasoning, or provider data whose terms prohibit
redistribution. Redactions must be listed in the result record. A result with
unknown provenance or missing raw output stays in `bench/` and must not be used
for a public claim.

## Layout

- [`cases/`](cases/) contains reusable public task definitions and fixtures.
- [`results/`](results/) contains immutable run records grouped by date and run
  id, for example `results/2026-07-15/wuu-openai-example/result.json`.
- [`result.schema.json`](result.schema.json) is the public record contract.

Run `make eval-check` before committing. CI rejects malformed records, unsafe
artifact paths, missing artifacts, checksum mismatches, dirty source builds,
and records that say they contain secrets or personal data.

## Comparison rules

- Use the same public case, prompt, fixture commit, timeout, and grader for all
  subjects in a comparison.
- Record model/provider settings, retries, tool permissions, hardware, wuu
  version and commit, token usage, duration, and cost source.
- Publish failures and errors, not only successful runs.
- Use repeated runs for performance or cost conclusions. A single run may be
  published as a case study but not presented as a general benchmark.
- State limitations and avoid claims broader than the recorded cases support.
