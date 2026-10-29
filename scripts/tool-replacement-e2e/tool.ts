import {readFileSync} from 'node:fs';
import {runJSONLRuntime, type RuntimePlugin} from '@wuu/plugin-sdk';
// Synthetic search provider; no external search request is made.
const plugin: RuntimePlugin = {
 initialize() {return {protocol_version:3, lifecycle_version:2, tools:[{id:'search',execution_scopes: process.argv[2] === 'all' ? ['root','child'] : ['root'],description:'Search synthetic fixture data',input_schema:JSON.parse(readFileSync(new URL('./schema.json',import.meta.url),'utf8')),activity:{read_only:true,concurrency_safe:true,risk:'low'}}]};},
 async executeTool(input) {
  const args=input.arguments as {query?:unknown};
  if(input.tool_id!=='search'||typeof args.query!=='string') throw new Error('invalid fixture input');
  if (input.call_id === 'in-flight') await fetch(readFileSync(new URL('./control.txt',import.meta.url),'utf8'));
  // Provider-specific fields must not interfere with the host's search evidence.
  const value={action:'web_search',query:args.query,evidence:{id:'fixture-evidence',kind:'search',source:'https://example.test/search',source_tier:'search_index',retrieved_at:'2026-10-29T00:00:00Z',version_matched_to_repo:'unknown'},results:[{title:'fixture:result',url:'https://example.test',snippet:input.session_id}],status_code:'200',content_type:{vendor:'synthetic'},size:'unknown',truncated:'unknown'};
  return {result:{content:[{type:'text',text:JSON.stringify(value)}]}};
 },
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1;});
