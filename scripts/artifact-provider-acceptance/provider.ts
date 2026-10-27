import { createHash } from "node:crypto";
import { ARTIFACT_STORE_DESCRIPTOR, runJSONLRuntime, RuntimeHostServiceError, type RuntimePlugin, type ArtifactStoreImport, type ArtifactResolve, type ArtifactRead, type ArtifactRange, type ImportedArtifact } from "@wuu/plugin-sdk";

// Independent SDK-only cloud fixture: opaque object identifiers, no WUU_HOME,
// directory layout, native provider imports, or local payload materialization.
const objects = new Map<string, { thread: string; artifact: ImportedArtifact; bytes: Buffer }>();
const reject = (code: string, message: string): never => { throw new RuntimeHostServiceError({code,message}); };
function object(input: ArtifactResolve) {
  const found = objects.get(input.uri);
  if (!found || found.thread !== input.thread_id) reject("artifact_unavailable", "object is not owned by this session");
  return found;
}
const started: {count:number,resolve():void}[] = [];
let readStarted = 0;
async function block(signal: AbortSignal) {
 readStarted++; for(const waiter of started) if(readStarted>=waiter.count) waiter.resolve();
 await new Promise<void>((_resolve,reject)=>{const abort=()=>{canceled++;reject(signal.reason);};if(signal.aborted)abort();else signal.addEventListener("abort",abort,{once:true});});
}
let canceled = 0;
const plugin: RuntimePlugin = {
  concurrentServices: ["fixture.artifacts.control", "state.artifacts"],
  initialize: () => ({protocol_version:3,lifecycle_version:2,provided_services:[ARTIFACT_STORE_DESCRIPTOR,{name:"fixture.artifacts.control",version:"1.0.0",methods:[{name:"wait-read",input_schema:"fixture.input.v1",output_schema:"fixture.output.v1"},{name:"cancellations",input_schema:"fixture.input.v1",output_schema:"fixture.output.v1"}]}]}),
  async invokeService({service,method,params}, _host, execution) {
    if(service === "fixture.artifacts.control") {
      if(method === "wait-read") {const count=(params as {count?:number}).count??1;if(readStarted<count)await new Promise<void>(resolve=>{started.push({count,resolve});});return {started:readStarted};}
      return {canceled};
    }
    if (method === "import") {
      const input = params as ArtifactStoreImport;
      if (!input.input.data || input.input.path) reject("invalid_params","fixture accepts inline upload only");
      const bytes=Buffer.from(input.input.data,"base64");
      const sha256=createHash("sha256").update(bytes).digest("hex");
      const id=`tenant / object ${objects.size+1}`;
      const artifact={id,uri:`fixture-cloud:bucket/${encodeURIComponent(id)}?etag=${sha256}`,name:input.input.name!,mime_type:input.input.mime_type!,size:bytes.length,sha256};
      objects.set(artifact.uri,{thread:input.scope.thread_id,artifact,bytes});return artifact;
    }
    if (method === "resolve") { const found=object(params as ArtifactResolve); if(found.artifact.name==="blocked-resolve.pdf")await block(execution.signal);return {managed:true,artifact:found.artifact};}
    if (method === "read-range") {
      const input=params as ArtifactRange;const found=object(input);
      if(input.sha256!==found.artifact.sha256 || !Number.isSafeInteger(input.offset) || input.offset<0 || input.offset>found.bytes.length || !Number.isSafeInteger(input.max_bytes) || input.max_bytes<=0 || input.max_bytes>1048576) reject("invalid_params","range identity or bounds changed");
      if(found.artifact.name === "blocked.pdf") {
        await block(execution.signal);
      }
      return {data:found.bytes.subarray(input.offset,input.offset+input.max_bytes).toString("base64"),mime_type:found.artifact.mime_type,sha256:found.artifact.sha256,offset:input.offset,total:found.bytes.length};
    }
    if (method === "read") { const input=params as ArtifactRead;const found=object(input);if(found.bytes.length>input.max_bytes)reject("limit_exceeded","read limit");return {data:found.bytes.toString("base64"),mime_type:found.artifact.mime_type}; }
    if (method === "delete-thread") {const thread=(params as {thread_id:string}).thread_id;for(const [uri,found] of objects)if(found.thread===thread)objects.delete(uri);return {thread_id:thread,deleted:true};}
    reject("method_not_found","method unavailable");
  },
};
await runJSONLRuntime(plugin, {input:process.stdin, output:process.stdout});
