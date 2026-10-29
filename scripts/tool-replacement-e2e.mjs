import {execFile} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp,mkdir,mkdtemp,readFile,writeFile} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile),root=fileURLToPath(new URL('..',import.meta.url));
const output=resolve(process.env.WUU_TOOL_REPLACEMENT_OUTPUT??join(root,'.tmp/e2e/tool-replacement'));
await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const report={passed:false,checks:[],limitations:['Synthetic independent plugin; no real model inference or rendered UI.']};let log='';
async function command(program,args,options={}) {log+=`${program} ${args.join(' ')}\n`;try{const result=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=result.stdout+result.stderr;process.stdout.write(result.stdout);return result.stdout;}catch(error){log+=(error.stdout??'')+(error.stderr??'');throw error;}}
try {
 report.commit=(await command('git',['rev-parse','HEAD'])).trim();
 const {project,tsc,packed}=await preparePublicSDK({sdk:join(root,'packages/plugin-sdk'),temporary:run});
 await cp(join(root,'scripts/tool-replacement-e2e/tool.ts'),join(project,'tool.ts'));
 await cp(join(root,'examples/plugins/synthetic-web-search.ts'),join(project,'synthetic-web-search.ts'));
 await command(process.execPath,[tsc,'tool.ts','synthetic-web-search.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 report.sdk_sha256=createHash('sha256').update(await readFile(packed)).digest('hex');
 const result=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^TestToolReplacement(Process|WorkerProcess|GenerationProcess|ExampleProcess)$'],{env:{...process.env,WUU_TOOL_REPLACEMENT_FIXTURE:project}});
 if(result.includes('--- SKIP:')||!result.includes('--- PASS: TestToolReplacementProcess')||!result.includes('--- PASS: TestToolReplacementWorkerProcess')||!result.includes('--- PASS: TestToolReplacementGenerationProcess')||!result.includes('--- PASS: TestToolReplacementExampleProcess'))throw new Error('tool replacement fixture did not run');
 report.checks.push('configured session and thread route direct and PTC tool calls to independent plugin','disabled target and removed provider fail without native fallback');report.passed=true;
}catch(error){report.error=String(error);process.exitCode=1;console.error(error);}
finally{await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Tool replacement evidence: ${run}`);}
