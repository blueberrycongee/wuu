import type { RuntimeHost, RuntimePlugin, ServiceDescriptor, ServiceInvokeParams } from './index.js';

export const SERVICE_CONTINUATION_HOST_FEATURE = 'service.continuation.v1';

/** Forward this selected tool-service invocation once to its configured
 * same-service delegate. The host preserves the original request and tool
 * authority; this helper sends no caller, target, or replacement arguments.
 * Use the callback's host. Returning, cancellation or explicit revocation ends
 * the authority; admitted callbacks retain their old generation across updates.
 * A failed delegate consumes the continuation too. There is no retry/fallback.
 * Declare SERVICE_CONTINUATION_HOST_FEATURE in required_host_features.
 */
export async function continueService<T = unknown>(host: RuntimeHost, invocation: ServiceInvokeParams): Promise<T> {
  if (!invocation.continuation_id) throw new Error('This invocation has no selected-service continuation');
  return await host.call('host.service.continue', {continuation_id: invocation.continuation_id}) as T;
}

/** Same callback identity and cancellation context as RuntimePlugin.invokeService. */
export type ServiceMethodOverride = NonNullable<RuntimePlugin['invokeService']>;

/** Override a few methods and explicitly delegate the others through this
 * callback's host. Declare the provided contract as a required service too,
 * and configure a consumer-scoped binding selecting its delegate. Without that
 * binding the host rejects a self/cyclic dependency before activation.
 *
 * This is dispatch, not error recovery: errors propagate without retries or a
 * fallback implementation. Forwarding grants no upstream caller/tool authority;
 * the host supplies the adapter's own authenticated callback identity.
 */
export function delegateService(
  descriptor: ServiceDescriptor,
  overrides: Readonly<Record<string, ServiceMethodOverride>> = {},
): ServiceMethodOverride {
  const service = descriptor.name;
  const methods = new Set(descriptor.methods.map(method => method.name));
  const handlers = {...overrides};
  for (const method of Object.keys(handlers)) {
    if (!methods.has(method)) throw new Error(`Service ${service} does not declare override ${method}`);
  }
  return async (input, host, execution) => {
    if (input.service !== service || !methods.has(input.method)) {
      throw new Error(`Service ${service} does not declare ${input.service}.${input.method}`);
    }
    if (Object.hasOwn(handlers, input.method)) return await handlers[input.method]!(input, host, execution);
    return await host.call('host.service.call', {service, method:input.method, params:input.params});
  };
}

/** The optional, callback-bound original operation for a selected tool replacement. */
export const NATIVE_TOOL_CONTINUATION_SERVICE = 'execution.native-tool-continuation';

/** Continue the exact host-admitted native call once. No arguments may be changed. */
export async function continueNativeTool(host: RuntimeHost): Promise<import('./index.js').ToolResult> {
  return await host.call('host.service.call', {service:NATIVE_TOOL_CONTINUATION_SERVICE, method:'call', params:{}}) as import('./index.js').ToolResult;
}
