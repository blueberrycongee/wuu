import {readFileSync,existsSync,writeFileSync} from 'node:fs';
import {continueNativeTool, getProcessEnvironment, requireProcessService, NATIVE_TOOL_CONTINUATION_SERVICE, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
const plugin: RuntimePlugin = {
 initialize(){return {protocol_version:3,lifecycle_version:2,required_services:[{name:NATIVE_TOOL_CONTINUATION_SERVICE,major_version:1,required:true},requireProcessService()],tools:[{id:'read',description:'Transform an authenticated remote read.',replacement_contracts:existsSync(new URL('./disable-remote',import.meta.url))?['read_file.text.v1']:['read_file.text.v1','read_file.remote.v1'],input_schema:JSON.parse(readFileSync(new URL('./schema.json',import.meta.url),'utf8')),activity:{read_only:true,concurrency_safe:true,risk:'low'}}]};},
 async executeTool(input, host){
  const location=await getProcessEnvironment(host);
  if(location.backend!=='command' || !location.identity || location.root!==input.cwd) throw Error('incorrect remote identity or root');
  const name=(input.arguments as {path:string}).path;
  if(name==='pause.txt' || name==='envpause.txt') await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  if(name==='envpause.txt') {const after=await getProcessEnvironment(host); if(after.root!==location.root || after.identity!==location.identity) throw Error('scope drift');}
  if(name==='skip.txt') return {result:{structured_content:{text:'fabricated\n',total_lines:1}}};
  if(name==='forgery.txt') {
   let rejected=false;
   try {await host.call('host.service.call',{service:NATIVE_TOOL_CONTINUATION_SERVICE,method:'call',params:{name:'grep',arguments:{pattern:'secret'}}});} catch {rejected=true;}
   if(!rejected) throw Error('rewritten request accepted');
  }
  const remote=await continueNativeTool(host);
  if(name==='error-immediate.txt') { writeFileSync(new URL('./observed-error',import.meta.url),'done'); return {result:{is_error:true,content:[{type:'text',text:String((remote.structured_content as {text:string}).text)}]}}; }
  if(name==='fail-after.txt') throw Error('failure after native read');
  if(name==='bad-after.txt') return {result:{structured_content:{text:'broken',total_lines:-1}}};
  if(name==='after-pause.txt' || name==='cancel-after.txt' || name==='error-after-pause.txt') await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  if(name==='error-after-pause.txt') return {result:{is_error:true,content:[{type:'text',text:String((remote.structured_content as {text:string}).text)}]}};
  if(name==='duplicate.txt') {
   let rejected=false;
   try {await continueNativeTool(host);} catch {rejected=true;}
   if(!rejected) throw Error('duplicate accepted');
  }
  if(name==='shared.txt' && existsSync(new URL('./observe-child',import.meta.url))) writeFileSync(new URL('./observed-child',import.meta.url),'done');
  const body=remote.structured_content as {text:string,total_lines:number};
  if(!body || typeof body.text!=='string' || typeof body.total_lines!=='number') throw Error('remote worker did not return text');
  return {result:{structured_content:{text:`plugin:${body.text}`,total_lines:body.total_lines}}};
 }
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(e=>{console.error(e);process.exitCode=1;});
