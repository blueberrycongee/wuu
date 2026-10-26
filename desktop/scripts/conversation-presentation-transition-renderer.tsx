import * as React from 'react';
import { createRoot } from 'react-dom/client';
import { flushSync } from 'react-dom';
import type { ApplicationRetirementPreparation } from '../src/renderer/plugins/ApplicationProviders';
import { ConversationPresentationBoundary, useConversationPresentationTransition, usePresentedNavigation } from '../src/renderer/ConversationPresentationTransition';
const fixture = window as unknown as Record<string, any>;
const startTransition = document.startViewTransition.bind(document);
document.startViewTransition = update => {
  const transition = startTransition(update);
  fixture.transitionReady = transition.ready;
  fixture.transitionFinished = transition.finished;
  return transition;
};
type Pending = { requestId: number; intentId: string; contentReady?: boolean };
type Geometry = { sidebar: number; footer: number };
function Workbench({ pending, target, geometry }: { pending?: Pending; target: string; geometry: Geometry }) {
  const ref = React.useRef<HTMLDivElement>(null), messages = React.useRef<HTMLDivElement>(null), [clicks, setClicks] = React.useState(0);
  fixture.present = usePresentedNavigation();
  useConversationPresentationTransition({ scopeRef: ref, pending, enabled: true, getRegions: React.useCallback(() => messages.current ? [{ element: messages.current }] : [], []) });
  return <div style={{ display: 'grid', gridTemplateColumns: `${geometry.sidebar}px 1fr`, height: '700px', font: '16px system-ui', background: '#f6f3ed' }}>
    <aside style={{ padding: '20px', borderRight: '1px solid #ccc' }}><button onClick={() => setClicks(value => value + 1)}>Navigation {clicks}</button><p>Workspace A</p><p>Workspace B</p></aside>
    <main ref={ref} style={{ padding: '24px', overflow: 'hidden' }}><h2>{target}</h2><div ref={messages} className="history" style={{ height: `${550 - geometry.footer}px`, overflow: 'auto', scrollbarWidth: 'none' }}>
      {Array.from({ length: 1000 }, (_, index) => <p key={index}>{target} message {index}: retained text, <b>formatting</b>, and <code>code</code>.</p>)}
    </div><textarea defaultValue="Preserved unsent draft" style={{ width: '95%', height: `${geometry.footer}px`, marginTop: '20px', background: '#fff', color: '#222', border: '1px solid #aaa' }} /></main>
  </div>;
}
function Fixture() {
  const preparations = React.useRef(new Set<(preparation: ApplicationRetirementPreparation) => void | Promise<void>>());
  const subscribeRetirementPreparation = React.useCallback((listener: (preparation: ApplicationRetirementPreparation) => void | Promise<void>) => {
    preparations.current.add(listener); return () => { preparations.current.delete(listener); };
  }, []);
  const [pending, setPending] = React.useState<Pending>();
  const [target, setTarget] = React.useState('Source workspace');
  const [geometry, setGeometry] = React.useState<Geometry>({ sidebar: 220, footer: 70 });
  fixture.geometry = (next: Geometry) => flushSync(() => setGeometry(next));
  const [binding, setBinding] = React.useState(1), [retiring, setRetiring] = React.useState(false);
  fixture.begin = (timing = 'delayed') => {
    const requestedAt = performance.now();
    return fixture.present(async () => {
    fixture.dispatchedAt = performance.now();
    fixture.dispatchLatencyMs = performance.now() - requestedAt;
    const next = { requestId: fixture.requestId = (fixture.requestId || 0) + 1, intentId: `intent-${fixture.requestId}` };
    setPending(next);
    if (timing === 'same-task') { setPending({ ...next, contentReady: true }); setTarget('Target workspace'); }
    if (timing === 'microtask') { await Promise.resolve(); flushSync(() => { setPending({ ...next, contentReady: true }); setTarget('Target workspace'); }); }
    });
  };
  fixture.commit = () => flushSync(() => { setPending(value => ({ ...value!, contentReady: true })); setTarget('Target workspace'); });
  fixture.prepareRetirement = () => Promise.all([...preparations.current].map(listener => listener({ binding, signal: new AbortController().signal })));
  fixture.waitInitialAge = (age: number) => new Promise(resolve => setTimeout(resolve, Math.max(0, age - (performance.now() - fixture.dispatchedAt))));
  fixture.retire = () => flushSync(() => setRetiring(true));
  fixture.replace = (next?: Geometry) => flushSync(() => { if (next) setGeometry(next); setBinding(value => value + 1); setRetiring(false); setPending(value => ({ ...value!, requestId: 1, contentReady: true })); setTarget('Successor workspace'); });
  fixture.reveal = () => flushSync(() => setPending(undefined));
  return <ConversationPresentationBoundary binding={binding} retiring={retiring} failed={false} subscribeRetirementPreparation={subscribeRetirementPreparation}>
    {retiring ? <div>Real provider recovery</div> : <Workbench key={binding} pending={pending} target={target} geometry={geometry} />}
  </ConversationPresentationBoundary>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);
