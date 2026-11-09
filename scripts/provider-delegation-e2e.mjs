// Independent packed-SDK adapter + real Go runtime/native provider graph.
import {execFile} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp, mkdir, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile);
const root=fileURLToPath(new URL('..',import.meta.url));
const output=resolve(process.env.WUU_DELEGATION_OUTPUT??join(root,'.tmp/e2e/provider-delegation'));
await mkdir(output,{recursive:true});
const run=await mkdtemp(join(output,'run-'));
const report={passed:false,run,checks:[],limitations:['Real Go composition and external JSONL process; no rendered UI.']};
let log='';
async function command(program,args,options={}) {
 log+=`${program} ${args.join(' ')}\n`;
 try {const result=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=result.stdout+result.stderr;process.stdout.write(result.stdout);return result.stdout;}
 catch(error){log+=(error.stdout??'')+(error.stderr??'');throw error;}
}
try {
 report.commit=(await command('git',['rev-parse','HEAD'])).trim();
 report.adapter_sha256=createHash('sha256').update(await readFile(join(root,'examples/plugins/settings-decorator.ts'))).digest('hex');
 const sdk=resolve(process.env.WUU_DELEGATION_SDK_ROOT??join(root,'packages/plugin-sdk'));
 const {project,tsc,packed}=await preparePublicSDK({sdk,temporary:run,tarball:process.env.WUU_DELEGATION_SDK_TARBALL});
 await cp(join(root,'examples/plugins/settings-decorator.ts'),join(project,'settings-decorator.ts'));
 await command(process.execPath,[tsc,'settings-decorator.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 report.sdk_sha256=createHash('sha256').update(await readFile(packed)).digest('hex');
 report.checks.push('independent adapter compiles using only packed public SDK');
 const result=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^Test(ProviderDelegationSDKProcess|StateProviderDelegationAuthority|StateProviderDelegationInheritance)$'],{env:{...process.env,WUU_DELEGATION_FIXTURE:project}});
 if(result.includes('--- SKIP:')||!result.includes('--- PASS: TestProviderDelegationSDKProcess')) throw new Error('delegation fixture did not execute');
 await command('go',['test','-race','-count=1','-v','./internal/pluginhost','./internal/composition','-run','TestConsumerScoped|TestV3ResourceMigrationHonorsExplicitAuthorityChoices']);
 report.checks.push('single and chained adapters reuse real native settings reads, writes and opaque state','one method decorated, all others retain native persistence and revisions','selected route graph/provenance and removed target fail-closed','invalid consumer/declaration/major/schema/self/cycle rejected; canceled callbacks retain delegate until cleanup', 'state.documents adapter chain retains one authority and rejects ordinary consumer bypass', 'isolated scopes inherit delegate routes; parent state import removes stale routes and retains its admitted authority');
 report.passed=true;
} catch(error){report.error=String(error);process.exitCode=1;console.error(error);}
finally {await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Delegation evidence: ${run}`);}
