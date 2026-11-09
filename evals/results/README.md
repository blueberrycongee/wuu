# Published eval results

Each run lives at `<YYYY-MM-DD>/<run-id>/result.json` with all public artifacts
referenced by that record. Once cited publicly, a run directory is immutable;
publish a new run instead of rewriting history.

This directory intentionally contains no example score. Templates that look
like results are easy to mistake for evidence. Use
[`../result.schema.json`](../result.schema.json) and `make eval-check` when
creating the first real record.
