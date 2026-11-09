// Exercises the production Personal process and host model service with isolated data.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';

const root = resolve(import.meta.dirname, '..');
const evidence = resolve(process.env.WUU_PERSONAL_MODEL_E2E_OUTPUT || join(root, '.tmp/e2e/personal-model'));
await mkdir(evidence, {recursive:true});
const fixture = await mkdtemp(join(tmpdir(), 'wuu-personal-model-'));
await mkdir(join(fixture, 'home'));
const config = join(fixture, 'config.json');
await writeFile(config, JSON.stringify({composition:{version:4,profile:'desktop'},default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:'https://model.invalid/v1',model:'model-one'}}}));
const report = {status:'failed', cases:[], fixture};
let child;
try {
 async function start() {
  child = spawn(process.env.WUU_TEST_CORE || join(root, 'desktop/build/bin/wuu-core'), ['app-server','--desktop-owner-pipe','--user-space','local','--product-domain','personal','--composition-profile','personal','--workdir',fixture,'--config',config], {stdio:['pipe','pipe','pipe','pipe'],env:{...process.env,WUU_HOME:join(fixture,'home'),WUU_NODE_EXECUTABLE:process.execPath}});
  let stderr = ''; child.stderr.on('data', chunk => {stderr += chunk});
  const waiting = new Map(); let id = 0;
  child.on('exit', code => {for (const pending of waiting.values()) pending.reject(new Error(`host exited ${code}: ${stderr}`)); waiting.clear()});
  createInterface({input:child.stdout}).on('line', line => {let frame; try {frame=JSON.parse(line)} catch {return} const pending=waiting.get(frame.id); if (!pending) return; waiting.delete(frame.id); frame.error ? pending.reject(new Error(JSON.stringify(frame.error))) : pending.resolve(frame.result)});
  child.stdio[3].end(JSON.stringify({version:1,user_space_id:'local',product_domain:'personal'}));
  const rpc=(method, params={}) => new Promise((resolve,reject)=>{const current=++id;waiting.set(current,{resolve,reject}); child.stdin.write(JSON.stringify({id:current,method,params})+'\n')});
  const initialized=await rpc('initialize');
  const personal=initialized.extension_inventory.find(item=>item.provenance?.plugin_id==='personal');
  assert(personal?.enabled, 'Personal plugin unavailable');
  const call=async (method,input={}) => (await rpc('plugin/client/request',{id:personal.id,fingerprint:personal.fingerprint,graph_generation:personal.graph_generation,method,input,user_space_id:'local',product_domain:'personal'})).result;
  return {call};
 }
 let host=await start();
 assert.deepEqual(await host.call('model.get'), {provider:'fixture',model:'model-one'});
 assert.deepEqual(await host.call('model.update',{provider:'fixture',model:'model-two'}), {provider:'fixture',model:'model-two'});
 assert.deepEqual(await host.call('model.get'), {provider:'fixture',model:'model-two'});
 report.cases.push('owner changes and reads the plugin-owned main session model');
 await assert.rejects(host.call('model.update',{provider:'missing',model:'model-two'}));
 assert.deepEqual(await host.call('model.get'), {provider:'fixture',model:'model-two'});
 report.cases.push('failed selection preserves the previous model');
 const old=child; old.stdin.end(); await new Promise(resolve=>old.once('exit',resolve));
 host=await start();
 assert.deepEqual(await host.call('model.get'), {provider:'fixture',model:'model-two'});
 report.cases.push('selection survives host restart');
 report.status='passed';
} catch (error) {report.error=error.stack; console.error(error); process.exitCode=1}
finally {child?.kill(); await writeFile(join(evidence,'report.json'),JSON.stringify(report,null,2)); console.log(JSON.stringify(report))}
