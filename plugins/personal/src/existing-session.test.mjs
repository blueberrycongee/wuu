import assert from 'node:assert/strict';
import {test} from 'node:test';
import {build} from '../../node_modules/esbuild/lib/main.js';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
// Contract failures: missing exact target, response loss, mismatched host/session,
// stale operation response, and treating command acceptance as worker completion.
const dir=await mkdtemp(join(tmpdir(),'personal-existing-session-'));
await build({entryPoints:[new URL('./existing-session.ts',import.meta.url).pathname],bundle:true,platform:'node',format:'esm',outfile:join(dir,'existing-session.mjs')});
const {existingSessionTarget,existingSessionResult}=await import(pathToFileURL(join(dir,'existing-session.mjs')).href);
process.on('exit',()=>{void rm(dir,{recursive:true,force:true})});
const target={version:1,execution_host:'registered-host',session_id:'coding-session'};
const receipt=(patch={})=>({version:1,operation_id:'run-1',locator:target,status:'completed',result:{session_id:'coding-session',state:'queued',queue_id:'queue-1'},...patch});
test('existing target requires exact locator and does not infer or substitute a host',()=>{
 assert.deepEqual(existingSessionTarget(target),target);
 for(const value of [null,{}, {...target,session_id:''},{...target,execution_host:''},{...target,version:2},{...target,owner:'claimed-owner'}])assert.throws(()=>existingSessionTarget(value));
});
test('queued command receipt stays queued; accepted operation is not completed work',()=>{
 assert.deepEqual(existingSessionResult(receipt(),target,'run-1'),{session_id:'coding-session',state:'queued',queue_id:'queue-1'});
});
test('unknown or pending delivery never masquerades as rejection or requests replay',()=>{
 for(const status of ['unknown','pending'])assert.throws(()=>existingSessionResult(receipt({status}),target,'run-1'),error=>error.code==='session_outcome_unknown');
});
test('a stale or redirected receipt is not accepted as this target result',()=>{
 for(const patch of [{operation_id:'old-run'},{locator:{...target,session_id:'other'}},{locator:{...target,execution_host:'other'}},{version:2},{result:undefined}])assert.throws(()=>existingSessionResult(receipt(patch),target,'run-1'),error=>error.code==='session_outcome_unknown');
});
test('explicit receiver refusal retains its stable code instead of becoming success',()=>{
 assert.throws(()=>existingSessionResult(receipt({error:{code:'target_revoked',message:'Target authorization revoked'},result:undefined}),target,'run-1'),error=>error.code==='target_revoked');
});
