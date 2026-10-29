import {execFile} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp,mkdir,mkdtemp,readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from '../packages/plugin-sdk/test/public-package.mjs';
const exec=promisify(execFile),root=fileURLToPath(new URL('..',import.meta.url));
const output=join(root,'.tmp/e2e/remote-read');await mkdir(output,{recursive:true});const run=await mkdtemp(join(output,'run-'));
const report={passed:false,checks:[],limitations:['Uses an isolated command-backend worker, a trusted synthetic plugin and a runnable example; no production remote service or real model.']};let log='';
async function command(program,args,options={}) {log+=`${program} ${args.join(' ')}\n`;try{const result=await exec(program,args,{cwd:root,maxBuffer:16*1024*1024,...options});log+=result.stdout+result.stderr;process.stdout.write(result.stdout);return result.stdout;}catch(error){log+=(error.stdout??'')+(error.stderr??'');throw error;}}
try {
 report.commit=(await command('git',['rev-parse','HEAD'])).trim();
 const {project,tsc,packed}=await preparePublicSDK({sdk:join(root,'packages/plugin-sdk'),temporary:run});
 await cp(join(root,'scripts/remote-read-e2e/fixture.ts'),join(project,'fixture.ts'));
 await cp(join(root,'scripts/remote-read-e2e/image-fixture.ts'),join(project,'image-fixture.ts'));
 await cp(join(root,'examples/plugins/remote-text-reader.ts'),join(project,'remote-text-reader.ts'));
 await cp(join(root,'examples/plugins/remote-image-reader.ts'),join(project,'remote-image-reader.ts'));
 await command(process.execPath,[tsc,'fixture.ts','image-fixture.ts','remote-text-reader.ts','remote-image-reader.ts','--module','NodeNext','--moduleResolution','NodeNext','--target','ES2023','--strict','--skipLibCheck'],{cwd:project});
 report.sdk_sha256=createHash('sha256').update(await readFile(packed)).digest('hex');
 await command('go',['build','-o',join(run,'wuu'),'./cmd/wuu']);
 const output=await command('go',['test','-race','-count=1','-v','./internal/runtime','-run','^Test(RemoteReadOnlyContinuation|RemoteObservationRealWorkerTransactions|RemoteDirectReadRechecksAuthorizationAfterContinuation|RemoteImageReaderProcess|RemoteImageReaderExampleProcess)'],{env:{...process.env,WUU_REMOTE_READ_WORKER_BINARY:join(run,'wuu'),WUU_REMOTE_READ_FIXTURE:project}});
 for(const name of ['TestRemoteReadOnlyContinuationWorkerProcess','TestRemoteReadOnlyContinuationWorkerActorModes','TestRemoteReadOnlyContinuationPluginProcess','TestRemoteObservationRealWorkerTransactions','TestRemoteDirectReadRechecksAuthorizationAfterContinuation','TestRemoteImageReaderProcess','TestRemoteImageReaderExampleProcess']) if(!output.includes(`--- PASS: ${name}`))throw Error(`${name} did not run`);
 if(output.includes('--- SKIP:'))throw Error('a required process test was skipped');
 if(process.platform!=='win32') {
  const recovery=await command('go',['test','-race','-count=1','-v','./internal/runtime','./internal/appserver','./internal/agentcontrol','-run','^Test(RemoteReplacementCodeModeIsStateless|RemoteErrorBeforeSettlementCannotExposeNativeText|RemoteObservation(DoesNotCommitWhenFinalizationFails|DoesNotCommitWhenPTCParentFails|CommitsWhenPTCParentSucceeds|CommitsTwoPTCChildrenAtomically|RevokedChildAbortsPTCBatch|RevokedAfterStageAbortsPTCBatch|RecoversSameWorkerAfterReconnect|WorkerRestartRejectsOldReceipt)|ThreadAdmissionRecoversRemoteObservationFromOriginalWorker|WorkerFollowupRecoversRemoteObservationUnderExecutionLease)$'],{env:{...process.env,WUU_REMOTE_READ_WORKER_BINARY:join(run,'wuu'),WUU_REMOTE_READ_FIXTURE:project}});
  for(const name of ['TestRemoteReplacementCodeModeIsStateless','TestRemoteErrorBeforeSettlementCannotExposeNativeText','TestRemoteObservationDoesNotCommitWhenPTCParentFails','TestRemoteObservationCommitsTwoPTCChildrenAtomically','TestRemoteObservationRevokedChildAbortsPTCBatch','TestRemoteObservationRevokedAfterStageAbortsPTCBatch','TestRemoteObservationRecoversSameWorkerAfterReconnect','TestThreadAdmissionRecoversRemoteObservationFromOriginalWorker','TestWorkerFollowupRecoversRemoteObservationUnderExecutionLease']) if(!recovery.includes(`--- PASS: ${name}`))throw Error(`${name} did not run`);
  if(recovery.includes('--- SKIP:'))throw Error('a required recovery test was skipped');
 }
 await command('go',['test','-race','-count=1','./internal/agent','./internal/toolctx','./internal/toolledger','-run','^Test(Settlement|InvocationSettlement|RemoteAbort|RemoteCommit|ObservationIntent|InvalidFinalizer|NestedInvalidFinalizer|FailedRemotePTC|CanceledRemotePTCDiscardsPendingOutput)']);
 await command('go',['test','-race','-count=1','./internal/executionenv','./internal/executionworker','./internal/tools','./internal/runtime','-run','^Test(UnobservedRead|RemoteReadOnlyContinuation|SanitizeRemoteReadResult|RemoteProcessScopeUsesCapturedExecutor|ReadObservationProtocol|ReadObservationAbortAndClose|RemoteObservationCommit|RemoteObservationCaptureBounds|ObservationOldWorker|ObservationLostCommit|StatelessCodeRejectsOlderWorkerBeforeDispatch)']);
 report.checks.push('packed SDK plugin and runnable example use a real pinned worker and PTC; local and remote same-name files differ','authorization, identities, actor and environment change, cancellation, retirement, unsupported workers and read observation failure boundaries','durable direct and Code Mode settlement, same-worker recovery, actor binding and unknown outcomes');report.passed=true;
}catch(error){report.error=String(error);process.exitCode=1;console.error(error);}
finally{await writeFile(join(run,'verification.log'),log);await writeFile(join(run,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Remote read evidence: ${run}`);}
