// Verify the independent public-SDK decorator against the Go prompt graph.
import {execFile} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp, mkdir, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile);
const root=fileURLToPath(new URL('..',import.meta.url));
const output=resolve(process.env.WUU_PROMPT_SECTION_OUTPUT??join(root,'.tmp/e2e/prompt-section'));
await mkdir(output,{recursive:true});
const run=await mkdtemp(join(output,'run-'));
const report={passed:false,checks:[],limitations:['Controlled model transport and prompt assembly through the real Go service graph and external process; no real model inference or rendered UI.']};
let log='';
async function command(program,args,options={}) {
 log+=`${program} ${args.join(' ')}\n`;
 try {const result=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=result.stdout+result.stderr;process.stdout.write(result.stdout);return result.stdout;}
 catch(error){log+=(error.stdout??'')+(error.stderr??'');throw error;}
}
try {
 report.commit=(await command('git',['rev-parse','HEAD'])).trim();
 const {project,tsc,packed}=await preparePublicSDK({sdk:join(root,'packages/plugin-sdk'),temporary:run});
 await cp(join(root,'examples/plugins/prompt-section-decorator.ts'),join(project,'prompt-section-decorator.ts'));
 await command(process.execPath,[tsc,'prompt-section-decorator.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 report.sdk_sha256=createHash('sha256').update(await readFile(packed)).digest('hex');
 const result=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^TestPromptSectionDecorator(Process|ModelInputs)$'],{env:{...process.env,WUU_PROMPT_SECTION_FIXTURE:project}});
 if(result.includes('--- SKIP:')||(!result.includes('--- PASS: TestPromptSectionDecoratorProcess')||!result.includes('--- PASS: TestPromptSectionDecoratorModelInputs'))) throw new Error('prompt fixture did not execute');
 report.checks.push('independent packed SDK decorator changes one native section for main and worker requests','duplicate target and removed delegate fail instead of falling back','main and worker model requests carry the replaced section');
 report.passed=true;
} catch(error){report.error=String(error);process.exitCode=1;console.error(error);}
finally {await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Prompt section evidence: ${run}`);}
