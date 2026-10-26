#!/usr/bin/env node
// Selected external model provider, real CLI, public SDK and durable receipts.
// Synthetic HTTP inference only. Does not consume any account credentials.
import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp,mkdir,mkdtemp,readFile,writeFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('..',import.meta.url));
const output=resolve(process.env.WUU_MODEL_PROVIDER_E2E_OUTPUT||join(root,'.tmp/e2e/model-provider'));
await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const home=join(run,'home'),workspace=join(run,'workspace'),source=join(run,'http-model.ts');await mkdir(home);await mkdir(workspace);
const binary=resolve(process.env.WUU_E2E_CORE||join(root,'bin/wuu'));
const env={PATH:process.env.PATH,HOME:run,WUU_HOME:home,WUU_NODE_EXECUTABLE:process.execPath};
const requests=[];
const server=createServer(async(req,res)=>{let body='';for await(const chunk of req)body+=chunk;requests.push(JSON.parse(body));res.setHeader('content-type','application/json');res.end(JSON.stringify({choices:[{message:{role:'assistant',content:'external-selected-model-ok'},finish_reason:'stop'}],usage:{prompt_tokens:7,completion_tokens:4}}));});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const result={status:'failed',run,checks:[],limitations:['Synthetic model, real CLI and external plugin process. No paid inference.']};
async function command(args){return await new Promise((resolve,reject)=>{const child=spawn(binary,args,{cwd:workspace,env});child.stdin.end();let stdout='',stderr='';child.stdout.on('data',v=>stdout+=v);child.stderr.on('data',v=>stderr+=v);const timer=setTimeout(()=>child.kill('SIGKILL'),30000);child.on('error',reject);child.on('close',code=>{clearTimeout(timer);resolve({code,stdout,stderr});});});}
try{
 await cp(join(root,'examples/plugins/http-model-provider.ts'),source);
 const module=`local-${createHash('sha256').update(source).digest('hex').slice(0,24)}`;
 const publish=await command(['plugin','dev','--watch=false',source]);assert.equal(publish.code,0,publish.stderr);
 const config={default_provider:'external',providers:{external:{type:'openai-compatible',base_url:'http://127.0.0.1:1/should-not-run',model:'fixture',models:{fixture:{options:{endpoint:`http://127.0.0.1:${server.address().port}`}}}}},composition:{version:3,profile:"headless",patches:[{modules:{'wuu.model.native':false,'wuu.credentials.os':false,'wuu.credentials.file':true,[module]:true}}],services:[{service:"state.credentials",major:1,provider:"wuu.credentials.file"},{service:'model.adapter',major:1,provider:module}]},skills:{enabled:false}};
 const configPath=join(home,'config.json');await writeFile(join(home,'.wuu-session-fixture'),'wuu-session-fixture-v1\n');await writeFile(configPath,JSON.stringify(config));
 const first=await command(['exec','--config',configPath,'--json','Say the provider marker.']);result.first=first;assert.equal(first.code,0,first.stderr);assert.match(first.stdout,/external-selected-model-ok/);assert(requests.length>0);result.checks.push('real CLI selects an external-only adapter without a native transport or native credential');
 const fixture=join(run,'session-fixture'); const built=spawnSync('go',['build','-o',fixture,'./scripts/session-fixture'],{cwd:root,encoding:'utf8'});assert.equal(built.status,0,built.stderr);
 const inspected=spawnSync(fixture,[],{input:JSON.stringify({home,workdir:workspace,op:'inspect',domains:['inference_submissions']}),encoding:'utf8'});assert.equal(inspected.status,0,inspected.stderr);
 const submissions=JSON.parse(inspected.stdout).documents.inference_submissions.map(item=>item.value);await writeFile(join(run,'submissions.json'),JSON.stringify(submissions,null,2));
 assert.equal(submissions.length,requests.length);assert(submissions.length>0);for(const submission of submissions){assert.equal(submission.Outcome,'succeeded');assert.equal(submission.ReportedUsage.InputTokens,7);assert.equal(submission.ReportedUsage.OutputTokens,4);}result.checks.push('each physical external HTTP send has exactly one durable successful receipt with authoritative usage');
 config.composition.patches[0].modules[module]=false;await writeFile(configPath,JSON.stringify(config));const count=requests.length;
 const disabled=await command(['exec','--config',configPath,'--json','This must not run.']);result.disabled=disabled;assert.notEqual(disabled.code,0);assert.equal(requests.length,count);result.checks.push('disabling selected provider stops inference without a native fallback');
 result.core_sha256=createHash('sha256').update(await readFile(binary)).digest('hex');result.status='passed';
}catch(error){result.error=String(error.stack||error);throw error}finally{server.closeAllConnections();await new Promise(resolve=>server.close(resolve));await writeFile(join(run,'result.json'),JSON.stringify(result,null,2));await writeFile(join(run,'requests.json'),JSON.stringify(requests,null,2));console.log(`Model provider evidence: ${run}`)}
