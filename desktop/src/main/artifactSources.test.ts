import { createHash } from "node:crypto";
import { describe, expect, it, vi } from "vitest";
import { ArtifactSources } from "./artifactSources";

// Public transport boundary: opaque provider IDs/URIs, exact range responses,
// abort/release/source revocation, changed generations, no local file fallback.
const bytes=Buffer.from("cloud-only document");
const sha256=createHash("sha256").update(bytes).digest("hex");
const input={uri:"s3+tenant:opaque/key?version=2",threadId:"thread",sha256};
function fixture() {
 let current=true;
 const request=vi.fn(async(method:string,params:any):Promise<any>=>{
  if(method==="thread/artifact/resolve")return {generation_id:"g1",artifact:{id:"cloud opaque ID / 2",uri:input.uri,name:"report.pdf",mime_type:"application/pdf",size:bytes.length,sha256}};
  if(params.generation_id!=="g1")throw Error("generation changed");
  return {data:bytes.subarray(params.offset,params.offset+params.max_bytes).toString("base64"),mime_type:"application/pdf",offset:params.offset,total:bytes.length,sha256};
 });
 const sources=new ArtifactSources();
 return {sources,request,revoke:()=>{current=false;},acquire:()=>sources.acquire(input,request,()=>current)};
}
describe("selected artifact transport",()=>{
 it("streams provider bytes and native ranges without interpreting the URI",async()=>{
  const {sources,acquire,request}=fixture();const lease=await acquire();
  const response=await sources.response(new Request(lease.url,{headers:{Range:"bytes=6-9"}}));
  expect(response.status).toBe(206);expect(response.headers.get("content-range")).toBe(`bytes 6-9/${bytes.length}`);expect(await response.text()).toBe("only");
  expect(request.mock.calls[1][1]).toMatchObject({uri:input.uri,thread_id:"thread",generation_id:"g1",sha256,offset:6,max_bytes:4});
 });
 it("revokes URLs on explicit release and document retirement",async()=>{
  const {sources,acquire,revoke}=fixture();const lease=await acquire();sources.release(lease.leaseId);
  expect((await sources.response(new Request(lease.url))).status).toBe(404);
  const next=await acquire();revoke();expect((await sources.response(new Request(next.url))).status).toBe(404);
  const pending=fixture(); let resolved!: (value:any)=>void;
  pending.request.mockImplementationOnce(()=>new Promise(resolve=>{resolved=resolve;}));
  const acquisition=pending.sources.acquire({...input,requestId:"pending-document"},pending.request,()=>true,7);
  expect(pending.sources.owns("wuu-artifact://lease/pending-document",7)).toBe(true);
  pending.sources.release("pending-document");
  resolved({generation_id:"g1",artifact:{id:"opaque",uri:input.uri,name:"report.pdf",mime_type:"application/pdf",size:bytes.length,sha256}});
  await expect(acquisition).rejects.toThrow("no longer active");
  expect((await pending.sources.response(new Request("wuu-artifact://lease/pending-document"))).status).toBe(404);
 });
 it("rejects URL-only access and stale provider identity",async()=>{
  const {sources,acquire,request}=fixture();expect((await sources.response(new Request("wuu-artifact://workspace/thread/anything.pdf"))).status).toBe(404);
  const lease=await acquire();request.mockImplementationOnce(async()=>({data:bytes.toString("base64"),mime_type:"application/pdf",offset:0,total:bytes.length,sha256:"0".repeat(64)}));
  await expect((await sources.response(new Request(lease.url))).text()).rejects.toThrow();
 });
 it("does not issue later reads after request cancellation",async()=>{
  const {sources,acquire,request}=fixture();const lease=await acquire();const abort=new AbortController();abort.abort();
  await expect(sources.response(new Request(lease.url,{signal:abort.signal}))).rejects.toMatchObject({name:"AbortError"});expect(request).toHaveBeenCalledTimes(1);
  // A captured transport registers cleanup when the operation is admitted,
  // rather than granting a generic stale-route RPC exception at cancellation.
  const captured=fixture(); const cleanup=vi.fn(async(_descriptor:unknown)=>{});let finish!:(value:unknown)=>void;
  const owned=vi.fn((method:string,params:any,descriptor:unknown)=>({
    result:method==="thread/artifact/resolve" ? captured.request(method,params) : new Promise(resolve=>{finish=resolve;}),
    release:async()=>{await cleanup(descriptor);finish?.({});},
  }));
  Object.assign(captured.request,{owned});
  const held=await captured.acquire();const running=new AbortController();
  const reply=await captured.sources.response(new Request(held.url,{signal:running.signal}));const reading=reply.text();
  await vi.waitFor(()=>expect(owned).toHaveBeenCalledTimes(2));running.abort();
  await expect(reading).rejects.toMatchObject({name:"AbortError"});
  expect(cleanup).toHaveBeenCalledExactlyOnceWith({method:"thread/artifact/cancel",params:{thread_id:"thread",request_id:expect.any(String)}});
 });
});
