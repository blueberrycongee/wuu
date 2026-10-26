import * as React from 'react';
import { createRoot } from 'react-dom/client';
import { flushSync } from 'react-dom';
import { ConversationPresentationBoundary, useConversationPresentationTransition, usePresentedNavigation } from '../src/renderer/ConversationPresentationTransition';
const fixture = window as unknown as Record<string, any>;
type Pending = { requestId: number; intentId: string; contentReady?: boolean };
function Workbench({ pending, target }: { pending?: Pending; target: string }) {
  const ref = React.useRef<HTMLDivElement>(null), [clicks, setClicks] = React.useState(0);
  fixture.present = usePresentedNavigation();
  useConversationPresentationTransition({ scopeRef: ref, pending, enabled: true });
  return <div style={{ display: 'grid', gridTemplateColumns: '220px 1fr', height: '700px', font: '16px system-ui', background: '#f6f3ed' }}>
    <aside style={{ padding: '20px', borderRight: '1px solid #ccc' }}><button onClick={() => setClicks(value => value + 1)}>Navigation {clicks}</button><p>Workspace A</p><p>Workspace B</p></aside>
    <main ref={ref} style={{ padding: '24px', overflow: 'hidden' }}><h2>{target}</h2><div className="history" style={{ height: '480px', overflow: 'auto', scrollbarWidth: 'none' }}>
      {Array.from({ length: 1000 }, (_, index) => <p key={index}>Conversation message {index}: retained text, <b>formatting</b>, and <code>code</code>.</p>)}
    </div><textarea disabled={!!pending} defaultValue="Preserved unsent draft" style={{ width: '95%', height: '70px', marginTop: '20px' }} /></main>
  </div>;
}
function Fixture() {
  const [pending, setPending] = React.useState<Pending>();
  const [target, setTarget] = React.useState('Source workspace');
  const [binding, setBinding] = React.useState(1), [retiring, setRetiring] = React.useState(false);
  fixture.begin = (timing = 'delayed') => {
    const requestedAt = performance.now();
    return fixture.present(async () => {
    fixture.dispatchLatencyMs = performance.now() - requestedAt;
    const next = { requestId: fixture.requestId = (fixture.requestId || 0) + 1, intentId: `intent-${fixture.requestId}` };
    setPending(next);
    if (timing === 'same-task') { setPending({ ...next, contentReady: true }); setTarget('Target workspace'); }
    if (timing === 'microtask') { await Promise.resolve(); flushSync(() => { setPending({ ...next, contentReady: true }); setTarget('Target workspace'); }); }
    });
  };
  fixture.commit = () => flushSync(() => { setPending(value => ({ ...value!, contentReady: true })); setTarget('Target workspace'); });
  fixture.retire = () => flushSync(() => setRetiring(true));
  fixture.replace = () => flushSync(() => { setBinding(value => value + 1); setRetiring(false); setPending(value => ({ ...value!, requestId: 1, contentReady: true })); setTarget('Successor workspace'); });
  fixture.reveal = () => flushSync(() => setPending(undefined));
  return <ConversationPresentationBoundary binding={binding} retiring={retiring} failed={false}>
    {retiring ? <div>Real provider recovery</div> : <Workbench key={binding} pending={pending} target={target} />}
  </ConversationPresentationBoundary>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);
