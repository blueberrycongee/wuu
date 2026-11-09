# Bun workspaces, without Turborepo

Adding a landing page to what was a single-package library repo, we moved to Bun
workspaces (`packages/blobatar`, `apps/site`, `apps/demo`) but deliberately did
not adopt Turborepo.

Workspaces earn their place for one reason beyond tidiness: apps depend on
`blobatar` as `workspace:*` and import `blobatar/react`, so they resolve
through the real `exports` map. The landing page and the tuning grid are
therefore live integration tests of the published surface — break an export path
and they fail to build. Relative `../src` imports would test a path no consumer
ever takes, and the `exports` map could rot unnoticed. This is the same argument
`scripts/size.ts` already makes for bundle size: measure through synthetic
consumers, not by building the barrel directly.

Turborepo was rejected on measurement, not taste. At the time of the decision the
full build was 8ms and the full test suite 667ms across 25 files — Turbo's own
startup is in that range, so its cache would cost more than it saves, while
adding a dependency and a layer between us and `bun run`. Bun workspaces already
handle linking and `bun --filter` already handles cross-package tasks.

**Revisit when** either a full `bun --filter '*' build` exceeds ~10 seconds, or
there are 4+ packages with a real dependency chain between them. Adopting Turbo
at that point is an afternoon's work; adopting it now buys nothing.

It paid for itself during the migration. Pointing the tuning grid at the public
entry points immediately surfaced two gaps that relative imports had hidden: the
`Animate` type was never re-exported from `index.ts`, and `layout` — needed to
read a seed's `shape` without rendering — was reachable only via
`src/styles/blob`, which is not an entry point at all. Both were added to the
public surface. Neither would have been noticed otherwise, because the only
in-repo consumer was reaching around the exports map entirely.

The restructuring was done immediately because it was free: `blobatar` was
unpublished (npm 404) with no git remote, so there were no downstream consumers
to break. That window closes with the first publish.

**Publishing is deferred**, which keeps that window open. Before the site goes
public, `blobatar` must be published — `apps/site` advertises
`bun add blobatar`, and that command 404s until it is.
