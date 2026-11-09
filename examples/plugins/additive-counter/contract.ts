import type { PluginGenerationApi, PluginRuntimeInvokeOptions, ServiceDescriptor } from '@wuu/plugin-sdk';

/** Public domain contract exported by A, not a host-specific registry. */
export const COUNTER_SERVICE = 'example.counter';
export const COUNTER_CONTROLS_SLOT = 'additive-counter/controls';
export interface CounterSnapshot { readonly value: number; readonly revision: number }
export const counterDescriptor: ServiceDescriptor = {
  name: COUNTER_SERVICE, version: '1.0.0',
  methods: ['read', 'add', 'watch'].map(name => ({ name, input_schema: `example.counter.${name}.input.v1`, output_schema: 'example.counter.snapshot.v1' })),
};

export function counterClient(api: Pick<PluginGenerationApi, 'invokeService'>) {
  const call = (method: string, params: unknown, options?: PluginRuntimeInvokeOptions) =>
    api.invokeService<CounterSnapshot>({ service: COUNTER_SERVICE, method, params }, options);
  return {
    read: (options?: PluginRuntimeInvokeOptions) => call('read', {}, options),
    add: (delta: number, options?: PluginRuntimeInvokeOptions) => call('add', { delta }, options),
    watch: (after: number, options: PluginRuntimeInvokeOptions) => call('watch', { after }, options),
  };
}

/** Long polling is a domain protocol over ordinary cancellable service calls.
 * Unmount and generation disposal end the subscription; no private event bus.
 */
export function useCounter(api: PluginGenerationApi) {
  const React = api.react;
  const [snapshot, setSnapshot] = React.useState<CounterSnapshot>();
  const [error, setError] = React.useState<string>();
  React.useEffect(() => {
    const lifetime = new AbortController(), client = counterClient(api);
    const stop = () => lifetime.abort();
    api.signal.addEventListener('abort', stop, { once: true });
    void (async () => {
      try {
        let next = await client.read({ signal: lifetime.signal });
        while (!lifetime.signal.aborted) {
          setSnapshot(next);
          next = await client.watch(next.revision, { signal: lifetime.signal });
        }
      } catch (failure) {
        if (!lifetime.signal.aborted) setError(String(failure));
      }
    })();
    return () => { stop(); api.signal.removeEventListener('abort', stop); };
  }, [api]);
  return { snapshot, error };
}
