import type { PluginGenerationApi, UISlotRenderProps } from '@wuu/plugin-sdk';
import { COUNTER_CONTROLS_SLOT, useCounter } from './contract.js';

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  api.registerUISlot({ id: 'panel', cardinality: 'many' });
  api.registerUISlot({ id: 'controls', cardinality: 'many' });
  function Counter({ slots: { Slot } }: UISlotRenderProps) {
    const { snapshot, error } = useCounter(api);
    return <section aria-label="Cooperative counter">
      <p>Counter: <output data-counter-value>{snapshot?.value ?? 'Loading'}</output></p>
      {error && <p role="status">Counter unavailable</p>}
      <Slot name={COUNTER_CONTROLS_SLOT} />
    </section>;
  }
  api.registerUIContribution({ id: 'counter', target: 'additive-counter/panel', render: Counter });
  api.registerUIContribution({ id: 'navigation', target: 'wuu.default-navigation/sidebar.primary',
    render: ({ slots: { Slot } }) => <Slot name="additive-counter/panel" /> });
}
