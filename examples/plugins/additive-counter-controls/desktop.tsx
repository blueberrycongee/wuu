import type { PluginGenerationApi } from '@wuu/plugin-sdk';
import { COUNTER_CONTROLS_SLOT, counterClient, useCounter } from '@wuu-example/additive-counter/contract';

/** B adds UI and invokes A's published service. It never edits or replaces A. */
export function activate(api: PluginGenerationApi): void {
  const React = api.react, { Button } = api.ui;
  function Controls() {
    const { snapshot, error } = useCounter(api);
    const [pending, setPending] = React.useState(false);
    const [failure, setFailure] = React.useState<string>();
    const add = async () => {
      setPending(true); setFailure(undefined);
      try { await counterClient(api).add(1); }
      catch (reason) { if (!api.signal.aborted) setFailure(String(reason)); }
      finally { if (!api.signal.aborted) setPending(false); }
    };
    return <div data-counter-controls={api.manifestId}>
      <Button disabled={!snapshot || !!error || pending} onClick={() => { void add(); }}>Add one</Button>
      <output aria-label="Observed counter">{snapshot?.value}</output>
      {(error || failure) && <p role="status">Counter unavailable</p>}
    </div>;
  }
  api.registerUIContribution({ id: 'controls', target: COUNTER_CONTROLS_SLOT, order: 10, render: Controls });
}
