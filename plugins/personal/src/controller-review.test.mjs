import assert from 'node:assert/strict';
import {test} from 'node:test';
import {build} from '../../node_modules/esbuild/lib/main.js';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
// Decision content is not authority: only the separately bound task/revision/run
// may receive an intent. Reject redirected, stale, unbounded and empty decisions.
const dir=await mkdtemp(join(tmpdir(),'personal-controller-review-'));
await build({entryPoints:[new URL('./controller-review.ts',import.meta.url).pathname],bundle:true,platform:'node',format:'esm',outfile:join(dir,'review.mjs')});
const {controllerReviewDecision}=await import(pathToFileURL(join(dir,'review.mjs')).href);
process.on('exit',()=>{void rm(dir,{recursive:true,force:true})});
const binding={task_id:'task',requirements_revision:2,worker_request_id:'worker-run'};
const input={...binding,decision:'accept',summary:'The requested result is present',evidence:['The worker response describes the requested change']};
test('controller decisions bind exact task revision and worker rather than content claims',()=>{
 assert.equal(controllerReviewDecision(input,binding).decision,'accept');
 for(const patch of [{task_id:'other'},{requirements_revision:1},{worker_request_id:'old'},{decision:'grant'},{summary:''},{evidence:[]},{evidence:['']},{evidence:['x'.repeat(4001)]}])assert.throws(()=>controllerReviewDecision({...input,...patch},binding));
});
test('same-task followup and user question remain distinct decision intents',()=>{
 assert.equal(controllerReviewDecision({...input,decision:'follow_up',follow_up:'Explain the failing test before changing anything else'},binding).follow_up,'Explain the failing test before changing anything else');
 assert.equal(controllerReviewDecision({...input,decision:'ask_user',question:'Which branch should I use?'},binding).question,'Which branch should I use?');
 assert.throws(()=>controllerReviewDecision({...input,decision:'follow_up'},binding));
 assert.throws(()=>controllerReviewDecision({...input,decision:'ask_user'},binding));
});
