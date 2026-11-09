// Public-SDK-only selected tools decorator and real external process authority.
import {execFile} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp,mkdir,mkdtemp,readFile,writeFile} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile),root=fileURLToPath(new URL('..',import.meta.url));
const output=resolve(process.env.WUU_CONTINUATION_OUTPUT??join(root,'.tmp/e2e/service-continuation'));
await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const report={passed:false,run,checks:[],limitations:['Real Go generation publication and external JSONL providers; no rendered UI.']};let log='';
async function command(program,args,options={}){log+=`${program} ${args.join(' ')}\n`;try{const result=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=result.stdout+result.stderr;process.stdout.write(result.stdout);return result.stdout;}catch(error){log+=(error.stdout??'')+(error.stderr??'');throw error;}}
try{
 report.commit=(await command('git',['rev-parse','HEAD'])).trim();
 const sdk=resolve(process.env.WUU_CONTINUATION_SDK_ROOT??join(root,'packages/plugin-sdk'));
 const {project,tsc,packed}=await preparePublicSDK({sdk,temporary:run,tarball:process.env.WUU_CONTINUATION_SDK_TARBALL});
 await cp(join(root,'examples/plugins/tool-service-decorator.ts'),join(project,'tool-service-decorator.ts'));
 for(const name of ['decorator.ts','tools.ts'])await cp(join(root,'scripts/service-continuation-e2e',name),join(project,name));
 await command(process.execPath,[tsc,'tool-service-decorator.ts','decorator.ts','tools.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 report.sdk_sha256=createHash('sha256').update(await readFile(packed)).digest('hex');
 report.example_sha256=createHash('sha256').update(await readFile(join(project,'tool-service-decorator.ts'))).digest('hex');
 report.checks.push('external decorator and independent process backend compile against only packed SDK');
 const result=await command('go',['test','-race','-count=1','-timeout=2m','-v','./internal/runtime','-run','^TestServiceContinuationSDK'],{env:{...process.env,WUU_CONTINUATION_FIXTURE:project}});
 if(result.includes('--- SKIP:'))throw new Error('continuation fixture was skipped');
 for(const name of ['TestServiceContinuationSDKProcess','TestServiceContinuationSDKReplacementAndRemoval','TestServiceContinuationSDKNativeDelegate']){
  if(!result.includes(`--- PASS: ${name} (`))throw new Error(`${name} did not execute`);
 }
 report.checks.push('single/chained decorators preserve exact process thread/actor authority and base context','ordinary delegation cannot acquire process authority; security denial and provider failure do not fall back','default native delegate follows the same gate: continuation succeeds; ordinary execute and rewritten list/resolve calls reject','duplicate continuation starts only one physical process','pause-before-next then actual generation publication: old work executes old provider; new work executes new provider','cancellation, owner/delegate removal and post-pause permission revocation start no process; removal rejects new work');
 await command('go',['test','-race','-count=1','./internal/pluginhost','-run','TestSelectedServiceContinuation|TestServiceContinuationWire']);
 report.checks.push('exact owner/callback/generation, late calls, concurrent duplicate, failure and revoked-delegate boundaries');
 report.passed=true;
}catch(error){report.error=String(error);process.exitCode=1;console.error(error);}
finally{await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Continuation evidence: ${run}`);}
