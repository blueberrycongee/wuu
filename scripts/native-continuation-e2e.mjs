import {execFile} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp,mkdir,mkdtemp,readFile,writeFile} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile),root=fileURLToPath(new URL('..',import.meta.url));
const output=resolve(process.env.WUU_NATIVE_CONTINUATION_OUTPUT??join(root,'.tmp/e2e/native-continuation'));
await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const report={passed:false,checks:[],limitations:['Synthetic independent plugin and controlled native-search stand-in; no real search, model inference or rendered UI.']};let log='';
async function command(program,args,options={}) {log+=`${program} ${args.join(' ')}\n`;try{const result=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=result.stdout+result.stderr;process.stdout.write(result.stdout);return result.stdout;}catch(error){log+=(error.stdout??'')+(error.stderr??'');throw error;}}
try {
 report.commit=(await command('git',['rev-parse','HEAD'])).trim();
 const {project,tsc,packed}=await preparePublicSDK({sdk:join(root,'packages/plugin-sdk'),temporary:run});
 await cp(join(root,'scripts/native-continuation-e2e/tool.ts'),join(project,'tool.ts'));
 await cp(join(root,'examples/plugins/native-search-decorator.ts'),join(project,'native-search-decorator.ts'));
 await command(process.execPath,[tsc,'tool.ts','native-search-decorator.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 report.sdk_sha256=createHash('sha256').update(await readFile(packed)).digest('hex');
 const result=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^TestNativeToolContinuation'],{env:{...process.env,WUU_NATIVE_CONTINUATION_FIXTURE:project}});
 if(result.includes('--- SKIP:'))throw new Error('native continuation fixture was skipped');
 for(const name of ['TestNativeToolContinuationProcess','TestNativeToolContinuationGenerationProcess','TestNativeToolContinuationRevokedAfterAdmission','TestNativeToolContinuationRejectsOrdinaryPlugin','TestNativeToolContinuationExampleProcess','TestNativeToolContinuationSharedCallbackCapabilityProcess']) if(!result.includes(`--- PASS: ${name}`))throw new Error(`${name} did not run`);
 await command('go',['test','-race','-count=1','./internal/tools','-run','^TestNativeSearchContinuation']);
 report.checks.push('controlled native result continues through selected replacement plugin, host service and scheduler in direct and PTC modes','original identity, revocation, callback expiry, cancellation and in-flight generation are checked; evidence remains isolated until final validation','a trusted plugin may share a live callback capability with another of its own callbacks');report.passed=true;
}catch(error){report.error=String(error);process.exitCode=1;console.error(error);}
finally{await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Native continuation evidence: ${run}`);}
