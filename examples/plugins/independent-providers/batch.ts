import {
  TOOL_BATCH_SERVICE_DESCRIPTOR, requireToolBatchResources, toolBatchResources,
  type RuntimePlugin, type ToolBatchRequest,
} from '@wuu/plugin-sdk';

/** A complete external batch backend: reverse order, serial dispatch, explicit
 * rejection of malformed input, and resource-backed durable per-call outcomes.
 * It works with any selected agent.tools@1 registry, including native PTC tools.
 * Bind agent.tool.batch@1 to this module; do not replace driver.default.
 */
export function createReverseBatchProvider(): RuntimePlugin {
  return {
    concurrentServices: ['agent.tool.batch'],
    initialize: () => ({
      protocol_version: 3, lifecycle_version: 2,
      provided_services: [TOOL_BATCH_SERVICE_DESCRIPTOR],
      required_services: requireToolBatchResources(),
    }),
    async invokeService({ method, params }, host) {
      if (method !== 'execute') throw new Error(`Unknown batch method: ${method}`);
      const batch = params as ToolBatchRequest;
      const resources = toolBatchResources(host, batch);
      for (let index = batch.calls.length - 1; index >= 0; index--) {
        const resolution = await resources.resolve(index);
        if (resolution.error) {
          await resources.settle(index, { content: [{ type: 'text', text: resolution.error }], is_error: true });
        } else {
          await resources.execute(index);
        }
      }
      return { completed: true };
    },
  };
}
