// Compile an independent plugin against the packed public SDK and exercise it
// through the real selected tool service and native continuation boundary.
import {execFile} from 'node:child_process';
import {cp,mkdir,mkdtemp,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';

const root=fileURLToPath(new URL('..',import.meta.url));
const output=join(root,'.tmp/e2e/lazy-native');
await mkdir(output,{recursive:true});
const run=await mkdtemp(join(output,'run-'));
const exec=promisify(execFile);
const report={passed:false,checks:[],limitations:['Synthetic plugin process; no model inference or rendered UI.']};
let log='';
async function command(program,args,options={}) {
  log+=`${program} ${args.join(' ')}\n`;
  try {
    const result=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});
    log+=result.stdout+result.stderr;
    process.stdout.write(result.stdout);
    return result.stdout;
  } catch (error) {
    log+=(error.stdout??'')+(error.stderr??'');
    throw error;
  }
}
try {
  const {project,tsc}=await preparePublicSDK({sdk:join(root,'packages/plugin-sdk'),temporary:run});
  await cp(join(root,'scripts/service-continuation-e2e/lazy-native.ts'),join(project,'lazy-native.ts'));
  await cp(join(root,'scripts/service-continuation-e2e/mcp-singleton.ts'),join(project,'mcp-singleton.ts'));
  await command(process.execPath,[tsc,'lazy-native.ts','mcp-singleton.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
  const result=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^TestNativeToolkitAdmittedAfterExternalWorkspaceStartup$|^TestNativeGenerationDependenciesSharedAcrossThreads$|^TestNativeThreadAdmissionDependencyFailureFailsClosed$|^TestNativeGenerationRevokesPluginMCPBeforeAndAfterAdmission$|^TestNativeThreadAdmissionRejectsInvalidRemoteSelection$|^TestBackgroundNativePreflightRetiresAfterFactoryFinishes$|^TestBackgroundNativeThreadAdmissionFailsWithoutWaiting$|^TestNativeGenerationInitializationCancellationProcess$'],{env:{...process.env,WUU_CONTINUATION_FIXTURE:project}});
  if(result.includes('--- SKIP:')||!result.includes('--- PASS: TestNativeToolkitAdmittedAfterExternalWorkspaceStartup')) throw new Error('lazy native fixture did not execute');
  if(!result.includes('--- PASS: TestNativeGenerationInitializationCancellationProcess')) throw new Error('generation cancellation fixture did not execute');
  if(!result.includes('--- PASS: TestBackgroundNativePreflightRetiresAfterFactoryFinishes')) throw new Error('background preflight fixture did not execute');
  if(!result.includes('--- PASS: TestNativeThreadAdmissionRejectsInvalidRemoteSelection')) throw new Error('remote failure fixture did not execute');
  if(!result.includes('--- PASS: TestNativeGenerationRevokesPluginMCPBeforeAndAfterAdmission')) throw new Error('MCP revocation fixture did not execute');
  if(!result.includes('--- PASS: TestNativeThreadAdmissionDependencyFailureFailsClosed')) throw new Error('failure fixture did not execute');
  if(!result.includes('--- PASS: TestNativeGenerationDependenciesSharedAcrossThreads')) throw new Error('shared generation fixture did not execute');
  const app=await command('go',['test','-race','-count=1','-v','./internal/appserver','-run','^TestNativeThreadAdmissionAppserverPTCAndPermissions$'],{env:{...process.env,WUU_CONTINUATION_FIXTURE:project}});
  if(app.includes('--- SKIP:')||!app.includes('--- PASS: TestNativeThreadAdmissionAppserverPTCAndPermissions')) throw new Error('appserver fixture did not execute');
  report.checks.push('external startup remains inert until thread admission selects native tools','admitted thread retains permissions and ownership through its native backend');
  report.passed=true;
} catch(error) {
  report.error=String(error);
  process.exitCode=1;
  console.error(error);
} finally {
  await writeFile(join(run,'verification.log'),log);
  await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');
  console.log(`Lazy native evidence: ${run}`);
}
