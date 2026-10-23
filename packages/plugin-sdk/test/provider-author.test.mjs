import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { once } from 'node:events';
import { randomUUID } from 'node:crypto';
import { mkdtemp, writeFile, copyFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { test } from 'node:test';
const exec = promisify(execFile);

// Public-package and transport regression: base64/null, native field casing,
// post-commit diagnostics, and uncertain writes must survive without replay.
test('packed provider clients preserve wire values and callback scope', {timeout:60000}, async () => {
  const dir = await mkdtemp(join(tmpdir(),'wuu-provider-author-'));
  let child;
  let lines;
  let watchdog;
  try {
    const sdk = fileURLToPath(new URL('../',import.meta.url));
    const packed = JSON.parse((await exec('npm',['pack',sdk,'--ignore-scripts','--json','--pack-destination',dir],{cwd:dir})).stdout)[0].filename;
    await writeFile(join(dir,'package.json'),JSON.stringify({private:true,type:'module',dependencies:{'@wuu/plugin-sdk':`file:./${packed}`}}));
    await exec('npm',['install','--ignore-scripts','--no-audit','--no-fund'],{cwd:dir});
    await copyFile(new URL('./fixtures/provider-author.ts',import.meta.url),join(dir,'author.ts'));
    await exec(process.execPath,[join(dir,'node_modules/typescript/bin/tsc'),'author.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2022','--strict','--skipLibCheck'],{cwd:dir}).catch(error => { throw new Error(error.stdout || error.message); });
    child = spawn(process.execPath,['--input-type=module','-e',"import plugin from './author.js'; import {runJSONLRuntime} from '@wuu/plugin-sdk'; await runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout});"],{cwd:dir,stdio:['pipe','pipe','pipe']});
    const exited = once(child,'exit');
    let stderr = '';
    child.stderr.on('data',data=>{stderr+=data;});
    lines = createInterface({input:child.stdout});
    const responses = lines[Symbol.asyncIterator]();
    const send = value => child.stdin.write(JSON.stringify(value)+'\n');
    const next = async () => { const line=await responses.next(); assert.equal(line.done,false,stderr); return JSON.parse(line.value); };
    watchdog = setTimeout(()=>child.kill(),15000);
    send({id:'init',method:'initialize',params:{supported_host_services:['host.service.call']}});
    assert.ok((await next()).result);
    send({id:'active',method:'activate'}); await next();
    const launchPrompt = randomUUID();
    const launchIntent = randomUUID();
    send({id:'run',method:'service.invoke',params:{service:'author.check',method:'call',execution_id:'execution-one',params:{launch_prompt:launchPrompt,launch_intent:launchIntent}}});
    const seen=[];
    let credential=null;
    let configReads=0;
    let result;
    for (;;) {
      const message=await next();
      if (message.id==='run') { assert.equal(message.error,undefined); result=message.result; break; }
      assert.equal(message.method,'host.service.call');
      const {service,method,params,execution_id}=message.params;
      assert.equal(execution_id,'execution-one');
      seen.push(`${service}/${method}`);
      let reply;
      if (service==='state.credentials') {
        if (method==='get') reply={value:credential};
        else if (method==='compare-exchange') { assert.equal(params.expected,null); assert.equal(params.value,''); credential=params.value; reply={exchanged:true}; }
        else if (method==='set') { send({id:message.id,error:{code:'credential_unavailable',message:'write outcome unknown'}}); continue; }
        else assert.fail(`unexpected credential operation ${method}`);
      } else if (service==='state.config') {
        reply={source:method==='read' && configReads++ > 0 ? '' : null,revision:'config-1',config:{}};
        if (method==='compare-exchange') { assert.deepEqual(params,{expected_revision:'config-1',source:null}); reply={...reply,committed:true,activation_error:'project_overlay_invalid'}; }
      } else if (service==='state.plugin-settings') reply={revision:'settings-1',document:{schema_version:1,plugin_id:'author',values:{}}};
      else if (service==='state.artifacts') { assert.equal(params.max_bytes,8); reply={data:'AAEC',mime_type:'application/octet-stream'}; }
      else if (service==='host.data.query') { assert.equal(method,'call'); reply={version:'1.0.0',thread_id:'worker',events:[]}; }
      else if (service==='security.authorize') { assert.equal(method,'authorize'); reply={outcome:'allow'}; }
      else if (service==='host.managed-session' && method==='create') {
        assert.deepEqual(params.initial_input,{ClientID:'initial-1',SessionID:'worker',Content:'inspect',Wake:true});
        // An authored launch input must survive independently from the brief.
        assert.equal(params.launch.prompt,launchPrompt);
        assert.equal(params.launch.intent,launchIntent);
        reply={session_id:'worker',created:false};
      } else if (service==='host.managed-session' && method==='submit') {
        assert.deepEqual(params.message,{Role:'user',Content:'inspect',ClientID:'input-2'});
        reply={admitted:false,capacity_full:true};
      } else if (service==='project.coordination' && method==='admission') reply={max_parallel:3};
      else assert.fail(`unexpected service ${service}/${method}`);
      send({id:message.id,result:reply});
    }
    assert.deepEqual(result.absent,{value:null});
    assert.deepEqual(result.empty,{value:''});
    assert.equal(result.swapped.exchanged,true);
    assert.equal(result.errorCode,'credential_unavailable');
    assert.equal(seen.filter(value=>value==='state.credentials/set').length,1);
    assert.equal(result.decodedSource,null);
    assert.equal(result.emptySource,'');
    assert.equal(result.changed.source,null);
    assert.equal(result.changed.committed,true);
    assert.equal(result.changed.activation_error,'project_overlay_invalid');
    assert.equal(result.artifact.data,'AAEC');
    assert.deepEqual(result.admitted,{admitted:false,capacity_full:true});
    assert.equal(result.admission.max_parallel,3);
    send({id:'stop',method:'shutdown'}); await next(); child.stdin.end();
    const [code]=await exited; assert.equal(code,0,stderr);
  } finally { clearTimeout(watchdog); child?.kill(); lines?.close(); await rm(dir,{recursive:true,force:true}); }
});
