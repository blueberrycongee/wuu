import { execFileSync } from "node:child_process";
import { mkdirSync,mkdtempSync,writeFileSync,appendFileSync } from "node:fs";
import { join,resolve,dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
const root=resolve(dirname(fileURLToPath(import.meta.url)),"..");
const evidence=join(root,".tmp/e2e/engine-provider");mkdirSync(evidence,{recursive:true});
const run=mkdtempSync(join(evidence,"run-"));
const fixture=mkdtempSync(join(tmpdir(),"wuu-independent-engine-"));
const log=join(run,"verification.log");
function command(program,args,cwd=root,env=process.env){
 appendFileSync(log,`${program} ${args.join(" ")}\n`);
 try {const output=execFileSync(program,args,{cwd,env,encoding:"utf8",stdio:["ignore","pipe","pipe"]});appendFileSync(log,output);process.stdout.write(output);return output}
 catch(error){appendFileSync(log,`${error.stdout??""}${error.stderr??""}`);throw error}
}
const packed=JSON.parse(command("npm",["pack","--json","--pack-destination",fixture],join(root,"packages/plugin-sdk")));
writeFileSync(join(fixture,"package.json"),JSON.stringify({name:"independent-engine",private:true,type:"module",dependencies:{"@wuu/plugin-sdk":`file:${join(fixture,packed[0].filename)}`}}));
command("npm",["install","--ignore-scripts","--no-audit","--no-fund"],fixture);
writeFileSync(join(fixture,"engine.ts"),`
import {engineServiceDescriptor,engineGateway,requireEngineGateway,runJSONLRuntime,type EngineBinding,type EngineRunParams,type EngineSessionParams,type EngineResultV1,type RuntimePlugin} from '@wuu/plugin-sdk';
import assert from 'node:assert/strict';
const sessions=new Map<string,EngineBinding>();
const active=new Map<string,Promise<void>>();
const plugin:RuntimePlugin={
 initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[engineServiceDescriptor('wuu')],required_services:[requireEngineGateway()]}),
 async invokeService({method,params},host,execution) {
  if(method==='descriptor')return {id:'wuu',version:'1.0.0',enabled:true,capabilities:[...(process.argv[2]==='admission'?[]:['canonical-history']),'model-options',...(process.argv[2]==='delegate'?['host-managed-continuation']:[])]};
  if(method==='bind') {
   const p=params as EngineBinding;
   assert.equal(p.model,'binding-model');assert.equal(p.effort,process.argv[2]==='delegate'?undefined:'high');assert.equal(p.speed,process.argv[2]==='delegate'?undefined:'fast');
   assert.deepEqual(p.model_options,{detail:'full'});assert.equal(p.permission_mode,'default');
   assert.equal(p.instructions,'binding-instructions');assert.equal(p.external_ref,'prior-reference');
   assert.deepEqual(p.mcp_servers,[{name:'fixture',url:'http://127.0.0.1:1/mcp'}]);
   assert.equal(p.thread_id,'engine-thread');assert.ok(p.root_dir);assert.ok(p.gateway_id);
   sessions.set('session',p);return {session_id:'session'};
  }
  if(method==='close'){const id=(params as EngineSessionParams).session_id;await active.get(id);assert.ok(sessions.delete(id));return {};}
  if(method==='run') {
   const p=params as EngineRunParams;
   const binding=sessions.get(p.session_id);assert.ok(binding);
   const gateway=engineGateway(host,binding.gateway_id);
   const result=(content:string):EngineResultV1=>({version:1,content,new_messages:[{role:'assistant',content}],durable_new_messages:[]});
   if(p.messages[0]?.content==='block') {
    let finished!:()=>void;active.set(p.session_id,new Promise<void>(resolve=>{finished=resolve}));
    try {
    await gateway.emit({version:1,type:'content_delta',content:'blocking'});
    await new Promise<void>(resolve=>{if(execution.signal.aborted)resolve();else execution.signal.addEventListener('abort',()=>resolve(),{once:true})});
    await gateway.settleTurn('canceled-confirmed');
    return {result:result('canceled')};
    } finally { active.delete(p.session_id);finished(); }
   }
   await gateway.persistReference('confirmed-reference');
   await gateway.persistTurn('confirmed-turn',true);
   assert.equal(await gateway.approval({kind:'command',engine_id:'wuu',thread_id:'engine-thread',turn_id:'confirmed-turn',item_id:'approval',command:'fixture',cwd:binding.root_dir,file_path:'',reason:'fixture'}),'accept');
   assert.deepEqual(await gateway.input({engine_id:'wuu',thread_id:'engine-thread',turn_id:'confirmed-turn',item_id:'input',blocking:true,questions:[]}),{question:['answer']});
   await gateway.settleTurn('confirmed-turn');
   if(process.argv[2]==='delegate') return await gateway.runLoop(p.messages);
   await gateway.emit({version:1,type:'content_delta',content:'external-engine-ok'});
   return {result:result('external-engine-ok')};
  }
  throw Error('unsupported method '+method);
 }
};
runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});
`);
command("node",[join(root,"packages/plugin-sdk/node_modules/typescript/bin/tsc"),"--noEmit","--strict","--skipLibCheck","--target","ES2022","--module","NodeNext","--typeRoots",join(root,"packages/plugin-sdk/node_modules/@types"),join(fixture,"engine.ts")],fixture);
command("go",["test","-race","-timeout","2m","./internal/runtime","-run","^TestSelectedEngineProcess$","-count=1","-v"],root,{...process.env,WUU_ENGINE_FIXTURE:fixture});
command("go",["test","-race","-timeout","2m","./internal/appserver","-run","^TestSelectedEngineThreadAdmission$","-count=1","-v"],root,{...process.env,WUU_ENGINE_FIXTURE:fixture});
writeFileSync(join(run,"environment.json"),JSON.stringify({fixture,node:process.version,go:command("go",["version"]).trim(),commit:command("git",["rev-parse","HEAD"]).trim()},null,2));
console.log(`Verified independently installed SDK engine replacement, complete binding, callbacks and canceled-close drain. Evidence: ${log}`);
