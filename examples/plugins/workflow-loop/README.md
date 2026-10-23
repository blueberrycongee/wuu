# Evidence workflow driver

This independent TypeScript plugin plans one evidence-gathering tool batch, commits a focused active context, and then synthesizes an answer with tools disabled. It requires `driver.turn` 3.0: input admission, request context, and request transforms are explicit stages, so an overflow retry cannot consume more input or transform a request twice. It uses only `@wuu/plugin-sdk` and runs directly with Node's TypeScript support.

From the repository root:

```bash
node scripts/workflow-driver-e2e.mjs
```

The check installs a packed public SDK in an independent directory, reads a real fixture file, preserves original history while replacing active context, interrupts synthesis, and restores the committed context from SQLite without repeating the tool effect. It also verifies driver-controlled context-overflow recovery using `DriverModelError`, interruption after the planned checkpoint but before history flush, and verifies that disabling the provider does not select another loop. It runs both the named `workflow` profile and a composition where the native default module is excluded and this plugin provides `driver.default`. Set `WUU_WORKFLOW_PROFILE=default` in its runtime environment to declare that service. Logs and the installed fixture remain under `.tmp/e2e/workflow-driver/`.

See the [English contract](../../../docs/en/customize/plugin-system.md) or [中文契约](../../../docs/zh-cn/customize/plugin-system.md) for operations, checkpoint ownership, current selection controls and compatibility limits.
