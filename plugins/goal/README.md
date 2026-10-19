# Goal

Goal keeps one persistent objective per session and continues it through ordinary model turns. It ships as a bundled extension and uses the same public runtime, storage, session and desktop contracts as external plugins.

Use `/goal` with an objective, explicitly ask the agent to create a goal, or open the Goal Inspector section. The Inspector provides create, pause, resume and clear controls. A completed goal can be replaced; an unfinished goal must be resumed or explicitly cleared first.

The model receives `create_goal`, `get_goal` and `update_goal`. Creation requires an explicit user request; ordinary requests do not implicitly start autonomous work. Only the user controls pause, resume and clear. The model can mark verified completion or a blocker that has persisted for three consecutive goal turns. The blocker audit is a model instruction; the plugin cannot independently establish that two natural-language reports describe the same external condition.

State lives in workspace-scoped plugin storage, keyed by session. Each continuation has a persisted request identity. Completed-turn observations settle usage once, even when the correlated lifecycle notification also arrives. Failed turns pause the goal. User interruption pauses automatic continuation. Pause, clear and disable cancel outstanding goal-owned requests; they do not cancel an unrelated user turn. Reload/restart restores objectives as paused and requires explicit resume.

Token usage is recorded at settled turn boundaries, including the turn that creates the goal, and requires the provider to report usage. Usage covers input and output tokens reported for this session, not descendant sessions or a live in-flight estimate. Time counts the active portion of observed model turns, excluding time spent paused or waiting in the queue. `get_goal` returns the most recently settled usage; the completion tool's own turn settles afterward. Objectives are limited to 8 KiB so sourced goal context fits the public pre-step message boundary.

The canonical runtime is `plugins/goal/src/index.ts`, using the public TypeScript plugin SDK. Run `npm ci --prefix plugins` and `npm --prefix plugins run build` to rebuild the self-contained bundled runtimes. For local iteration, run `wuu plugin dev plugins/goal`; the ordinary plugin watcher rebuilds and reloads the same package. Node 22.7 or newer is required.

Validation:

```sh
npm --prefix plugins run typecheck
node scripts/bundled-plugins-e2e.mjs --plugin=goal
go test -race ./internal/appserver -run 'TestGoalExtension|TestSharedSessionPlugin'
cd desktop && npx vitest run src/renderer/plugins/GoalPlugin.test.tsx
```

Legacy `budget_limited` records load as paused and can be resumed without a budget. Stored budget fields are ignored; this migration can be removed once persisted pre-removal goal records are no longer supported.
