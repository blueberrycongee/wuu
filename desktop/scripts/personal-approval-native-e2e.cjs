// Production renderer → actual authenticated UserHost → actual local command.
// The Go harness owns the temporary workspace and HTTP endpoint lifetime.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const playwrightPath=process.env.WUU_REVIEW_PLAYWRIGHT_PATH || '../../clients/mobile-web/node_modules/playwright';
const {chromium}=require(playwrightPath);
const {expect}=require(path.join(playwrightPath,'test'));
const [origin,output] = process.argv.slice(2);
(async()=>{
 const browser=await chromium.launch({headless:true,chromiumSandbox:true,...(process.env.WUU_REVIEW_CHROMIUM?{executablePath:process.env.WUU_REVIEW_CHROMIUM}:{}),args:['--disable-background-networking']});
 try {
  const page=await browser.newPage({viewport:{width:1100,height:950}});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(origin+'/?review=ready&nativeApproval=1');
  const card=page.locator('[data-personal-action-review]');
  await card.locator('[data-review-choice="approve_once"]').waitFor();
  const initial=await (await page.request.post(origin+'/native-review',{data:{method:'review.list',input:{}}})).json();
  const before=await (await page.request.get(origin+'/native-effect')).json();
  assert.equal(before.content,'','pending command ran before owner choice');
  await card.scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(output,'native-pending.png')});
  await card.locator('[data-review-choice="remember"]').click();
  await card.locator('[data-review-rule-preview]').waitFor();
  await page.screenshot({path:path.join(output,'native-rule-preview.png')});
  let offline=true;
  await page.route('**/native-review',route=>{
   if(offline && route.request().postDataJSON()?.method==='review.list')return route.abort('internetdisconnected');
   return route.continue();
  });
  await card.locator('[data-review-choice="save_rule"]').click();
  await expect.poll(async()=> (await (await page.request.get(origin+'/native-effect')).json()).content).toBe('once');
  await expect(card.locator('[data-review-reconciling]')).toBeVisible({timeout:15000});
  await expect(card.locator('[data-review-refresh]')).toBeEnabled();
  await expect(card.locator('[data-review-choice="approve_once"]')).toBeDisabled();
  for(const [theme,width,font] of [['light',1100,15],['dark',720,20]]){
   await page.setViewportSize({width,height:950});
   await page.evaluate(({theme,font})=>{document.documentElement.dataset.theme=theme;document.documentElement.style.setProperty('--conversation-message-font-size',font+'px')},{theme,font});
   await card.scrollIntoViewIfNeeded();
   await page.screenshot({path:path.join(output,`native-unknown-${theme}.png`)});
  }
  offline=false;
  await card.locator('[data-review-refresh]').click();
  const request=async(method,input={})=>{
   const response=await page.request.post(origin+'/native-review',{data:{method,input}});
   assert.equal(response.ok(),true,await response.text());return (await response.json()).value;
  };
  await expect.poll(async()=> (await request('review.list')).requests[0]?.status).toBe('succeeded');
  const snapshot=await request('review.list');
  const action=snapshot.requests[0];
  assert.equal(action.status,'succeeded');
  // The host has accepted and executed, but the submit response is still held.
  await expect(card.locator('[data-review-choice="approve_once"]')).toHaveCount(0,{timeout:15000});
  await page.screenshot({path:path.join(output,'native-ack-pending-recovered.png')});
  await page.request.post(origin+'/release-ack');
  const rules=await request('review.rules');
  assert.equal(rules.rules.length,1);
  await request('review.resolve',{space_id:action.action.scope.space_id,id:action.action.id,digest:action.action.digest,expected_revision:initial.value.requests[0].revision,choice:'approve_once'});
  assert.equal((await (await page.request.get(origin+'/native-effect')).json()).content,'once','duplicate click repeated effect');
  await expect(card).toHaveCount(0);
  await page.screenshot({path:path.join(output,'native-completed.png')});
  assert.deepEqual(errors,[]);
  fs.writeFileSync(path.join(output,'report.json'),JSON.stringify({verified:true,browser:browser.version(),cases:['pending has no effect','rule preview','save and allow once','native shell executed once','duplicate owner choice does not replay','UI recovers while accepted response is held','read outage retains unknown state and locks resubmission','light/default/wide and dark/large/narrow recovery view'],action_id:action.action.id,rule_count:rules.rules.length},null,2));
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
