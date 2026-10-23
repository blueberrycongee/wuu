# Evidence workflow driver

This independent TypeScript plugin plans one evidence-gathering tool batch and then synthesizes an answer with tools disabled. It uses only `@wuu/plugin-sdk` and runs directly with Node's TypeScript support.

From the repository root:

```bash
node scripts/workflow-driver-e2e.mjs
```

The check installs a packed public SDK in an independent directory, reads a real fixture file, interrupts synthesis, resumes without repeating the tool effect, and verifies that disabling the provider does not select another loop. Logs and the installed fixture remain under `.tmp/e2e/workflow-driver/`.

See the [English contract](../../../docs/en/customize/plugin-system.md) or [中文契约](../../../docs/zh-cn/customize/plugin-system.md) for operations, checkpoint ownership, current selection controls and compatibility limits.
