// Runs the compiled production Personal contribution in jsdom. This records
// DOM/protocol behavior only: no layout, pixels, native host, accounts or writes.
const fs=require('node:fs');
const path=require('node:path');
const assert=require('node:assert/strict');
const {webcrypto,createHash}=require('node:crypto');
const {JSDOM,VirtualConsole}=require('jsdom');
const target=path.resolve(__dirname,'../out/personal-fixture/index.html');
const output=path.resolve(process.env.WUU_REVIEW_DOM_OUTPUT||path.join(__dirname,'../out/personal-approval-dom'));
const html=fs.readFileSync(target,'utf8');fs.mkdirSync(output,{recursive:true});
const report={scope:'Compiled production Personal contribution + App shell, synthetic in-memory service, jsdom DOM execution only',visualAcceptance:false,nativeHost:false,modelCalls:false,fileEffects:false,fixture_sha256:createHash('sha256').update(html).digest('hex'),cases:[],errors:[]};
let dom,document;
async function open(scenario='ready') {
  dom?.window.close();const virtual=new VirtualConsole();
  virtual.on('jsdomError',error=>{if(error.type!=='css parsing'&&!String(error).includes('HTMLCanvasElement.prototype.getContext'))report.errors.push(String(error))});
  virtual.on('error',error=>report.errors.push(String(error)));
  dom=new JSDOM(html,{url:`https://synthetic-preview.invalid/?review=${scenario}`,runScripts:'dangerously',pretendToBeVisual:true,virtualConsole:virtual,beforeParse(window){
    window.matchMedia=query=>({matches:false,media:query,addEventListener(){},removeEventListener(){}});window.ResizeObserver=class{observe(){}unobserve(){}disconnect(){}};
    window.structuredClone=structuredClone;window.TextEncoder=TextEncoder;window.TextDecoder=TextDecoder;
    Object.defineProperty(window.crypto,'subtle',{value:{digest:(algorithm,bytes)=>webcrypto.subtle.digest(algorithm,new Uint8Array(bytes))}});
    window.URL.createObjectURL=()=> 'blob:synthetic-review';window.URL.revokeObjectURL=()=>{};
  }});document=dom.window.document;await wait(()=>document.querySelector('[data-wuu-personal]'));
}
function wait(predicate){if(predicate())return Promise.resolve();return new Promise((resolve,reject)=>{const observer=new dom.window.MutationObserver(()=>{if(predicate()){clearTimeout(timer);observer.disconnect();resolve();}});const timer=setTimeout(()=>{observer.disconnect();reject(new Error(`DOM did not settle: ${predicate}; errors=${JSON.stringify(report.errors)}; text=${document.querySelector("[data-wuu-personal]")?.textContent?.slice(-2200)}; review=${JSON.stringify(dom.window.__reviewFixture)}`));},15000);observer.observe(document,{childList:true,subtree:true,attributes:true});});}
function node(selector){const found=document.querySelector(selector);assert(found,`missing ${selector}`);return found;}
const state=status=>()=>document.querySelector(`[data-review-state="${status}"]`);
(async()=>{
  for(const scenario of ['ready','expired','blocked','stale','denied','revoked','uncertain','succeeded']){
    await open(scenario);await wait(state(['ready','expired'].includes(scenario)?'awaiting_confirmation':scenario));
    assert.equal(document.querySelector('[data-personal-action-review] [role="switch"]'),null);
    assert.equal(node('.mosaic-main textarea').disabled,false);
    if(['expired','stale','denied','revoked','uncertain','succeeded'].includes(scenario))assert.equal(document.querySelector('[data-review-choice="approve_once"]:enabled'),null);
  }
  report.cases.push('All consequential action states visible in main chat; terminal/expired actions cannot approve; chat composer stays available');
  for(const scenario of ['empty','unavailable']){await open(scenario);await wait(()=>document.querySelector('.mosaic-main textarea'));await new Promise(resolve=>setTimeout(resolve,0));assert.equal(document.querySelector('[data-personal-action-review]'),null);}
  report.cases.push('Empty and unavailable action state does not replace ordinary chat with a card');
  await open();await wait(state('awaiting_confirmation'));let approve=node('[data-review-choice="approve_once"]');approve.click();approve.click();await wait(state('approved'));
  assert.equal(dom.window.__reviewFixture.calls.length,1);assert.deepEqual({...dom.window.__reviewFixture.calls[0].input},{space_id:'local',id:'synthetic-file-1',digest:'a'.repeat(64),expected_revision:2,choice:'approve_once'});
  node('[data-review-choice="revoke"]').click();await wait(state('revoked'));report.cases.push('Double approval invokes one exact digest/revision decision; saved approval can be withdrawn');
  await open();await wait(state('awaiting_confirmation'));node('[data-review-choice="reject"]').click();await wait(state('denied'));assert.equal(dom.window.__reviewFixture.calls[0].input.choice,'reject');report.cases.push('Reject persists the specific synthetic decision');
  await open('lost-ack');await wait(state('awaiting_confirmation'));node('[data-review-choice="approve_once"]').click();await wait(()=>document.querySelector('[data-personal-action-review] [role="alert"]'));assert(node('[data-review-choice="approve_once"]').disabled);node('[data-review-choice="approve_once"]').click();node('[data-review-refresh]').click();await wait(state('approved'));assert.equal(dom.window.__reviewFixture.calls.length,1);report.cases.push('Lost ACK disables replay until read-only refresh restores saved approval');
  await open('stale-response');await wait(state('awaiting_confirmation'));node('[data-review-choice="approve_once"]').click();await wait(()=>document.querySelector('[data-personal-action-review] [role="alert"]'));node('[data-review-refresh]').click();await wait(state('stale'));assert.equal(dom.window.__reviewFixture.calls.length,1);report.cases.push('Stale response refreshes without retrying the decision');
  await open();node('[data-review-open-settings]').click();await wait(()=>document.querySelector('[data-review-variant="settings"] [role="switch"]'));
  let toggle=node('[data-review-variant="settings"] [role="switch"]');assert.equal(toggle.getAttribute('aria-checked'),'false');toggle.click();toggle.click();await wait(()=>document.querySelector('[data-review-variant="settings"] [role="switch"]')?.getAttribute('aria-checked')==='true');
  assert.equal(dom.window.__reviewFixture.calls.filter(c=>c.method==='review.settings').length,1);assert.equal(dom.window.__reviewFixture.calls[0].input.expected_revision,1);report.cases.push('Explicit AutoReview opt-in changes a saved Personal settings revision once');
  assert.deepEqual(report.errors,[]);report.status='passed';
})().catch(error=>{report.status='failed';report.error=String(error.stack||error);process.exitCode=1}).finally(()=>{dom?.window.close();fs.writeFileSync(path.join(output,'report.json'),JSON.stringify(report,null,2)+'\n');process.stdout.write(JSON.stringify(report,null,2)+'\n');});
