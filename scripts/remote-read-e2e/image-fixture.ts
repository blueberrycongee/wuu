import {existsSync, readFileSync, writeFileSync} from 'node:fs';
import {continueNativeTool, getProcessEnvironment, requireProcessService, NATIVE_TOOL_CONTINUATION_SERVICE, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
const plugin: RuntimePlugin = {
 initialize(){return {protocol_version:3,lifecycle_version:2,required_services:[{name:NATIVE_TOOL_CONTINUATION_SERVICE,major_version:1,required:true},requireProcessService()],tools:[{id:'read',description:'Read through the authenticated worker.',replacement_contracts:existsSync(new URL('./disable-image',import.meta.url))?['read_file.text.v1','read_file.remote.v1']:['read_file.text.v1','read_file.image.v1','read_file.remote.v1'],input_schema:JSON.parse(readFileSync(new URL('./schema.json',import.meta.url),'utf8')),activity:{read_only:true,concurrency_safe:true,risk:'low'}}]};},
 async executeTool(input, host){
  const location=await getProcessEnvironment(host);
  if(location.backend!=='command' || !location.identity || location.root!==input.cwd) throw Error('incorrect remote identity');
  const name=(input.arguments as {path:string}).path;
  const original=await continueNativeTool(host);
  if(name==='failed-parent.png' || name==='success-parent.png') writeFileSync(new URL('./observed-image',import.meta.url),name);
  if(name==='pause.png') await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  if(name==='too-large.png') return {result:{content:[{type:'image',mime_type:'image/png',data:Buffer.alloc(2*1024*1024+1).toString('base64')}]}};
  if(name==='bad.png') return {result:{content:[{type:'image',mime_type:'image/png',data:Buffer.from('broken').toString('base64')}]}};
  const part={type:'image' as const,mime_type:'image/png',data:readFileSync(new URL('./alternate.png',import.meta.url)).toString('base64')};
  if(name==='forge.txt') return {result:{content:[part]}};
  if(!original.content || original.content.length!==1 || original.content[0].type!=='image') throw Error('native image unavailable');
  return {result:{content:[part]}};
 }
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(e=>{console.error(e);process.exitCode=1;});
