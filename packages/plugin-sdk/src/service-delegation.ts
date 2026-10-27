import type { RuntimePlugin, ServiceDescriptor } from './index.js';

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
