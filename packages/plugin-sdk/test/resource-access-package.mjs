// Security acceptance: independently packaged SDK identity+policy providers,
// signed credentials through the real executable, scoped data and revocation.
import assert from 'node:assert/strict';
import {execFile, spawn} from 'node:child_process';
import {generateKeyPairSync, sign, createHash} from 'node:crypto';
import {once} from 'node:events';
import {cp, mkdir, mkdtemp, readFile, realpath, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from './public-package.mjs';
const exec=promisify(execFile);
const ownSDK=fileURLToPath(new URL('../',import.meta.url)),repo=resolve(ownSDK,'../..');
const sdk=resolve(process.env.WUU_AUTH_SDK_ROOT??ownSDK),binary=resolve(process.env.WUU_AUTH_BINARY??join(repo,'bin/wuu'));
const output=resolve(process.env.WUU_AUTH_OUTPUT??join(repo,'.tmp/e2e/resource-access'));await mkdir(output,{recursive:true});
const run=await mkdtemp(join(output,'run-'));const workspace=join(run,'workspace'),home=join(run,'home');await mkdir(workspace);await mkdir(home);
const env={PATH:process.env.PATH,HOME:run,WUU_HOME:home,WUU_NODE_EXECUTABLE:process.execPath};
const configPath=join(home,'config.json'),statePath=join(run,'policy.json'),jwksPath=join(run,'jwks.json');
const issuer='https://identity.fixture.invalid',audience='resource-access-fixture';
const identity={kind:'user',issuer,tenant:'tenant-a',subject:'reader'};
let state={grants:[],revoked_tokens:[]};const save=()=>writeFile(statePath,JSON.stringify(state));await save();
const {publicKey,privateKey}=generateKeyPairSync('ed25519');await writeFile(jwksPath,JSON.stringify({keys:[{...publicKey.export({format:'jwk'}),kid:'test-key'}]}));
const token=(overrides={})=>{const h=Buffer.from(JSON.stringify({alg:'EdDSA',kid:'test-key'})).toString('base64url'),p=Buffer.from(JSON.stringify({iss:issuer,aud:audience,kind:identity.kind,tenant:identity.tenant,sub:identity.subject,jti:'token-1',exp:Math.floor(Date.now()/1000)+600,...overrides})).toString('base64url');return h+'.'+p+'.'+sign(null,Buffer.from(h+'.'+p),privateKey).toString('base64url');};
const transcript=[],checks=[],servers=[];const result={passed:false,checks,run,limitations:['Synthetic pinned-JWK JWT adapter; not production OIDC discovery or SAML interoperability.','No enterprise credentials, inference, public listener or rendered UI.']};
const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
function start(authenticated=false){
 const child=spawn(binary,['app-server','--workdir',workspace,'--config',configPath,...authenticated?['--authentication=provider','--authentication-audience',audience]:[]],{env,stdio:['pipe','pipe','pipe']});
 const pending=new Map(),frames=[];let next=0,stderr='',ended=false;const exited=once(child,'exit');
 child.stderr.on('data',chunk=>{stderr+=String(chunk);});
 createInterface({input:child.stdout}).on('line',line=>{let frame;try{frame=JSON.parse(line);}catch{return};frames.push(frame);transcript.push({received:frame});const waiter=pending.get(String(frame.id));if(waiter){pending.delete(String(frame.id));clearTimeout(waiter.timer);waiter.resolve(frame);}});
 child.on('exit',()=>{ended=true;for(const waiter of pending.values()){clearTimeout(waiter.timer);waiter.reject(new Error('server exited: '+stderr));}pending.clear();});
 const server={frames,async rpc(method,params={}){if(ended)throw new Error('server already exited: '+stderr);const id=String(++next);return await new Promise((resolve,reject)=>{const timer=setTimeout(()=>{pending.delete(id);reject(new Error(method+' timed out: '+stderr));},30000);pending.set(id,{resolve,reject,timer});transcript.push({sent:{id,method,params:method==='authenticate'?{version:params.version,scheme:params.scheme,credential:'[omitted]'}:params}});child.stdin.write(JSON.stringify({id,method,params})+'\n');});},async close(){if(ended)return;child.stdin.end();const timer=setTimeout(()=>child.kill('SIGKILL'),15000);try{await exited;}finally{clearTimeout(timer);}}};servers.push(server);return server;
}
const ok=reply=>{assert(!reply.error,JSON.stringify(reply));return reply.result;};
const denied=reply=>{assert(reply.error,JSON.stringify(reply));assert.equal(reply.result,undefined);};
try{
 const {project,tsc,packed}=await preparePublicSDK({sdk,temporary:run,tarball:process.env.WUU_AUTH_SDK_TARBALL});
 await cp(join(repo,'examples/plugins/independent-access/provider.ts'),join(project,'provider.ts'));
 await cp(join(repo,'examples/compositions/minimal/engine.ts'),join(project,'engine.ts'));
 await exec(process.execPath,[tsc,'provider.ts','engine.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 const publicSDK=await import(pathToFileURL(join(project,'node_modules/@wuu/plugin-sdk/dist/index.js')));assert.equal(publicSDK.authenticationHandshake('Bearer','opaque').params.version,1);
 checks.push('independent identity/resource policy compile using packed public SDK only');
 const source=join(run,'plugin');await mkdir(source);const wrapper=join(project,'runtime.mjs');
 await writeFile(wrapper,`import {runJSONLRuntime} from '@wuu/plugin-sdk';import {createAccessProvider} from './provider.js';import {createEchoEngine} from './engine.js';const access=createAccessProvider(${JSON.stringify({issuer,jwksPath,statePath})});const engine=createEchoEngine('auth-fixture');await runJSONLRuntime({concurrentServices:['engine.wuu','identity.authentication','resource.authorization'],initialize:()=>{const a=access.initialize(),e=engine.initialize();return {...e,provided_services:[...a.provided_services,...e.provided_services]};},activate:async()=>{await access.activate();await engine.activate();},shutdown:async()=>{await engine.shutdown();await access.shutdown();},invokeService:(r,h,e)=>r.service==='engine.wuu'?engine.invokeService(r,h,e):access.invokeService(r,h,e)},{input:process.stdin,output:process.stdout});`);
 await exec(join(sdk,'node_modules/esbuild/bin/esbuild'),[wrapper,'--bundle','--platform=node','--format=esm','--outfile='+join(source,'runtime.mjs')]);
 await writeFile(join(source,'plugin.json'),JSON.stringify({schema_version:1,id:'example.external-access',name:'Independent identity and resource policy',version:'1.0.0',runtime:{protocol:'wuu-plugin-v1',command:process.execPath,args:['runtime.mjs']}}));
 await writeFile(configPath,JSON.stringify({composition:{version:4,profile:'minimal'},skills:{enabled:false}}));
 await exec(binary,['plugin','install',source],{cwd:workspace,env});
 const installed=JSON.parse(await readFile(configPath,'utf8'));
 installed.composition={version:4,profile:'minimal',patches:[{modules:{'example.external-access':true,'wuu.resource-policy.default':false},services:[{service:'engine.wuu',major:1,provider:'example.external-access'},{service:'identity.authentication',major:1,provider:'example.external-access'},{service:'resource.authorization',major:1,provider:'example.external-access'}]}]};await writeFile(configPath,JSON.stringify(installed));
 const owner=start();ok(await owner.rpc('initialize'));const a=ok(await owner.rpc('thread/start')).thread.id,b=ok(await owner.rpc('thread/start')).thread.id;
 ok(await owner.rpc('thread/rename',{thread_id:a,title:'Visible fixture'}));ok(await owner.rpc('thread/rename',{thread_id:b,title:'Hidden fixture'}));
 const actions=['thread/resume','thread/history/read','thread/attachment/read','thread/listAll','thread/rename','event:thread/resumed','event:thread/updated'];
 state.grants=[{principal:identity,actions:['initialize','thread/listAll'],resources:[{kind:'global'}]},{principal:identity,actions,resources:[{kind:'session',id:a}]}];await save();
 const viewer=start(true);const valid=token();ok(await viewer.rpc('authenticate',{version:1,scheme:'Bearer',credential:valid}));ok(await viewer.rpc('initialize'));
 assert.equal(ok(await viewer.rpc('thread/resume',{session_id:a})).thread.id,a);denied(await viewer.rpc('thread/resume',{session_id:b}));
 const list=ok(await viewer.rpc('thread/listAll',{summary_only:true}));assert.deepEqual(list.threads.map(t=>t.id),[a]);
 denied(await viewer.rpc('config/read',{principal:{...identity,subject:'owner'},owner:true}));denied(await viewer.rpc('future/unsafe',{thread_id:a}));
 checks.push('real authenticated app-server enforces scoped history/list metadata; owner self-claims and unknown methods denied');
 state.grants.push({principal:identity,actions:['thread/start'],resources:[{kind:'global'},{kind:'workspace',id:await realpath(workspace)}],permission_modes:['read_only']});await save();
 denied(await viewer.rpc('thread/start',{permission_mode:'unconfined'}));checks.push('requested permission escalation reaches independent policy attributes and is denied');
 ok(await viewer.rpc('thread/start',{permission_mode:'read_only'}));
 const beforeDeniedCreate=ok(await owner.rpc('thread/listAll',{summary_only:true})).threads.length;
 const otherWorkspace=join(run,'other-workspace');await mkdir(otherWorkspace);
 denied(await viewer.rpc('thread/start',{cwd:otherWorkspace,permission_mode:'read_only'}));
 assert.equal(ok(await owner.rpc('thread/listAll',{summary_only:true})).threads.length,beforeDeniedCreate);
 checks.push('resolved workspace create scope allows selected cwd and denies other cwd without persistence');
 state.grants=[];await save();denied(await viewer.rpc('thread/resume',{session_id:a}));denied(await viewer.rpc('thread/attachment/read',{thread_id:a,turn_id:'x',item_id:'x'}));
 checks.push('revocation applies on existing connection to real history and attachment paths');
 state.fail_policy=true;await save();denied(await owner.rpc('thread/resume',{session_id:a}));state.fail_policy=false;await save();checks.push('selected policy failure denies even owner without native fallback');
 state.revoked_tokens=['token-1'];await save();denied(await viewer.rpc('initialize'));checks.push('identity revocation rechecked on existing connection');
 for(const bad of [token({aud:'other'}),token({iss:'https://other.invalid'}),token({exp:1}),valid.slice(0,-4)+'xxxx']){const client=start(true);denied(await client.rpc('authenticate',{version:1,scheme:'Bearer',credential:bad}));await client.close();}
 checks.push('signature, issuer, audience and expiry failures rejected before app-server activation');
 const noHandshake=start(true);denied(await noHandshake.rpc('initialize'));await noHandshake.close();checks.push('provider-mode connection cannot select owner mode or skip credential handshake');
 result.binary={path:binary,sha256:hash(await readFile(binary))};result.sdk={tarball:packed,sha256:hash(await readFile(packed))};result.passed=true;
}catch(error){result.error=error.stack??String(error);throw error;}finally{for(const server of servers)await server.close();await writeFile(join(run,'result.json'),JSON.stringify(result,null,2));await writeFile(join(run,'transcript.json'),JSON.stringify(transcript,null,2));console.log(JSON.stringify(result,null,2));}
