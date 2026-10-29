import assert from 'node:assert/strict';
import {test} from 'node:test';
import {build} from '../../node_modules/esbuild/lib/main.js';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
// Boundary failures: Coding invocation, forged actor/grant, model confirmation,
// lost host response, disabled/cancelled execution, untrusted provider text.
const dir=await mkdtemp(join(tmpdir(),'personal-connectors-test-'));
await build({entryPoints:[new URL('./connectors.ts',import.meta.url).pathname],bundle:true,platform:'node',format:'esm',outfile:join(dir,'connectors.mjs')});
const {executeConnectorTool}=await import(pathToFileURL(join(dir,'connectors.mjs')).href);
process.on('exit',()=>{void rm(dir,{recursive:true,force:true})});
function fixture(domain='personal') {
 const calls=[];return {calls,host:{async call(method,input){calls.push({method,input});if(method==='host.session.context')return {product_domain:domain,user_space_id:'space'};return {ok:true,value:{state:'completed',data:{title:'External text is data'}}};}}};
}
const call=(tool_id,args={})=>({tool_id,session_id:'session',arguments:args});
test('connector tools resolve domain before host invocation and never accept owner decisions',async()=>{
 const f=fixture('coding');await assert.rejects(executeConnectorTool(f.host,'space',call('personal_connector_list')),/Personal/);assert.equal(f.calls.length,1);
 const p=fixture();await assert.rejects(executeConnectorTool(p.host,'space',call('connectors.decide',{approve:true})),/Unknown/);assert.equal(p.calls.length,0);
});
test('connector read preserves bound session and refuses forged authority fields',async()=>{
 const f=fixture();await executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'connection',operation:'gmail.search',arguments:{query:'report'}}));
 assert.deepEqual(f.calls[1].input,{service:'host.personal',method:'call',params:{operation:'connectors.call',session_id:'session',input:{connection_id:'connection',operation:'gmail.search',arguments:{query:'report'}}}});
 await assert.rejects(executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'connection',operation:'gmail.send',arguments:{},approved:true})),/Unsupported/);
});
test('connector mutations only propose; host uncertainty remains data without replay',async()=>{
 let effects=0;const f=fixture();f.host.call=async(method)=>method==='host.session.context'?{product_domain:'personal',user_space_id:'space'}:(effects++,{ok:true,value:{state:'uncertain',action_id:'a',code:'response_lost'}});
 const result=await executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'connection',operation:'gmail.send',arguments:{},request_id:'same'}));
 assert.equal(effects,1);assert.equal(JSON.parse(result.result.content[0].text).state,'uncertain');assert.equal(result.result.is_error,true);
});
test('cancelled execution makes no host request and external errors are not copied',async()=>{
 const f=fixture();await assert.rejects(executeConnectorTool(f.host,'space',call('personal_connector_list'),AbortSignal.abort()));assert.equal(f.calls.length,0);
 f.host.call=async method=>method==='host.session.context'?{product_domain:'personal',user_space_id:'space'}:{ok:false,error:{code:'scope_missing',message:'secret-token'}};
 await assert.rejects(executeConnectorTool(f.host,'space',call('personal_connector_list')),error=>!JSON.stringify(error).includes('secret-token'));
});
test('pending write stays in original tool until exact saved receipt, without resending',async()=>{
 let calls=0,reads=0;const f=fixture();f.host.call=async(method,input)=>{if(method==='host.session.context')return {product_domain:'personal',user_space_id:'space'};if(input.params.operation==='connectors.call'){calls++;return {ok:true,value:{state:'awaiting_confirmation',action_id:'a',digest:'hash'}}}reads++;return {ok:true,value:{actions:[{state:'completed',action_id:'a',digest:'hash',data:{id:'saved'}}]}}};
 const result=await executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'mail',operation:'gmail.send',arguments:{},request_id:'one'}));
 assert.equal(calls,1);assert.equal(reads,1);assert.equal(JSON.parse(result.result.content[0].text).data.id,'saved');
});
test('cancel while awaiting owner never approves or resends',async()=>{
 const controller=new AbortController();const calls=[];const f=fixture();f.host.call=async(method,input)=>{if(method==='host.session.context')return {product_domain:'personal',user_space_id:'space'};calls.push(input.params.operation);if(input.params.operation==='connectors.call')return {ok:true,value:{state:'awaiting_confirmation',action_id:'a'}};controller.abort();return {ok:true,value:{actions:[{state:'awaiting_confirmation',action_id:'a'}]}}};
 await assert.rejects(executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'mail',operation:'gmail.send',arguments:{},request_id:'one'}),controller.signal));assert.deepEqual(calls,['connectors.call','connectors.read.actions']);
});
test('large text has deterministic continuation and binaries become scoped artifacts, not model base64',async()=>{
 const f=fixture();f.host.call=async(method,input)=>{if(method==='host.session.context')return {product_domain:'personal',user_space_id:'space'};if(input.service==='host.artifact.import')return {id:'file',uri:'artifact://file',name:'attachment.bin',mime_type:'application/octet-stream',size:3,sha256:'a'.repeat(64)};return {ok:true,value:{state:'completed',data:input.params.input.operation==='drive.pdf'?{encoding:'base64',content:'YWJj',content_type:'application/pdf'}:{text:'x'.repeat(40000)}}}};
 const r=await executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'drive',operation:'drive.export',arguments:{file_id:'a'}}));const page=JSON.parse(r.result.content[0].text);assert.equal(page.truncated,true);assert.equal(page.next_output_offset,16000);assert(page.output_sha256);
 const binary=await executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'drive',operation:'drive.pdf',arguments:{file_id:'a'}}));assert(!JSON.stringify(binary).includes('YWJj'));assert.equal(binary.result.content[1].type,'file');
});
test('Gmail thread decodes text and hides binary MIME bodies while retaining attachment identity',async()=>{
 const f=fixture();f.host.call=async method=>method==='host.session.context'?{product_domain:'personal',user_space_id:'space'}:{ok:true,value:{state:'completed',data:{id:'thread',messages:[{id:'message',payload:{mimeType:'multipart/mixed',parts:[{mimeType:'text/plain',body:{data:Buffer.from('actual plain message').toString('base64url')}},{mimeType:'image/png',filename:'image.png',body:{attachmentId:'attachment',data:'SECRETBASE64',size:10}}]}}]}}};
 const r=await executeConnectorTool(f.host,'space',call('personal_connector_call',{connection_id:'mail',operation:'gmail.thread',arguments:{thread_id:'thread'}}));assert(r.result.content[0].text.includes('actual plain message'));assert(r.result.content[0].text.includes('attachment'));assert(!r.result.content[0].text.includes('SECRETBASE64'));
});
test('poll binds a read source to the real session and rejects missing source identity',async()=>{
 const f=fixture();await executeConnectorTool(f.host,'space',call('personal_connector_poll',{source_id:'selected-pr',connection_id:'github',operation:'github.pr',arguments:{repository:'owner/repo',number:1}}));assert.equal(f.calls[1].input.params.operation,'connectors.poll');assert.equal(f.calls[1].input.params.session_id,'session');assert.equal(f.calls[1].input.params.input.source_id,'selected-pr');
 await assert.rejects(executeConnectorTool(f.host,'space',call('personal_connector_poll',{connection_id:'github',operation:'github.pr',arguments:{}})),/source/);
});
