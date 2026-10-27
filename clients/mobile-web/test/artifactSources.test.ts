import { afterEach, describe, expect, it, vi } from 'vitest';
import { BrowserArtifactSources, BROWSER_ARTIFACT_SOURCE_CAPABILITIES } from '../src/lib/artifactSources';

const bytes = new TextEncoder().encode('cloud-backed preview');
const sha = async (value: Uint8Array) => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', value as Uint8Array<ArrayBuffer>)), b => b.toString(16).padStart(2, '0')).join('');
const deferred = <T>() => { let resolve!: (value:T)=>void; const promise=new Promise<T>(done=>{resolve=done});return {promise,resolve}; };
afterEach(()=>vi.restoreAllMocks());

async function fixture() {
 const digest=await sha(bytes);
 const input={uri:'s3://selected-cloud/session/image',threadId:'thread',sha256:digest,requestId:'request-1'};
 const artifact={id:'resource',uri:input.uri,name:'preview.txt',mime_type:'text/plain',size:bytes.length,sha256:digest};
 const calls:Array<{method:string;params:Record<string,unknown>}>=[];
 const request=async<T>(method:string,raw:unknown):Promise<T>=>{
  const params=raw as Record<string,unknown>;calls.push({method,params});
  if(method==='thread/artifact/resolve')return {artifact,generation_id:'provider-generation'} as T;
  if(method==='thread/artifact/cancel')return {ok:true} as T;
  return {data:btoa(String.fromCharCode(...bytes)),mime_type:artifact.mime_type,total:bytes.length,offset:0,sha256:digest} as T;
 };
 return {input,artifact,calls,request};
}
describe('browser selected artifact source leases',()=>{
 it('uses authenticated range RPCs, verifies digest and never fetches a provider URI',async()=>{
  const {input,request,calls}=await fixture();const sources=new BrowserArtifactSources();const fetchSpy=vi.spyOn(globalThis,'fetch');
  const lease=await sources.acquire(input,request,()=>true,'/workspace');
  expect(lease.leaseId).toBe(input.requestId);expect(lease.url).toMatch(/^blob:/);expect(fetchSpy).not.toHaveBeenCalled();
  expect(calls.map(c=>c.method)).toEqual(['thread/artifact/resolve','thread/artifact/read','thread/artifact/resolve']);
  expect(calls[1].params).toMatchObject({thread_id:'thread',uri:input.uri,sha256:input.sha256,generation_id:'provider-generation',offset:0,max_bytes:bytes.length});
  expect(new Uint8Array(await (await fetch(lease.url)).arrayBuffer())).toEqual(bytes);
  sources.release(lease.leaseId);await expect(fetch(lease.url)).rejects.toThrow();
 });
 it('rejects declared oversized resources before requesting any bytes',async()=>{
  const {input,artifact,calls,request}=await fixture();artifact.size=BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxBytes+1;
  const sources=new BrowserArtifactSources();await expect(sources.acquire(input,request,()=>true,'/a')).rejects.toMatchObject({code:'artifact_preview_too_large',limit:BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxBytes});
  expect(calls.map(c=>c.method)).toEqual(['thread/artifact/resolve']);
 });
 it('cancels an admitted range and never publishes a late URL',async()=>{
  const {input,request}=await fixture();const pending=deferred<unknown>();let chunkRequest:Record<string,unknown>|undefined;
  const cancel=vi.fn();const sources=new BrowserArtifactSources();
  const work=sources.acquire(input,async<T>(method:string,raw:unknown)=>{
   if(method==='thread/artifact/read'){chunkRequest=raw as Record<string,unknown>;return pending.promise as Promise<T>}
   if(method==='thread/artifact/cancel'){cancel(raw);return {ok:true} as T}
   return request<T>(method,raw);
  },()=>true,'/a');
  await vi.waitFor(()=>expect(chunkRequest).toBeDefined());sources.release(input.requestId);
  await expect(work).rejects.toThrow(/released|aborted/);
  expect(cancel).toHaveBeenCalledWith({thread_id:input.threadId,request_id:chunkRequest?.request_id});
  pending.resolve({});
 });
 it('rejects changed generation and wrong bytes before exposing a Blob',async()=>{
  const {input,request}=await fixture();const create=vi.spyOn(URL,'createObjectURL');
  for(const kind of ['generation','digest']){
   let resolved=0;const sources=new BrowserArtifactSources();
   await expect(sources.acquire({...input,requestId:kind},async<T>(method:string,raw:unknown)=>{
    const response=await request<Record<string,unknown>>(method,raw);
    if(method==='thread/artifact/resolve'&&++resolved>1&&kind==='generation')response.generation_id='changed';
    if(method==='thread/artifact/read'&&kind==='digest')response.data=btoa('x'.repeat(bytes.length));
    return response as T;
   },()=>true,'/a')).rejects.toThrow(/changed|digest/);
  }
  expect(create).not.toHaveBeenCalled();
 });
 it('revokes only owned workspace leases and pending work on cleanup',async()=>{
  const {input,request}=await fixture();const sources=new BrowserArtifactSources();
  const a=await sources.acquire(input,request,()=>true,'/a');
  const b=await sources.acquire({...input,requestId:'second'},request,()=>true,'/b');
  expect(sources.owns(a.url)).toBe(true);expect(sources.owns('blob:forged')).toBe(false);
  sources.clear('/a');expect(sources.owns(a.url)).toBe(false);expect(sources.owns(b.url)).toBe(true);
  sources.clear();expect(sources.owns(b.url)).toBe(false);
 });
});

