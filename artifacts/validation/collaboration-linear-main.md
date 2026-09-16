# Collaboration integration verification

- Preserved original main at `backup/pre-linear-main-20260916` (`5235e38c123eeb7c730ae21b52753168b3ba2c87`) and in the verified bundle `artifacts/linear-main-backup/pre-linear.bundle` in the original checkout.
- Replayed 15 non-merge commits on `6372d8e6a92e4f6b6e53145e22563752ad965a04`. The resulting tree matched the backup exactly before the UI fixes; unpublished history contains no merges.
- Task messages are filtered only in the conversation renderer. The task board and backend lifecycle remain intact. The cancellation linkage regression passed.
- Added behavioral coverage for saved DM restoration, unavailable-room fallback, hiding the previous room during DM opening, stale DM responses, stale directory polling, and per-room late message responses.
- `make ci`, `make check-docs`, and `CI=1 make build-docs` passed in the independent worktree. The initial dependency symlinks were replaced with worktree-local installs to avoid resolving the original checkout's protocol types.
- Real Electron renderer checks passed with an isolated profile and fixture APIs: saved DM restoration; task bodies absent from the log; group switch, reentry, and reload; dark/light themes at 14/20 px and 390/1280 px widths (eight combinations). Screenshots were captured locally. This is automated rendering/interaction evidence, not a claim of manual visual inspection or live-provider E2E coverage.

Remote workflow verification is performed against the final pushed commit, not the historical merge or an intermediate HEAD.
