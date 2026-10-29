import * as React from 'react';import {createRoot,type Root} from 'react-dom/client';import {afterEach,it,expect,vi} from 'vitest';
import {createConnectorOAuth} from '../../../../plugins/personal/ui/connector-oauth';
let root:Root|undefined,node:HTMLDivElement;afterEach(()=>{if(root)React.act(()=>root?.unmount());node?.remove();root=undefined});
// Lifecycle failure contracts: no OAuth on mount, close while Start is in flight,
// unverified state never becomes connected, no secret/provider error echo.
it('only explicit start can authorize and a late response after close is cancelled',async()=>{let done:(x:unknown)=>void=()=>{};const service={start:vi.fn(()=>new Promise(resolve=>{done=resolve})),attempt:vi.fn(),cancel:vi.fn(async()=>({ok:true}))};node=document.createElement('div');document.body.append(node);root=createRoot(node);const Panel=createConnectorOAuth(React);await React.act(async()=>root?.render(<Panel provider="gmail" service={service} onConnected={()=>{}}/>));expect(service.start).not.toHaveBeenCalled();await React.act(async()=>node.querySelector<HTMLButtonElement>('[data-oauth-start]')!.click());expect(service.start).toHaveBeenCalledTimes(1);await React.act(async()=>root?.unmount());root=undefined;await React.act(async()=>done({attempt_id:'a',state:'pending'}));expect(service.cancel).toHaveBeenCalledWith({attempt_id:'a'});expect(service.attempt).not.toHaveBeenCalled();});

function deferred() {
  let resolve!: (value: unknown) => void, reject!: (error: Error) => void;
  const promise = new Promise<unknown>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function mount(service: Parameters<ReturnType<typeof createConnectorOAuth>>[0]['service'], onConnected = vi.fn()) {
  node = document.createElement('div'); document.body.append(node); root = createRoot(node);
  const Panel = createConnectorOAuth(React);
  await React.act(async () => root!.render(<Panel provider="gmail" service={service} onConnected={onConnected} />));
  return onConnected;
}
const start = () => node.querySelector<HTMLButtonElement>('[data-oauth-start]')!;
const cancel = () => [...node.querySelectorAll('button')].find(button => button.textContent === '取消授权')!;

it('cancels an obsolete start by its own identity without replacing the newer attempt', async () => {
  const first = deferred(), second = deferred(), poll = deferred();
  const service = { start: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise), attempt: vi.fn(() => poll.promise), cancel: vi.fn(async () => ({})) };
  await mount(service);
  await React.act(async () => start().click());
  await React.act(async () => cancel().click());
  await React.act(async () => start().click());
  await React.act(async () => second.resolve({ attempt_id: 'new', state: 'pending' }));
  await React.act(async () => first.resolve({ attempt_id: 'old', state: 'pending' }));
  expect(service.cancel.mock.calls).toEqual([[{ attempt_id: 'old' }]]);
  expect(service.attempt.mock.calls).toEqual([[{ attempt_id: 'new' }]]);
  expect(start().disabled).toBe(true);
  await React.act(async () => cancel().click());
  expect(service.cancel.mock.calls).toEqual([[{ attempt_id: 'old' }], [{ attempt_id: 'new' }]]);
});

it('does not let a rejected obsolete start enable another start over its replacement', async () => {
  const first = deferred(), second = deferred();
  const service = { start: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise), attempt: vi.fn(), cancel: vi.fn(async () => ({})) };
  await mount(service);
  await React.act(async () => start().click());
  await React.act(async () => cancel().click());
  await React.act(async () => start().click());
  await React.act(async () => first.reject(new Error('old start failed')));
  expect(start().disabled).toBe(true);
});

it.each(['connected', 'rejected'])('ignores an obsolete poll that settles as %s after restart', async outcome => {
  const first = deferred(), second = deferred();
  const service = { start: vi.fn().mockResolvedValueOnce({ attempt_id: 'old', state: 'pending' }).mockResolvedValueOnce({ attempt_id: 'new', state: 'pending' }), attempt: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise), cancel: vi.fn(async () => ({})) };
  const connected = await mount(service);
  await React.act(async () => start().click());
  await React.act(async () => cancel().click());
  await React.act(async () => start().click());
  const currentStatus = node.querySelector('[role=status]')!.textContent;
  await React.act(async () => { if (outcome === 'connected') first.resolve({ attempt_id: 'old', state: 'connected' }); else first.reject(new Error('obsolete poll')); });
  expect(connected).not.toHaveBeenCalled();
  expect(start().disabled).toBe(true);
  expect(node.querySelector('[role=status]')!.textContent).toBe(currentStatus);
  await React.act(async () => second.resolve({ attempt_id: 'new', state: 'connected' }));
  expect(connected).toHaveBeenCalledTimes(1);
  expect(start().disabled).toBe(false);
});

it.each(['transport_lost', 'authorization_already_completed'])('keeps an earlier cancellation warning without changing its replacement (%s)', async code => {
  const first = deferred(), second = deferred(), poll = deferred();
  const service = { start: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise), attempt: vi.fn(() => poll.promise), cancel: vi.fn(async () => { throw Object.assign(new Error(code), { code }); }) };
  const connected = await mount(service);
  await React.act(async () => start().click());
  await React.act(async () => cancel().click());
  await React.act(async () => start().click());
  await React.act(async () => second.resolve({ attempt_id: 'new', state: 'pending' }));
  const currentStatus = node.querySelector('[role=status]')!.textContent;
  await React.act(async () => first.resolve({ attempt_id: 'old', state: 'pending' }));
  expect(service.cancel.mock.calls).toEqual([[{ attempt_id: 'old' }]]);
  expect(node.querySelector('[data-oauth-cancellation-unknown]')).not.toBeNull();
  expect(node.querySelector('[role=status]')!.textContent).toBe(currentStatus);
  expect(start().disabled).toBe(true);
  expect(connected).not.toHaveBeenCalled();
  await React.act(async () => poll.resolve({ attempt_id: 'new', state: 'connected' }));
  expect(connected).toHaveBeenCalledTimes(1);
  expect(start().disabled).toBe(false);
  expect(node.querySelector('[data-oauth-cancellation-unknown]')).not.toBeNull();
});