it('reserves the aggregate budget before concurrent ranges and returns it on cancellation',async()=>{
 const {input,artifact}=await fixture();artifact.size=BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxBytes;
 const sources=new BrowserArtifactSources();let reads=0;
 const request=async<T>(method:string):Promise<T>=>{
  if(method==='thread/artifact/resolve')return {artifact,generation_id:'provider-generation'} as T;
  if(method==='thread/artifact/cancel')return {ok:true} as T;
  reads++;return new Promise<T>(()=>{});
 };
 const count=BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxTotalBytes/BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxBytes;
 const jobs=Array.from({length:count},(_,i)=>sources.acquire({...input,requestId:`reserved-${i}`},request,()=>true,'/a').catch(error=>error));
 await vi.waitFor(()=>expect(reads).toBe(count));
 await expect(sources.acquire({...input,requestId:'over-budget'},request,()=>true,'/a')).rejects.toMatchObject({code:'artifact_preview_too_large',limit:BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxTotalBytes});
 expect(reads).toBe(count);sources.clear();await Promise.all(jobs);
 const fresh=sources.acquire({...input,requestId:'fresh'},request,()=>true,'/a').catch(error=>error);
 await vi.waitFor(()=>expect(reads).toBe(count+1));sources.clear();await fresh;
});

it('refuses HTML blobs that cannot enforce native response CSP',async()=>{
 const {input,artifact,request,calls}=await fixture();artifact.mime_type='text/html';
 await expect(new BrowserArtifactSources().acquire(input,request,()=>true,'/a')).rejects.toThrow(/HTML.*desktop/);
 expect(calls.map(c=>c.method)).toEqual(['thread/artifact/resolve']);
});


it('cancels a blocked provider metadata resolve through the same captured route', async () => {
  const { input } = await fixture();
  const pending = deferred<unknown>();
  const cancel = vi.fn();
  const sources = new BrowserArtifactSources();
  const work = sources.acquire(input, async <T>(method: string, params: unknown) => {
    if (method === 'thread/artifact/resolve') return pending.promise as Promise<T>;
    cancel(params); return { ok: true } as T;
  }, () => true, '/a');
  sources.release(input.requestId);
  await expect(work).rejects.toThrow(/released/);
  expect(cancel).toHaveBeenCalledWith({ thread_id: input.threadId, request_id: input.requestId });
  pending.resolve({});
});
