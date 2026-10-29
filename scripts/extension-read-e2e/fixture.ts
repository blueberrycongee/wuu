import {readFileSync} from 'node:fs';
import {runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
const plugin:RuntimePlugin={
 initialize(){return {protocol_version:3,lifecycle_version:2,tools:[{id:'read',replacement_contracts:['read_file.text.v1'],description:'Read synthetic external text',input_schema:JSON.parse(readFileSync(new URL('./schema.json',import.meta.url),'utf8')),activity:{read_only:true,concurrency_safe:true,risk:'low'}},{id:'produce',direct_only:true,description:'Produce large synthetic output',input_schema:{type:'object',properties:{}},activity:{read_only:true,concurrency_safe:true,risk:'low'}}]};},
 async executeTool(input){
  if(input.tool_id==='produce') return {result:{content:[{type:'text',text:'archived🙂\n'.repeat(5000)}]}};
  const args=input.arguments as {path?:string}; if(input.tool_id!=='read'||!args.path) throw Error('expected path');
  if(args.path==='pause.txt') await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  if(args.path==='fail.txt') throw Error('external reader failed');
  if(args.path==='bad.txt') return {result:{structured_content:{text:'broken',total_lines:-1}}};
  return {result:{structured_content:{text:'external:first\nexternal:second\n',total_lines:2}}};
 },
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(e=>{console.error(e);process.exitCode=1;});
