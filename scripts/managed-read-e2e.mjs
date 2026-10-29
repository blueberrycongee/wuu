import {execFile} from 'node:child_process';
import {cp,mkdir,mkdtemp,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile),root=fileURLToPath(new URL('..',import.meta.url));
const output=join(root,'.tmp/e2e/managed-read');await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const report={passed:false,checks:[],limitations:['Synthetic local result; no external service or model inference.']};let log='';
async function command(program,args,options={}){log+=`${program} ${args.join(' ')}\n`;try{const r=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=r.stdout+r.stderr;process.stdout.write(r.stdout);return r.stdout;}catch(e){log+=(e.stdout??'')+(e.stderr??'');throw e;}}
try{
 const {project,tsc}=await preparePublicSDK({sdk:join(root,'packages/plugin-sdk'),temporary:run});
 await cp(join(root,'scripts/managed-read-e2e/fixture.ts'),join(project,'fixture.ts'));
 await command(process.execPath,[tsc,'fixture.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 const out=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^TestManagedReadPluginProcess$'],{env:{...process.env,WUU_MANAGED_READ_FIXTURE:project}});
 if(out.includes('--- SKIP:')||!out.includes('--- PASS: TestManagedReadPluginProcess')) throw Error('managed read process test did not run');
 report.checks.push('plugin nested read, session-root replacement, foreign session and permissions');report.passed=true;
}catch(e){report.error=String(e);process.exitCode=1;console.error(e);}finally{await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Recovery evidence: ${run}`);}
