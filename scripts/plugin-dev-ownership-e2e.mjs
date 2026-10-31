#!/usr/bin/env node
// Real CLI, Coding and Personal app-servers, selected settings provider, and
// generated Personal runtime. All state is synthetic; no GUI or model calls.
import assert from 'node:assert/strict';
import {execFile, spawn, spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {once} from 'node:events';
import {cp, mkdir, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath} from 'node:url';

const root = fileURLToPath(new URL('..',import.meta.url));
const output = resolve(process.env.WUU_OWNERSHIP_EVIDENCE || join(root,'.tmp/e2e/plugin-dev-ownership'));
await mkdir(output,{recursive:true});
const scratch = await mkdtemp(join(output,'run-'));
const binary = process.env.WUU_E2E_CORE ? resolve(process.env.WUU_E2E_CORE) : join(scratch,process.platform === 'win32' ? 'wuu.exe' : 'wuu');
const home = join(scratch,'home'), project = join(scratch,'coding');
await mkdir(home); await mkdir(project);
const env = {PATH:process.env.PATH, HOME:scratch, WUU_HOME:home, WUU_NODE_EXECUTABLE:process.execPath, ...(process.env.SystemRoot ? {SystemRoot:process.env.SystemRoot} : {})};
const config = join(home,'config.json');
await writeFile(config,JSON.stringify({default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:'http://127.0.0.1:1/v1',api_key:'synthetic',model:'fixture'}},ptc:{enabled:false},skills:{enabled:false}}));
const result = {status:'failed',binary,source_revision:spawnSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8'}).stdout.trim(),source_dirty:spawnSync('git',['diff','--quiet'],{cwd:root}).status !== 0,checks:[],publications:[],hosts:[]};
const hosts = [];
function start(name,args) {
 const child = spawn(binary,['app-server','--config',config,...args],{env,stdio:['pipe','pipe','pipe']});
 const host = {name,child,sequence:0,pending:new Map(),stderr:'',transcript:[]};
 child.stderr.on('data',data=>host.stderr+=data);
 createInterface({input:child.stdout}).on('line',line=>{
  const message = JSON.parse(line); host.transcript.push(message);
  if (message.id && host.pending.has(message.id)) {host.pending.get(message.id)(message);host.pending.delete(message.id);}
 });
 host.rpc = (method,params={}) => new Promise((resolve,reject)=>{
  const id = String(++host.sequence);
  const timer = setTimeout(()=>{host.pending.delete(id);reject(new Error(`${name} ${method} timed out: ${host.stderr}`));},30000);
  host.pending.set(id,response=>{clearTimeout(timer);response.error?reject(new Error(`${name} ${method}: ${JSON.stringify(response.error)}`)):resolve(response.result);});
  child.stdin.write(JSON.stringify({id,method,params})+'\n');
 });
 hosts.push(host); return host;
}
const initialize = host => host.rpc('initialize',{protocol_version:'wuu-app-server/v0.1',client:{name:'ownership-e2e'}});
const client = async (host, item, method, input = {}) => (await host.rpc('plugin/client/request', { id:item.id, fingerprint:item.fingerprint, graph_generation:item.graph_generation, method, input })).result;
const personalItem = init => init.extension_inventory.find(item=>item.provenance?.plugin_id==='personal' || item.id?.endsWith(':personal'));
const statePath = subject => join(home,'user-spaces/local/personal/state/plugin-storage/user',`sha256-${createHash('sha256').update(subject).digest('hex')}.json`);
const source = join(scratch,'personal-dev');
function cli(args) {
 return new Promise(resolve => {
  execFile(binary,args,{env,cwd:project,encoding:'utf8',timeout:60000,maxBuffer:8*1024*1024},(error,stdout,stderr)=>{
   const run = {status:error ? error.code ?? 1 : 0,stdout,stderr};
   result.publications.push({args,...run}); resolve(run);
  });
 });
}
function publish() {
 const run = cli(['plugin','dev','--watch=false',source]);
 return run;
}
try {
 if (!process.env.WUU_E2E_CORE) {
  const version = (await readFile(join(root,'VERSION'),'utf8')).trim();
  const build = spawnSync('go',['build','-ldflags',`-X github.com/blueberrycongee/wuu/internal/version.Version=${version}`,'-o',binary,'./cmd/wuu'],{cwd:root,encoding:'utf8',timeout:180000});
  assert.equal(build.status,0,build.stderr);
 }
 const coding = start('coding',['--workdir',project]);
 await initialize(coding);
 const personal = start('personal',['--workdir',join(home,'user-spaces/local/personal/files'),'--user-space','local','--product-domain','personal','--composition-profile','personal']);
 let item = personalItem(await initialize(personal));
 assert.equal(item.runtime_state,'active',JSON.stringify(item));
 const preferences = (await client(personal,item,'settings.get')).settings;
 const saved = (await client(personal,item,'settings.update',{expected_revision:preferences.revision,quiet_hours:{enabled:true,start:'23:17',end:'07:19',timezone:'UTC'},notifications:false,notification_batch_seconds:37})).settings;
 const original = JSON.parse(await readFile(statePath('plugin:bundled:personal'),'utf8'));
 assert.equal(original.transfer_receipts[0].target_subject_id,'plugin:bundled:personal');
 await cp(join(root,'internal/plugin/bundled/personal'),source,{recursive:true});
 await writeFile(join(source,'package.json'),JSON.stringify({scripts:{build:'node -e "process.exit(0)"'}}));
 let run = await publish();
 assert.equal(run.status,0,run.stderr);
 item = personalItem(await initialize(personal));
 assert.equal(item.id,'plugin:dev:personal'); assert.equal(item.runtime_state,'active',JSON.stringify(item));
 assert.deepEqual((await client(personal,item,'settings.get')).settings,saved);
 assert.deepEqual(JSON.parse(await readFile(statePath('plugin:bundled:personal'),'utf8')),original);
 const receipt = JSON.parse(await readFile(join(home,'dev/generations/personal/receipt.json'),'utf8'));
 assert.equal(receipt.storage_subject_id,'plugin:bundled:personal');
 result.checks.push('bundled-to-dev update preserves exact storage and ownership receipt through real Personal RPC');
 const reports = JSON.parse((await cli(['plugin','hosts','personal'])).stdout);
 assert(reports.some(report=>report.host.product_domain==='coding'));
 assert(reports.some(report=>report.host.product_domain==='personal' && report.plugins.some(plugin=>plugin.state==='active' && plugin.storage_subject_id==='plugin:bundled:personal')));
 result.hosts = reports;
 result.checks.push('publication and status report both live Coding and Personal hosts');
 const untouched = await cli(['plugin','reload','missing-plugin']);
 assert.notEqual(untouched.status,0,'reload of an absent plugin must not report success');
 assert.deepEqual(JSON.parse((await cli(['plugin','hosts','personal'])).stdout).map(report=>report.generation_id),reports.map(report=>report.generation_id),'unrelated hosts were reloaded');

 const fencePath = statePath('plugin:bundled:memory'), fence = await readFile(fencePath);
 const conflict = JSON.parse(fence); conflict.transfer_fences[0].target_subject_id='plugin:user:conflicting-owner';
 await writeFile(fencePath,JSON.stringify(conflict));
 const beforeReceipt = await readFile(join(home,'dev/generations/personal/receipt.json'));
 run = await publish();
 assert.notEqual(run.status,0); assert.match(run.stderr,/previous generation preserved/);
 assert.deepEqual(await readFile(join(home,'dev/generations/personal/receipt.json')),beforeReceipt);
 assert.deepEqual((await client(personal,item,'settings.get')).settings,saved);
 await writeFile(fencePath,fence);
 result.checks.push('durable ownership conflict rejects publication and keeps the previous Personal runtime usable');

 const runtimePath = join(source,'runtime.mjs'), runtime = await readFile(runtimePath,'utf8');
 assert(runtime.includes('async activateScoped(host, lifecycle) {'));
 await writeFile(runtimePath,runtime.replace('async activateScoped(host, lifecycle) {','async activateScoped(host, lifecycle) { throw new Error("synthetic activation failure");'));
 run = await publish();
 assert.notEqual(run.status,0); assert.match(run.stderr,/was published, but host activation is incomplete/);
 const failed = JSON.parse((await cli(['plugin','hosts','personal'])).stdout);
 assert(failed.some(report=>report.host.product_domain==='personal' && report.plugins.some(plugin=>plugin.state==='failed')));
 result.checks.push('post-commit activation failure is reported by the actual Personal host');
 await writeFile(runtimePath,runtime);
 run = await publish(); assert.equal(run.status,0,run.stderr);
 run = await cli(['plugin','reload','personal']); assert.equal(run.status,0,run.stderr);
 item = personalItem(await initialize(personal)); assert.equal(item.runtime_state,'active');
 assert.deepEqual((await client(personal,item,'settings.get')).settings,saved);
 result.checks.push('repair publication and explicit reload restore Personal without deleting or replaying its data');
 result.status = 'passed';
} catch(error) {
 result.error = error.stack; process.exitCode = 1;
} finally {
 for (const host of hosts) {
  await writeFile(join(scratch,`${host.name}.stderr.log`),host.stderr);
  await writeFile(join(scratch,`${host.name}.jsonl`),host.transcript.map(row=>JSON.stringify(row)).join('\n'));
  host.child.stdin.end();
  const timer = setTimeout(()=>host.child.kill('SIGKILL'),5000);
  if (host.child.exitCode === null) await once(host.child,'exit');
  clearTimeout(timer);
 }
 await writeFile(join(scratch,'result.json'),JSON.stringify(result,null,2));
 console.log(JSON.stringify({status:result.status,checks:result.checks,error:result.error,evidence:join(scratch,'result.json')},null,2));
}
