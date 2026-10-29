import {execFile} from 'node:child_process';
import {cp,mkdir,mkdtemp,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile),root=fileURLToPath(new URL('..',import.meta.url));
const output=join(root,'.tmp/e2e/result-recovery');await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const report={passed:false,checks:[],limitations:['Synthetic local result; no external service or model inference.']};let log='';
async function command(program,args,options={}){log+=`${program} ${args.join(' ')}\n`;try{const r=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=r.stdout+r.stderr;process.stdout.write(r.stdout);return r.stdout;}catch(e){log+=(e.stdout??'')+(e.stderr??'');throw e;}}
try{
 const {project,tsc}=await preparePublicSDK({sdk:join(root,'packages/plugin-sdk'),temporary:run});
 await cp(join(root,'scripts/result-recovery-e2e/fixture.ts'),join(project,'fixture.ts'));
 await cp(join(root,'examples/plugins/result-recovery.ts'),join(project,'result-recovery.ts'));
 await command(process.execPath,[tsc,'fixture.ts','result-recovery.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 const out=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^(TestResultRecovery(Process|PTCProcess)|TestSideRecoveryRetainsSessionLeaseProcess)$'],{env:{...process.env,WUU_RESULT_RECOVERY_FIXTURE:project}});
 if(out.includes('--- SKIP:')||!out.includes('--- PASS: TestResultRecoveryProcess')||!out.includes('--- PASS: TestResultRecoveryPTCProcess')||!out.includes('--- PASS: TestSideRecoveryRetainsSessionLeaseProcess')) throw Error('result recovery test did not run');
 report.checks.push('archived output round trips via native, legacy, documented plugin orchestrator and code-mode paths','denied cross-session, changed artifact, canceled call and disabled original reader');report.passed=true;
}catch(e){report.error=String(e);process.exitCode=1;console.error(e);}finally{await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Recovery evidence: ${run}`);}
