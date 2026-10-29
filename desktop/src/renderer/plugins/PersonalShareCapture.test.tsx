import * as React from 'react';
import {createRoot,type Root} from 'react-dom/client';
import {afterEach,expect,it,vi} from 'vitest';
import {createShareCapturePanel} from '../../../../plugins/personal/ui/share-capture-panel';
// Receiver lifecycle: duplicate click suppression, uncertain replay identity,
// successful clear and unmount safety. No account or network fixtures.
let root:Root|undefined,container:HTMLDivElement;
afterEach(()=>{if(root)React.act(()=>root?.unmount());container?.remove();root=undefined;});
async function mount(invoke:(method:string,input?:unknown)=>Promise<unknown>){container=document.createElement('div');document.body.append(container);root=createRoot(container);const Panel=createShareCapturePanel(React,{},invoke);await React.act(async()=>root!.render(<Panel/>));}
async function fill(text:string){const area=container.querySelector('textarea')!;await React.act(async()=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value')!.set!.call(area,text);area.dispatchEvent(new Event('input',{bubbles:true}));});}
function save(){return Array.from(container.querySelectorAll('button')).find(b=>b.textContent==='保存到主聊天')!;}
it('retries uncertain delivery with exact identity and clears only on receipt',async()=>{let failed=true;const invoke=vi.fn(async(_method:string,_input?:unknown)=>{if(failed)throw Error('lost');return {state:'queued'};});await mount(invoke);await fill('A quote');expect(invoke).not.toHaveBeenCalled();await React.act(async()=>save().click());expect(container.textContent).toContain('未确认');await React.act(async()=>save().click());expect(invoke.mock.calls).toHaveLength(2);expect(invoke.mock.calls[0][0]).toBe('capture.send');expect(invoke.mock.calls[0][1]).toEqual(invoke.mock.calls[1][1]);expect((invoke.mock.calls[0][1] as {prompt:string}).prompt).toContain('untrusted source data');failed=false;await React.act(async()=>save().click());expect(container.querySelector('textarea')!.value).toBe('');expect(container.textContent).toContain('已交给主聊天');});
it('clear never invokes host and late response cannot resubmit',async()=>{let finish!:(v:unknown)=>void;const invoke=vi.fn(()=>new Promise(r=>finish=r));await mount(invoke);await fill('discard');await React.act(async()=>Array.from(container.querySelectorAll('button')).find(b=>b.textContent==='清空')!.click());expect(invoke).not.toHaveBeenCalled();await fill('retained');await React.act(async()=>{const button=save();button.click();button.click();});expect(invoke).toHaveBeenCalledTimes(1);await React.act(async()=>root!.unmount());root=undefined;await React.act(async()=>finish({state:'queued'}));expect(invoke).toHaveBeenCalledTimes(1);});

it.each(['text', 'url', 'submit'])('fences a pending import after a newer %s action', async action => {
  let finish!: (value: string) => void;
  const invoke = vi.fn(async () => ({ state: 'queued' }));
  await mount(invoke); await fill('Current draft');
  const picker = container.querySelector<HTMLInputElement>('input[type=file]')!;
  Object.defineProperty(picker, 'files', { value: [{ size: 100, text: () => new Promise<string>(resolve => { finish = resolve; }) }] });
  await React.act(async () => picker.dispatchEvent(new Event('change', { bubbles: true })));
  if (action === 'text') await fill('Newer draft');
  else if (action === 'url') {
    const input = container.querySelector<HTMLInputElement>('input[type=url]')!;
    await React.act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'https://example.com/new'); input.dispatchEvent(new Event('input', { bubbles: true })); });
  } else await React.act(async () => save().click());
  await React.act(async () => finish(JSON.stringify({ schema: 'wuu.share.v1', capture_id: 'older-import', kind: 'text', text: 'Obsolete imported text' })));
  expect(container.querySelector('textarea')!.value).toBe(action === 'text' ? 'Newer draft' : action === 'submit' ? '' : 'Current draft');
  expect(container.querySelector<HTMLInputElement>('input[type=url]')!.value).toBe(action === 'url' ? 'https://example.com/new' : '');
  expect(invoke).toHaveBeenCalledTimes(action === 'submit' ? 1 : 0);
});
