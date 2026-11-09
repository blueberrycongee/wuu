import { execFileSync } from "node:child_process";
import { mkdirSync,mkdtempSync,writeFileSync,appendFileSync } from "node:fs";
import { join,resolve,dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
const root=resolve(dirname(fileURLToPath(import.meta.url)),"..");
const evidence=join(root,".tmp/e2e/loop-policy");mkdirSync(evidence,{recursive:true});
const run=mkdtempSync(join(evidence,"run-"));
const fixture=mkdtempSync(join(tmpdir(),"wuu-independent-policy-"));
const log=join(run,"verification.log");
function command(program,args,cwd=root,env=process.env){
 appendFileSync(log,`${program} ${args.join(" ")}\n`);
 try {const output=execFileSync(program,args,{cwd,env,encoding:"utf8",stdio:["ignore","pipe","pipe"]});appendFileSync(log,output);process.stdout.write(output);return output}
 catch(error){appendFileSync(log,`${error.stdout??""}${error.stderr??""}`);throw error}
}
const packed=JSON.parse(command("npm",["pack","--json","--pack-destination",fixture],join(root,"packages/plugin-sdk")));
writeFileSync(join(fixture,"package.json"),JSON.stringify({name:"independent-policy",private:true,type:"module",dependencies:{"@wuu/plugin-sdk":`file:${join(fixture,packed[0].filename)}`}}));
command("npm",["install","--ignore-scripts","--no-audit","--no-fund"],fixture);
for(const mode of ["stop","continue"])writeFileSync(join(fixture,`${mode}.ts`),`
import {agentPolicyServiceDescriptor,composeAgentPolicies,runJSONLRuntime} from '@wuu/plugin-sdk';
const evaluate=composeAgentPolicies([
 input=>input.phase==='schedule'?{action:'serial'}:undefined,
 input=>(input.phase==='retry'||input.phase==='transport_retry')?{action:'fail'}:undefined,
 input=>input.phase==='continue'?{action:${JSON.stringify(mode)}==='continue'&&input.finish_reason==='continue'?'continue':'stop'}:undefined,
]);
runJSONLRuntime({initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[agentPolicyServiceDescriptor()]}),invokeService:async({method,params})=>{if(method!=='evaluate')throw Error(method);return evaluate(params)}},{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});
`);
command("go",["test","-race","./internal/runtime","-run","^TestSelectedLoopPolicyProcess$","-count=1","-v"],root,{...process.env,WUU_POLICY_FIXTURE:fixture});
writeFileSync(join(run,"environment.json"),JSON.stringify({fixture,node:process.version,go:command("go",["version"]).trim(),commit:command("git",["rev-parse","HEAD"]).trim()},null,2));
console.log(`Verified external SDK policy, parallel compositions, inherited child binding and removal. Evidence: ${log}`);
