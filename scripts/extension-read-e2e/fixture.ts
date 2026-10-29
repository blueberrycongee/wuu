import {readFileSync} from 'node:fs';
import {runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
const plugin:RuntimePlugin={
 initialize(){return {protocol_version:3,lifecycle_version:2,tools:[{id:'read',replacement_contracts:['read_file.text.v1','read_file.image.v1'],description:'Read synthetic external text',input_schema:JSON.parse(readFileSync(new URL('./schema.json',import.meta.url),'utf8')),activity:{read_only:true,concurrency_safe:true,risk:'low'}},{id:'produce',direct_only:true,description:'Produce large synthetic output',input_schema:{type:'object',properties:{}},activity:{read_only:true,concurrency_safe:true,risk:'low'}}]};},
 async executeTool(input){
  if(input.tool_id==='produce') return {result:{content:[{type:'text',text:'archived🙂\n'.repeat(5000)}]}};
  const args=input.arguments as {path?:string}; if(input.tool_id!=='read'||!args.path) throw Error('expected path');
  if(args.path==='static.gif' || args.path==='animated.gif') return {result:{content:[{type:'image',mime_type:'image/gif',data:readFileSync(new URL(args.path==='static.gif' ? './static.gif' : './animated.gif',import.meta.url)).toString('base64')}]}};
  if(args.path==='plain-error.png') return {result:{is_error:true,content:[{type:'text',text:'reader denied image'}]}};
  if(args.path==='evil-image.png') return {result:{is_error:true,content:[{type:'image',mime_type:'image/png',data:readFileSync(new URL('./external.png',import.meta.url)).toString('base64')}]}};
  if(args.path==='legacy.txt' && readFileSync(new URL('./legacy-workspace/legacy.txt',import.meta.url)).subarray(0,8).equals(Buffer.from([137,80,78,71,13,10,26,10]))) return {result:{content:[{type:'image',mime_type:'image/png',data:readFileSync(new URL('./external.png',import.meta.url)).toString('base64')}]}};
  if(args.path==='plugin-image.png') return {result:{content:[{type:'image',mime_type:'image/png',data:readFileSync(new URL('./external.png',import.meta.url)).toString('base64')}]}};
  if(args.path==='pause.txt') await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  if(args.path==='fail.txt') throw Error('external reader failed');
  if(args.path==='bad.txt') return {result:{structured_content:{text:'broken',total_lines:-1}}};
  return {result:{structured_content:{text:'external:first\nexternal:second\n',total_lines:2}}};
 },
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(e=>{console.error(e);process.exitCode=1;});
