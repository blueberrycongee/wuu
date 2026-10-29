import {readFileSync} from 'node:fs';
import {continueNativeTool, NATIVE_TOOL_CONTINUATION_SERVICE, runJSONLRuntime, type RuntimePlugin, type RuntimeHost} from '@wuu/plugin-sdk';
let saved: RuntimeHost | undefined;
let sharedReady!: () => void;
let sharedFinish!: () => void;
const ready = new Promise<void>(resolve => {sharedReady = resolve;});
const finish = new Promise<void>(resolve => {sharedFinish = resolve;});
const plugin: RuntimePlugin = {
 initialize() { return {protocol_version:3,lifecycle_version:2, required_services:[{name:NATIVE_TOOL_CONTINUATION_SERVICE,major_version:1,required:true}],tools:[{id:'search',description:'Delegate the search once.',input_schema:JSON.parse(readFileSync(new URL('./schema.json',import.meta.url),'utf8')),activity:{read_only:true,concurrency_safe:true,risk:'low'}}]}; },
 async executeTool(input,host) {
  const query=(input.arguments as {query:string}).query;
  if(query==='share-first') { saved=host; sharedReady(); await finish; return {result:await continueNativeTool(host)}; }
  if(query==='share-second') {
   await ready; if(!saved) throw new Error('missing shared host');
   const captured=await continueNativeTool(saved); sharedFinish();
   const value=JSON.parse(captured.content![0]!.text!); value.query='share-second';
   return {result:{content:[{type:'text',text:JSON.stringify(value)}]}};
  }
  if(query==='late') {
   if(!saved) throw new Error('no saved callback');
   let revoked=false;
   try {await continueNativeTool(saved);} catch {revoked=true;}
   if(!revoked) throw new Error('completed callback retained continuation');
  }
  if(query==='save') saved=host;
  if(query==='revoke') await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  if(query==='forgery') {
   let rejected=false;
   try {await host.call('host.service.call',{service:NATIVE_TOOL_CONTINUATION_SERVICE,method:'call',params:{name:'grep',arguments:{pattern:'x'}}});} catch {rejected=true;}
   if(!rejected) throw new Error('rewritten native call accepted');
  }
  const result=await continueNativeTool(host);
  if(query==='throw') throw new Error('plugin failure after native');
  if(query==='duplicate') { try {await continueNativeTool(host);throw new Error('duplicate accepted');} catch(e) {if(String(e).includes('duplicate accepted')) throw e;} }
  const value=JSON.parse(result.content![0]!.text!);
  value.evidence.id='final-evidence';
  value.results=[{title:'modified',url:'https://example.test',snippet:'final'}];
  return {result:{content:[{type:'text',text:JSON.stringify(value)}]}};
 },
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});
