// Production Personal contribution and conversation shell, synthetic in-memory
// owner service. No native host, model, account, file write or network action.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { pathToFileURL } = require('node:url');
const { createHash } = require('node:crypto');
const { chromium } = require(process.env.WUU_REVIEW_PLAYWRIGHT_PATH || 'playwright');
const target = path.resolve(__dirname, '../out/personal-fixture/index.html');
const output = path.resolve(process.env.WUU_REVIEW_UI_OUTPUT || path.join(__dirname, '../out/personal-approval-e2e'));
fs.mkdirSync(output, {recursive:true});
const report = {scope:'Current compiled production Personal contribution and conversation shell; synthetic in-memory owner service; no native host, model, account, or file effects', fixture_sha256:createHash('sha256').update(fs.readFileSync(target)).digest('hex'), cases:[], screenshots:[], identity_checks:[], errors:[]};
let browser, context, page;
const wait = selector => page.waitForSelector(selector, {state:'attached',timeout:15000});
async function open(scenario='ready', extra={}, viewport={width:1100,height:950}, sidebarCollapsed=false) {
  await context?.close();
  context=await browser.newContext({viewport,reducedMotion:'reduce'});
  await context.addInitScript(collapsed=>localStorage.setItem('wuu.desktop.sidebarCollapsed',String(collapsed)),sidebarCollapsed);
  page=await context.newPage();
  page.on('pageerror',error=>report.errors.push(String(error)));
  await page.route('**/*',route=>route.request().url().startsWith('file:')||route.request().url().startsWith('data:') ? route.continue() : route.abort());
  const url=pathToFileURL(target);url.search=new URLSearchParams({review:scenario,...extra}).toString();
  await page.goto(url.href);await wait('[data-wuu-personal]');
}
async function navigationGeometry() {
  return page.evaluate(()=>{
    const rect=node=>node?.getBoundingClientRect().toJSON();
    const sidebar=document.querySelector('[data-workbench-region="navigation"]');
    return {shell:document.querySelector('.app-shell')?.className,sidebar:rect(sidebar),ribbon:rect(document.querySelector('.sidebar-ribbon')),sidebarPointerEvents:sidebar&&getComputedStyle(sidebar).pointerEvents,controls:[...document.querySelectorAll('[data-personal-action-review] button')].filter(node=>node.checkVisibility()).map(node=>{
      const r=node.getBoundingClientRect(),hit=document.elementFromPoint((r.left+r.right)/2,(r.top+r.bottom)/2);
      return {choice:node.dataset.reviewChoice,rect:r.toJSON(),receivesPointer:node===hit||node.contains(hit),hit:hit&&{tag:hit.tagName,className:hit.className,region:hit.closest('[data-workbench-region]')?.getAttribute('data-workbench-region')}};
    })};
  });
}
async function capture(name) {
  await page.evaluate(()=>document.fonts.ready);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  const geometry=await page.evaluate(()=>({width:innerWidth,height:innerHeight,scrollWidth:document.documentElement.scrollWidth,dpr:devicePixelRatio,controls:[...document.querySelectorAll('[data-personal-action-review] button')].filter(node=>node.checkVisibility()).map(node=>{const r=node.getBoundingClientRect();return {text:node.textContent,left:r.left,right:r.right,top:r.top,bottom:r.bottom,disabled:node.disabled};})}));
  const identity=await page.evaluate(()=>{
    const conversation=document.querySelector('.mosaic-main > .plugin-conversation');
    if(!conversation?.checkVisibility())return null;
    const rect=selector=>conversation.querySelector(selector).getBoundingClientRect().toJSON();
    const identity=rect('.mosaic-identity'),viewport=rect('.plugin-conversation-scroll');
    const sideHits=[(viewport.left+identity.left)/2,(identity.right+viewport.right)/2].map(x=>Boolean(document.elementFromPoint(x,(identity.top+identity.bottom)/2)?.closest('.plugin-conversation-scroll')));
    return {header:rect('.plugin-conversation-titlebar'),identity,viewport,sideHits,firstMessage:conversation.querySelector('.plugin-conversation-message')?.getBoundingClientRect().toJSON(),scrollTop:conversation.querySelector('.plugin-conversation-scroll').scrollTop};
  });
  if(identity){
    report.identity_checks.push({name,...identity});
    assert(identity.identity.top>=identity.header.top-1 && identity.identity.bottom<=identity.header.bottom+1,`${name}: identity escapes its header footprint`);
    assert(identity.viewport.top<=identity.identity.top && identity.viewport.bottom>identity.identity.bottom,`${name}: history must scroll behind the floating identity`);
    assert(identity.sideHits.every(Boolean),`${name}: identity blocks reading-area input outside its footprint`);
    if(identity.scrollTop===0 && identity.firstMessage) assert(identity.firstMessage.top>=identity.identity.bottom,`${name}: the first message cannot be scrolled clear of the identity`);
  }
  assert(geometry.scrollWidth<=geometry.width+1, `${name}: page horizontal overflow`);
  for(const control of geometry.controls) assert(control.left>=-1 && control.right<=geometry.width+1,`${name}: clipped review control ${control.text}`);
  const file=`${name}.png`;await page.screenshot({path:path.join(output,file)});report.screenshots.push({file,geometry,navigation:await navigationGeometry()});
}
(async()=>{
  browser=await chromium.launch({headless:true,chromiumSandbox:true,...(process.env.WUU_REVIEW_CHROMIUM?{executablePath:process.env.WUU_REVIEW_CHROMIUM}:{}),args:['--disable-background-networking']});
  report.browser_version=browser.version();
  for(const [name,scenario,extra,width,height] of [
    ['chat-light','ready',{},1100,950],['chat-dark','ready',{theme:'dark'},1100,950],
    ['chat-narrow-large-long','long',{theme:'dark',font:'20'},430,1100],
    ['chat-expired','expired',{},1000,950],['chat-blocked','blocked',{},1000,950],
    ['chat-uncertain','uncertain',{},1000,950],['chat-stale','stale',{},1000,950],
    ['chat-denied','denied',{},1000,950],['chat-revoked','revoked',{},1000,950],['chat-succeeded','succeeded',{},1000,950]]) {
    await open(scenario,extra);await page.setViewportSize({width,height});await wait('[data-review-variant="conversation"]');
    if(await page.locator('.personal-review-history').count())await page.locator('.personal-review-history').evaluate(node=>node.open=true);
    await page.locator('[data-personal-action-review]').scrollIntoViewIfNeeded();await capture(name);
    assert.equal(await page.locator('[data-personal-action-review] [role="switch"]').count(),0,'chat cannot opt into AutoReview');
    if(['expired','uncertain','stale','denied','revoked','succeeded'].includes(scenario))assert.equal(await page.locator('[data-review-choice="approve_once"]:enabled').count(),0);
  }
  report.cases.push('Main chat renders consequential file approval and saved terminal states; expired/stale/unknown actions are not confirmable');
  for(const [scenario,name] of [['empty','home-empty-actions'],['unavailable','home-no-approval-service']]){
    for(const narrow of [false,true]){
      await open(scenario,narrow?{font:'20'}:{},narrow?{width:430,height:1100}:{width:1100,height:950});await wait('.mosaic-main textarea');
      assert.equal(await page.locator('[data-personal-action-review]').count(),0);assert(await page.locator('.mosaic-main textarea').isEnabled());await capture(name+(narrow?'-narrow':''));
    }
  }
  report.cases.push('Empty/unavailable action state retains the identity footprint and ordinary composer at default wide and large-text narrow sizes');
  for(const narrow of [false,true]){
    const name=narrow?'identity-scroll-narrow-large':'identity-scroll-wide';
    await open('long',{replies:'1',...(narrow?{font:'20',theme:'dark'}:{})},narrow?{width:430,height:1100}:{width:1100,height:950});
    await wait('[data-review-choice="approve_once"]');
    const viewport=page.locator('.mosaic-main > .plugin-conversation > .plugin-conversation-scroll');
    await page.locator('[data-review-choice="approve_once"]').scrollIntoViewIfNeeded();
    const before=await viewport.evaluate(node=>node.scrollTop);
    await viewport.hover();await page.mouse.wheel(0,before>0?-240:240);
    await page.waitForFunction(previous=>document.querySelector('.mosaic-main > .plugin-conversation > .plugin-conversation-scroll').scrollTop!==previous,before);
    await capture(name);
    const composer=page.locator('.mosaic-main textarea'),draft=`Keep draft ${name}`;
    await composer.fill(draft);
    const readingPosition=await viewport.evaluate(node=>node.scrollTop);
    await page.locator('[data-action="toggle-personal-panel"]').click();await wait('[data-personal-panel]:not([hidden])');
    await page.keyboard.press('Escape');
    await page.waitForFunction(()=>document.querySelector('[data-personal-panel]')?.hidden && document.activeElement===document.querySelector('[data-action="toggle-personal-panel"]'));
    assert.equal(await composer.inputValue(),draft,`${name}: profile interaction replaced the draft`);
    assert(Math.abs(await viewport.evaluate(node=>node.scrollTop)-readingPosition)<=1,`${name}: profile interaction moved the reading position`);
    await capture(`${name}-profile-return`);
    report.cases.push(`${name}: real wheel scrolling passes beneath the floating identity; profile/Escape preserves draft, reading position and focus`);
  }
  // Real pointer input catches an invisible navigation drawer over the card;
  // DOM dispatch would approve successfully even when the owner cannot click.
  for(const [name,viewport,sidebarCollapsed,extra] of [
    ['wide',{width:1100,height:950},false,{}],
    ['narrow',{width:430,height:1100},false,{font:'20'}],
    ['wide-collapsed',{width:1100,height:950},true,{}],
    ['narrow-collapsed',{width:430,height:1100},true,{font:'20'}],
  ]) {
    await open('ready',extra,viewport,sidebarCollapsed);await wait('[data-review-choice="approve_once"]');
    await page.locator('[data-review-choice="approve_once"]').click();await wait('[data-review-state="approved"]');
    assert.equal(await page.evaluate(()=>window.__reviewFixture.calls.filter(c=>c.method==='review.resolve').length),1);
    assert(await page.locator('.mosaic-main textarea').isEnabled());await capture(name==='wide'?'chat-approved':`chat-approved-${name}`);
    await page.locator('[data-review-choice="revoke"]').click();await wait('[data-review-state="revoked"]');
    assert.equal(await page.evaluate(()=>window.__reviewFixture.calls.filter(c=>c.method==='review.revoke').length),1);
    // The fix must preserve the ribbon, not make navigation inert with its aside.
    await page.locator('.sidebar-ribbon [data-wuu-plugin="automations"]').click();
    await page.locator('.sidebar-ribbon [data-wuu-plugin="personal"]').click();await wait('[data-review-state="revoked"]');
    report.cases.push(`${name}: pointer approval and withdrawal reach the exact action; ribbon remains usable`);
  }
  await open();await wait('[data-review-choice="reject"]');await page.locator('[data-review-choice="reject"]').click();await wait('[data-review-state="denied"]');
  assert.equal(await page.evaluate(()=>window.__reviewFixture.calls[0].input.choice),'reject');report.cases.push('Reject stops the precise action in the displayed synthetic state');
  await open('lost-ack');await wait('[data-review-choice="approve_once"]');await page.locator('[data-review-choice="approve_once"]').click();await wait('[data-personal-action-review] [role="alert"]');
  assert(await page.locator('[data-review-choice="approve_once"]').isDisabled());await capture('chat-lost-ack');
  await page.locator('[data-review-refresh]').click();await wait('[data-review-state="approved"]');
  assert.equal(await page.evaluate(()=>window.__reviewFixture.calls.filter(c=>c.method==='review.resolve').length),1);report.cases.push('Lost ACK locks mutations; read-only refresh restores saved decision without replay');
  await open('stale-response');await wait('[data-review-choice="approve_once"]');await page.locator('[data-review-choice="approve_once"]').click();await wait('[data-personal-action-review] [role="alert"]');await page.locator('[data-review-refresh]').click();await wait('[data-review-state="stale"]');report.cases.push('Stale decision refreshes to changed task state without resubmission');
  await open();await page.locator('[data-review-open-settings]').click();await wait('[data-review-variant="settings"]');await page.locator('[data-personal-action-review]').scrollIntoViewIfNeeded();await capture('settings-manual-default');
  const toggle=page.locator('[data-personal-action-review] [role="switch"]');assert.equal(await toggle.getAttribute('aria-checked'),'false');await toggle.click();await page.waitForFunction(()=>document.querySelector('[data-personal-action-review] [role="switch"]')?.getAttribute('aria-checked')==='true');
  assert.equal(await page.evaluate(()=>window.__reviewFixture.calls.filter(c=>c.method==='review.settings').length),1);await capture('settings-opted-in');report.cases.push('AutoReview defaults off; explicit version-bound opt-in saves once without changing Coding');
  assert.deepEqual(report.errors,[]);report.status='passed';
})().catch(async error=>{
  report.status='failed';report.error=String(error.stack||error);process.exitCode=1;
  if(page&&!page.isClosed())try{report.failure_navigation=await navigationGeometry();}catch(error){report.navigation_capture_error=String(error);}
  if(page&&!page.isClosed())try{await page.screenshot({path:path.join(output,'failure.png')});report.screenshots.push({file:'failure.png'});}catch(captureError){report.capture_error=String(captureError);}
}).finally(async()=>{fs.writeFileSync(path.join(output,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report,null,2));await context?.close();await browser?.close();});
