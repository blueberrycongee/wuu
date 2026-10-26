import { execFileSync } from "node:child_process";
import { mkdirSync,mkdtempSync,writeFileSync,appendFileSync } from "node:fs";
import { join,resolve,dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
const root=resolve(dirname(fileURLToPath(import.meta.url)),"..");
const evidence=join(root,".tmp/e2e/model-capacity");mkdirSync(evidence,{recursive:true});
const run=mkdtempSync(join(evidence,"run-"));
const fixture=mkdtempSync(join(tmpdir(),"wuu-independent-capacity-"));
const log=join(run,"verification.log");
function command(program,args,cwd=root,env=process.env){
 appendFileSync(log,`${program} ${args.join(" ")}\n`);
 try {const output=execFileSync(program,args,{cwd,env,encoding:"utf8",stdio:["ignore","pipe","pipe"]});appendFileSync(log,output);process.stdout.write(output);return output}
 catch(error){appendFileSync(log,`${error.stdout??""}${error.stderr??""}`);throw error}
}
const packed=JSON.parse(command("npm",["pack","--json","--pack-destination",fixture],join(root,"packages/plugin-sdk")));
writeFileSync(join(fixture,"package.json"),JSON.stringify({name:"independent-capacity",private:true,type:"module",dependencies:{"@wuu/plugin-sdk":`file:${join(fixture,packed[0].filename)}`}}));
command("npm",["install","--ignore-scripts","--no-audit","--no-fund"],fixture);
writeFileSync(join(fixture,"capacity.ts"),`
import {modelCapacityServiceDescriptor,runJSONLRuntime} from '@wuu/plugin-sdk';
const leases=new Set();let sequence=0;
runJSONLRuntime({initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[modelCapacityServiceDescriptor()]}),invokeService:async({method,params})=>{
 if(method==='acquire'){const lease='external-'+(++sequence);leases.add(lease);return {lease}}
 if(method==='settle'){leases.delete(params.lease);return {}}
 throw Error(method);
}},{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});
`);
writeFileSync(join(fixture,"receipt.ts"),`
import {connect} from 'node:net';
import {createInterface} from 'node:readline';
import {createModelSubmission,updateModelSubmission,requireModelReceiptService,runJSONLRuntime} from '@wuu/plugin-sdk';
runJSONLRuntime({
 initialize:()=>({protocol_version:3,lifecycle_version:2,required_services:[requireModelReceiptService()],provided_services:[{name:'model.adapter',version:'1.0.0',methods:[{name:'describe',input_schema:'model.adapter.describe.input.v1',output_schema:'model.adapter.describe.output.v1'}]}],model_adapters:[{id:'external.receipt',version:1,mode:'receipt'}],capabilities:[{id:'model.adapter',kind:'decision',version:1,error_policy:'propagate'}]}),
 invokeService:async()=>({id:'external.receipt',version:1,mode:'receipt'}),
 invokeCapability:async(params,host,execution)=>{
  const socket=connect(Number(process.argv[2]),'127.0.0.1');
  await new Promise((resolve,reject)=>{socket.once('connect',resolve);socket.once('error',reject)});
  const lines=createInterface({input:socket})[Symbol.asyncIterator]();
  const receipt=params.input.receipt;
  const submission=await createModelSubmission(host,receipt,{provider:'fixture',capacity_scope:'common-account',protocol:'fixture',transport:'controlled',mode:'external'});
  const canceled=()=>socket.write('canceled\\n');
  execution.signal.addEventListener('abort',canceled,{once:true});
  if(execution.signal.aborted)canceled();
  socket.write('admitted\\n');
  for await(const command of {[Symbol.asyncIterator]:()=>lines}){
   if(command==='settle'){
    await updateModelSubmission(host,receipt,{id:submission.id,outcome:'succeeded',usage:{input_tokens:7,output_tokens:4}},true);
    socket.write('settled\\n');
   }
   if(command==='return'){socket.end();return {output:{response:{content:'late',finish_reason:'stop'}}}}
  }
  throw Error('control closed');
 }
},{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});
`);
command("go",["test","-race","./internal/runtime","-run","^TestSelectedCapacityProcess","-count=1","-v"],root,{...process.env,WUU_CAPACITY_FIXTURE:fixture});
writeFileSync(join(run,"environment.json"),JSON.stringify({fixture,node:process.version,go:command("go",["version"]).trim(),commit:command("git",["rev-parse","HEAD"]).trim()},null,2));
console.log(`Verified SDK-only capacity strategy and shared physical owner across independent compositions. Evidence: ${log}`);
