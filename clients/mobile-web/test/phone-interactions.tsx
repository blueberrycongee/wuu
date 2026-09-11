import { useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { RuntimePicker } from '../../../desktop/src/renderer/ComposerRuntimeMenus';
import { FloatingMenuPortal } from '../../../desktop/src/renderer/ComposerFloatingMenu';
import { AccountPanel } from '../../../desktop/src/renderer/AccountPanel';
import { WuuUIRoot } from '../../../desktop/src/renderer/ui/layers/UILayerHost';
import { setActiveLocale } from '../../../desktop/src/renderer/i18n';
import { startWebViewportSync } from '../src/lib/viewport';
import type { InitializeResult } from '../../../desktop/src/shared/protocol';
import '../../../desktop/src/renderer/styles.css';
import '../src/styles.css';
import '../src/workbench.css';

document.documentElement.dataset.hostKind = 'web';
setActiveLocale('en-US');
startWebViewportSync();
localStorage.setItem('wuu.account.server', 'https://example.invalid');
const accountDriver = async () => ({ github: false });

function Fixture() {
  const anchor = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState<'model' | null>(null);
  const [draft, setDraft] = useState('Unsent phone draft');
  const [model, setModel] = useState('model-0');
  const [provider, setProvider] = useState('first');
  const models = Array.from({ length: 30 }, (_, index) => ({ id: `model-${index}`, display_name: `Model ${index}` }));
  const initialized = { protocol_version: 'wuu-app-server/v0.1', provider, model, variant: '', workspace_root: '',
    providers: ['first', 'second'].map(name => ({ name, type: 'openai', model: 'model-0', models })) } as InitializeResult;
  if (location.search.includes('login')) return <main className="account-home"><AccountPanel driver={accountDriver} presentation="mobile" onComputer={() => {}} /></main>;
  return <WuuUIRoot><main style={{ height: '100%', display: 'flex', flexDirection: 'column', justifyContent: 'flex-end' }}>
    <p data-testid="selection">{provider}/{model}</p>
    <textarea aria-label="Draft" value={draft} onChange={event => setDraft(event.target.value)} style={{ minHeight: 48, fontSize: 16 }} />
    {location.search.includes('anchored') ? <>
      <div ref={anchor} style={{ height: 48 }}><button onClick={() => setOpen('model')}>Anchored menu</button></div>
      {open && <FloatingMenuPortal anchorRef={anchor} owner="codex-runtime" placement="above" align="left" width={200}><div style={{ height: 100 }}>Anchored content</div></FloatingMenuPortal>}
    </> : <RuntimePicker anchorRef={anchor} initialized={initialized} state={{ loading: false, error: '', models: [] }}
      openMenu={open} running={false} onToggleMenu={() => setOpen(current => current ? null : 'model')}
      onSelectModel={(nextProvider, nextModel) => { setProvider(nextProvider); setModel(nextModel); }} onSelectEffort={() => {}} />}
  </main></WuuUIRoot>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);
