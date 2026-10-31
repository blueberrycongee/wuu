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
    const initialized = (await next()).result;
    assert.ok(initialized);
    assert.ok(initialized.required_services.some(service => service.name === 'host.data.query' && service.major_version === 2));
    send({id:'active',method:'activate'}); await next();
    const launchPrompt = randomUUID();
    const launchIntent = randomUUID();
    const indexThread = randomUUID(), indexTurn = randomUUID(), indexPayload = randomUUID();
    const artifactThread = randomUUID();
    let artifactDeleted = false, artifactDeleteCount = 0;
    let indexBatch;
    let appendCount = 0, queryCount = 0, latestContextCount = 0;
    const stewardship={version:1,authority:'configured-store',provider:'selected-docs',operation:'verify',input_digest:'bound-digest',input_json:'{}',consumer:'verification'};
    const admission={version:1,principal:{kind:'user',issuer:'selected',tenant:'tenant',subject:'owner'},action:'test/execute',input_digest:'host-bound-digest',input_json:'{"same":"admitted bytes"}',authority:'local-installation'};
    send({id:'run',method:'service.invoke',params:{admission,storage_stewardship:stewardship,service:'author.check',method:'call',execution_id:'execution-one',params:{launch_prompt:launchPrompt,launch_intent:launchIntent,index_thread:indexThread,index_turn:indexTurn,index_payload:indexPayload,artifact_thread:artifactThread}}});
    const seen=[];
    let credential=null;
    let configReads=0, transferCount=0, transferCheckCount=0, grantIssues=0;
    let issuedGrant;
    let result;
    for (;;) {
      const message=await next();
      if (message.id==='run') { assert.equal(message.error,undefined); result=message.result; break; }
      assert.equal(message.method,'host.service.call');
      const {service,method,params,execution_id}=message.params;
      assert.equal(execution_id,['release-inspection','release-authorization'].includes(method) ? undefined : 'execution-one');
      if(['release-inspection','release-authorization'].includes(method)) assert.equal(message.params.cleanup,true);
      seen.push(`${service}/${method}`);
      let reply;
      if (service==='sandbox.process') {
        assert.equal(method,'confine');
        assert.deepEqual(params,{argv:['/bin/sh','-c','true'],policy:{mode:'workspace-write',writable_roots:['/workspace'],protected_paths:['/workspace/private']}});
        reply={argv:['/sandbox','--','/bin/sh','-c','true'],enforcement:'full',protected_paths_enforced:true};
      } else if (service==='execution.authorization') {
        if(method==='describe') reply={default_lifetime_seconds:3600,max_lifetime_seconds:7200,authority_kinds:['replacement-authority'],scope_kinds:['exact-run']};
        else if (method==='issue') {
          grantIssues++;
          assert.equal(params.scope.consumer,'author');
          assert.equal(params.scope.revision,1);
          assert.equal(params.owner,undefined,'caller JSON must not invent host owner admission');
          if (grantIssues===2) { send({id:message.id,error:{code:'service_unavailable',message:'grant outcome unknown'}}); continue; }
          assert.equal(grantIssues,1,'uncertain grants must not be retried by the helper');
          reply=issuedGrant={reference:{provider:'replacement-authority',id:'opaque-reference'},principal:{kind:'user',issuer:'selected',tenant:'tenant',subject:'owner'},scope:params.scope,capabilities:params.capabilities,expires_at:params.expires_at};
        } else {
          assert.deepEqual(params.reference,issuedGrant.reference);
          assert.deepEqual(params.scope,issuedGrant.scope);
          if (method==='validate') { assert.equal(params.action,'user/session/send'); assert.deepEqual(params.resources,[{kind:'session',id:'worker'}]); reply={valid:false,reason:'revoked externally'}; }
          else { assert.equal(method,'revoke'); reply={revoked:true}; }
        }
      } else if (service==='state.credentials') {
        if (method==='get') reply={value:credential};
        else if (method==='compare-exchange') { assert.equal(params.expected,null); assert.equal(params.value,''); credential=params.value; reply={exchanged:true}; }
        else if (method==='set') { send({id:message.id,error:{code:'credential_unavailable',message:'write outcome unknown'}}); continue; }
        else assert.fail(`unexpected credential operation ${method}`);
      } else if (service==='state.config') {
        reply={source:method==='read' && configReads++ > 0 ? '' : null,revision:'config-1',config:{}};
        if (method==='compare-exchange') { assert.deepEqual(params,{expected_revision:'config-1',source:null}); reply={...reply,committed:true,activation_error:'project_overlay_invalid'}; }
      } else if (service==='state.documents') {
        assert.deepEqual(params,{id:'guard-one',namespaces:['records']});
        if(method==='acquire-inspection') reply={id:params.id,namespaces:params.namespaces,revision:'9007199254740993'};
        else { assert.equal(method,'release-inspection'); reply={}; }
      } else if (service==='state.plugin-settings') {
        if (method === 'acquire-inspection' || method === 'release-inspection') {
          assert.deepEqual(params,{id:'guard-two',subject:'author',scope:'user'});
          reply=method==='acquire-inspection'?{id:params.id,document:{schema_version:1,plugin_id:'author',values:{'record':'opaque'}},revision:'revision'}:{};
        } else if (method === 'check-transfer-state') {
          transferCheckCount++;
          assert.equal(transferCount, 0, 'preparation must precede ownership mutation');
          assert.deepEqual(params, {id:'move-1',source_subject_id:'old-owner',target_subject_id:'author',key_prefix:'records.',scope:'user'});
          reply={};
        } else if (method === 'transfer-state') {
          transferCount++;
          assert.deepEqual(params, { id: ['move-1', 'unsupported', 'uncertain'][transferCount - 1], source_subject_id: 'old-owner', target_subject_id: 'author', key_prefix: 'records.', scope: 'user' });
          if (transferCount > 1) {
            send({id:message.id,error:{code:transferCount === 2 ? 'method_not_found' : 'service_unavailable',message:'selected transfer unavailable'}}); continue;
          }
          reply={};
        } else {
          assert.equal(method, 'read', 'a transfer failure must not emulate local read/update');
          reply={revision:'settings-1',document:{schema_version:1,plugin_id:'author',values:{}}};
        }
      }
      else if (service==='state.artifacts') {
        if (method==='delete-thread') {
          artifactDeleteCount++;
          if (artifactDeleteCount <= 2) {
            assert.deepEqual(params,{thread_id:artifactThread});
            artifactDeleted=true;
            reply={thread_id:artifactThread,deleted:true};
          } else {
            assert.equal(artifactDeleteCount,3,'artifact helper retried an uncertain deletion');
            assert.deepEqual(params,{thread_id:'uncertain-thread'});
            send({id:message.id,error:{code:'artifact_unavailable',message:'cleanup outcome unknown'}}); continue;
          }
        } else {
          assert.equal(method,'read');
          assert.deepEqual(params,{thread_id:artifactThread,uri:'artifact:one',max_bytes:8});
          if (artifactDeleted) {
            send({id:message.id,error:{code:'artifact_not_found',message:'selected provider no longer has payload'}}); continue;
          }
          reply={data:'AAEC',mime_type:'application/octet-stream'};
        }
      }
      else if (service==='host.data.query') {
        if (method==='append-turn') {
          appendCount++;
          assert.equal(params.thread_id,indexThread);
          assert.equal(params.events.length,3);
          assert.ok(params.events.every(event => event.thread_id === params.thread_id && event.turn_id === params.turn_id));
          if (appendCount === 1) {
            assert.equal(params.turn_id,indexTurn);
            assert.deepEqual(params.events.map(event => event.type),['turn','future_fact','final']);
            assert.ok(params.events.every(event => event.data.text === indexPayload && event.data.nested.zero === 0 && event.data.nested.absent === null));
            indexBatch=params;
          } else if (appendCount === 2) assert.deepEqual(params,indexBatch);
          else if (appendCount === 3) {
            assert.equal(params.turn_id,indexTurn);
            assert.ok(params.events.every(event => event.data.changed === true));
            send({id:message.id,error:{code:'index_conflict',message:'turn already has different events'}}); continue;
          } else if (appendCount === 4) {
            assert.equal(params.turn_id,'uncertain');
            send({id:message.id,error:{code:'index_unavailable',message:'append outcome unknown'}}); continue;
          } else assert.fail('index helper retried an append');
          reply={version:'2.0.0',thread_id:indexThread,turn_id:indexTurn,committed:true,...(appendCount===2?{duplicate:true}:{diagnostic_path:'provider://diagnostic'})};
        } else if (method==='latest-context') {
          latestContextCount++;
          if (latestContextCount===1) {
            assert.deepEqual(params,{thread_id:indexThread});
            reply={version:'2.0.0',thread_id:indexThread,events:[
              {type:'turn',thread_id:indexThread,turn_id:indexTurn,data:{thread_id:indexThread,turn_id:indexTurn,model:indexPayload}},
              {type:'context_requests',thread_id:indexThread,turn_id:indexTurn,data:[{step_index:4,input_tokens:37}]},
            ]};
          } else {
            assert.equal(latestContextCount,2,'latest context helper retried or fell back');
            assert.deepEqual(params,{thread_id:'oversized-context'});
            send({id:message.id,error:{code:'limit_exceeded',message:'latest context exceeds read budget'}}); continue;
          }
        } else {
          assert.equal(method,'call'); queryCount++;
          if (queryCount===1) {
            assert.deepEqual(params,{thread_id:indexThread,types:['future_fact'],turn_id:indexTurn,limit:1});
            reply={version:'2.0.0',thread_id:indexThread,events:[indexBatch.events[1]],diagnostic_path:'provider://diagnostic'};
          } else {
            assert.deepEqual(params,{thread_id:indexThread});
            reply={version:'2.0.0',thread_id:indexThread,events:null};
          }
        }
      }
      else if (service==='security.authorize') {
        if(method==='acquire-authorization') {assert.equal(params.id,'effect-one');assert.equal(params.request.tool.read_only,false);reply={id:params.id,decision:{outcome:'deny',reason:'bounded policy'}};}
        else if(method==='release-authorization'){assert.deepEqual(params,{id:'effect-one'});reply={};}
        else {assert.equal(method,'authorize');reply={outcome:'allow'};}
      }
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
    assert.deepEqual(result.executionFacts,{session_id:'packed-session',agent_id:'packed-agent',main_session_id:'packed-session',active:true});
    assert.deepEqual(result.authorityMethods.map(method => method.name),['observe-execution','legacy-memory']);
    assert.deepEqual(result.effectFence,{id:'effect-one',decision:{outcome:'deny',reason:'bounded policy'}});
    assert.deepEqual(result.stewardship,stewardship);
    assert.equal(result.grantPolicy.default_lifetime_seconds,3600);
    assert.deepEqual(result.serviceAdmission,admission);
    assert.equal(result.inspection.revision,'9007199254740993');
    assert.equal(result.settingsInspection.document.values.record,'opaque');
    assert.deepEqual(result.granted,issuedGrant);
    assert.deepEqual(result.checked,{valid:false,reason:'revoked externally'});
    assert.deepEqual(result.revoked,{revoked:true});
    assert.equal(result.grantUncertain,'service_unavailable');
    assert.equal(grantIssues,2);
    assert.equal(result.protectedPathsEnforced,true);
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
    assert.equal(transferCheckCount, 1);
    assert.equal(transferCount, 3, 'failed or uncertain transfers must not retry');
    assert.equal(result.transferUnsupported, 'method_not_found');
    assert.equal(result.transferUncertain, 'service_unavailable');
    assert.equal(result.artifact.data,'AAEC');
    assert.equal(artifactDeleteCount,3);
    assert.deepEqual(result.deleted,{thread_id:artifactThread,deleted:true});
    assert.deepEqual(result.deletedAgain,result.deleted);
    assert.equal(result.artifactUncertain,'artifact_unavailable');
    assert.equal(result.deletedRead,'artifact_not_found');
    assert.equal(appendCount,4);
    assert.equal(queryCount,2);
    assert.equal(latestContextCount,2);
    assert.equal(result.contextLimit,'limit_exceeded');
    assert.equal(result.latestContext.events[0].data.model,indexPayload);
    assert.equal(result.latestContext.events[1].data[0].input_tokens,37);
    assert.deepEqual(result.appended,{version:'2.0.0',thread_id:indexThread,turn_id:indexTurn,committed:true,diagnostic_path:'provider://diagnostic'});
    assert.deepEqual(result.duplicate,{version:'2.0.0',thread_id:indexThread,turn_id:indexTurn,committed:true,duplicate:true});
    assert.equal(result.indexConflict,'index_conflict');
    assert.equal(result.indexUncertain,'index_unavailable');
    assert.deepEqual(result.facts.events,[indexBatch.events[1]]);
    assert.equal(result.facts.diagnostic_path,'provider://diagnostic');
    assert.equal(result.emptyFacts.events,null);
    assert.deepEqual(result.admitted,{admitted:false,capacity_full:true});
    assert.equal(result.admission.max_parallel,3);
    send({id:'stop',method:'shutdown'}); await next(); child.stdin.end();
    const [code]=await exited; assert.equal(code,0,stderr);
  } finally { clearTimeout(watchdog); child?.kill(); lines?.close(); await rm(dir,{recursive:true,force:true}); }
});
