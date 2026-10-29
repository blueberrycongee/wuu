import {appendFileSync,readFileSync} from 'node:fs';
import {continueNativeTool, getProcessEnvironment, requireProcessService, NATIVE_TOOL_CONTINUATION_SERVICE, runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
const plugin: RuntimePlugin = {
 initialize(){return {protocol_version:3,lifecycle_version:2,required_services:[{name:NATIVE_TOOL_CONTINUATION_SERVICE,major_version:1,required:true},requireProcessService()],tools:[{id:'read',description:'Paginate authenticated remote text and read images.',replacement_contracts:['read_file.text.v1','read_file.image.v1','read_file.pagination.v1','read_file.remote.v1'],input_schema:JSON.parse(readFileSync(new URL('./schema.json',import.meta.url),'utf8')),activity:{read_only:true,concurrency_safe:true,risk:'low'}}]};},
 async executeTool(input,host){
  const location=await getProcessEnvironment(host);
  if(location.backend!=='command' || !location.identity || location.root!==input.cwd) throw Error('incorrect remote identity');
  const args=input.arguments as {path:string;continuation?:string};
  if(args.continuation && args.continuation!=='provider-private') throw Error('invalid provider cursor');
  const native=await continueNativeTool(host);
  if(args.path==='pause-page.txt' && args.continuation) await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  if(args.path==='conflict.txt' || args.path==='alias-other.txt' || args.path==='extension-distinct.txt') appendFileSync(new URL('./native-line-witness.txt',import.meta.url), `${args.path}\n`);
  if(args.path.startsWith('ptc-') || args.path==='image.png') appendFileSync(new URL('./completed.txt',import.meta.url), `${args.path}:${args.continuation?'next':'first'}\n`);
  const image=native.content?.[0];
  if(image?.type==='image') return {result:{content:[image]}};
  const body=native.structured_content as {text:string,total_lines:number};
  if(typeof body?.text!=='string' || typeof body.total_lines!=='number') throw Error('missing native text');
  const lines=body.text.match(/[^\n]*\n|[^\n]+$/g)??[];
  const selected=lines.slice(0,2);
  if(args.path==='lie.txt') return {result:{structured_content:{text:selected.join(''),total_lines:body.total_lines+1,next_cursor:'provider-private'}}};
  return {result:{structured_content:{text:selected.join(''),total_lines:body.total_lines,...(selected.length<lines.length?{next_cursor:'provider-private'}:{})}}};
 }
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(e=>{console.error(e);process.exitCode=1;});
