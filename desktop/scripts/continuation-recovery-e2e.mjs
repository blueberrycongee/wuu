// Headless production-component recovery evidence. Inference is simulated;
// public RPC/persistence and provider fault coverage live in the Go suites.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createServer } from '../node_modules/vite/dist/node/index.js';
import react from '../node_modules/@vitejs/plugin-react/dist/index.js';
import { chromium } from '../../clients/mobile-web/node_modules/playwright/index.mjs';

const desktop = fileURLToPath(new URL('..', import.meta.url));
const output = path.resolve(process.env.WUU_RECOVERY_OUTPUT || path.join(desktop, '../artifacts/issue-903/ui'));
await fs.mkdir(output, { recursive: true });
const source = '/@fs/' + desktop + '/src/renderer/';
await fs.writeFile(path.join(output, 'index.html'), '<!doctype html><html><head><meta charset="utf-8"><title>Simulated recovery evidence</title></head><body><div id="root"></div><script type="module" src="/fixture.tsx"></script></body></html>');
await fs.writeFile(path.join(output, 'fixture.tsx'), `
import React, {createRef} from 'react';
import {createRoot} from 'react-dom/client';
import {Composer} from '${source}ComposerView.tsx';
import {TurnFailureNotice} from '${source}TurnNotice.tsx';
import {ImagePreviewProvider} from '${source}ImagePreview.tsx';
import {I18nProvider} from '${source}i18n/index.tsx';
import {WorkbenchConnectionContext} from '${source}WorkbenchConnectionContext.tsx';
import '${source}styles.css';
import {applyMessageFlowFontSize} from '${source}MessageFlowFontSizeSection.tsx';
window.wuu = {initialLanguagePreference:'en-US',listSkills:async()=>({skills:[]})};
const noop=()=>{};
const root=createRoot(document.getElementById('root'));
window.continueCalls=0;window.sendCalls=0;window.applyFont=applyMessageFlowFontSize;
window.renderRecovery=(state)=>{
 const progress=state==='waiting_network'||state==='retrying';
 const reconnect={id:'recovery',type:'stream_reconnect',status:progress?'in_progress':'failed',reason:state==='waiting_network'?'waiting_network':'incomplete_stream',retry_count:2,max_retries:10,retry_at_ms:Date.now()+5000};
 const error=progress?undefined:{message:'Simulated network interruption; no live model request.',recovery:{attempt_count:3,retry_count:2,max_attempts:11,submission_count:2,stop_reason:state==='unsafe_replay'?'replay_unsafe':'retry_limit',failure_category:state==='unsafe_replay'?'replay_unsafe':'incomplete_stream'}};
 root.render(<I18nProvider><ImagePreviewProvider><WorkbenchConnectionContext.Provider value={true}>
 <main className="conversation-pane" style={{display:'block',width:'min(900px, calc(100% - 48px))',margin:'48px auto'}}>
 <p style={{color:'var(--text-secondary)',marginBottom:32}}>Simulated recovery · no live provider</p>
 <div className="turn"><div className="message-user">Complete the task</div><p>Completed progress and confirmed tool results are preserved.</p>
 <TurnFailureNotice reconnect={reconnect} error={error} onContinue={progress?undefined:()=>new Promise(resolve=>{window.continueCalls++;window.finishContinue=resolve;})}/></div>
 <Composer variant="dock" mainConversation prompt="" setPrompt={noop} files={[]} images={[]} queuedMessages={[]} guideMessages={[]}
 running={progress} status="ready" readOnly={false} initialized={{protocol_version:'wuu-app-server/v0.1',provider:'fake',model:'test',workspace_root:'/synthetic',providers:[{name:'fake',type:'openai-compatible',model:'test'}]}}
 codexModels={{loading:false,error:'',models:[]}} codexRuntimeMenu={null} codexRuntimeRef={createRef()} menuOpen={false} accessMenuOpen={false} branchMenuOpen={false} menuRef={createRef()} accessMenuRef={createRef()} workspaceFilter="" setWorkspaceFilter={noop}
 onToggleMenu={noop} onToggleAccessMenu={noop} onToggleCodexRuntimeMenu={noop} onSelectRuntimeModel={noop} onSelectRuntimeEffort={noop} onSelectPermissionMode={noop} onToggleBranchMenu={noop} onOpenSettings={noop} onOpenSkillsCatalog={noop} onSelectWorkspace={noop} onSelectNoProject={noop} onSelectGitBranch={noop} onCreateWorkspace={noop} onOpenWorkspace={noop} onStartNewThread={noop} onOpenWorkspaceTool={noop} onPasteAttachmentFiles={noop} onRemoveFile={noop} onRemoveImage={noop} onRemoveQueuedMessage={noop} onRemoveGuideMessage={noop} onGuideQueuedMessage={noop} onEditQueuedMessage={noop} onEditGuideMessage={noop}
 onSend={()=>{window.sendCalls++;}} onInterrupt={noop} onContinue={progress?undefined:()=>new Promise(resolve=>{window.continueCalls++;window.finishContinue=resolve;})} tokensPerSecond={0}/>
 </main></WorkbenchConnectionContext.Provider></ImagePreviewProvider></I18nProvider>);
};
window.renderRecovery('exhausted');
`);
const dev = await createServer({ root: output, configFile: false, publicDir: false, plugins: [react()],
  resolve: {alias:{react:path.join(desktop,'node_modules/react'),'react-dom':path.join(desktop,'node_modules/react-dom')}},
  server: {host:'127.0.0.1',port:0,fs:{allow:[path.resolve(desktop,'..')]}},
});
let browser;
const report={kind:'headless production-component fixture',inference:'simulated; no live provider',checks:[],screenshots:[],errors:[]};
try {
 await dev.listen();
 browser=await chromium.launch({headless:true,...(process.env.WUU_HEADLESS_CHROMIUM?{executablePath:process.env.WUU_HEADLESS_CHROMIUM}:{})});
 const page=await browser.newPage();
 page.on('pageerror',error=>report.errors.push(error.message));
 await page.goto(dev.resolvedUrls.local[0]);
 await page.waitForSelector('[data-wuu-component="composer-send"]');
 for(const [theme,font,width] of [['light',15,1280],['dark',20,720]]){
  await page.setViewportSize({width,height:900});
  await page.evaluate(({theme,font})=>{document.documentElement.dataset.theme=theme;window.applyFont(font);}, {theme,font});
  for(const state of ['waiting_network','retrying','exhausted','unsafe_replay']){
   await page.evaluate(state=>window.renderRecovery(state),state);
   await page.locator('[data-recovery-state="'+state+'"]').waitFor();
   const file=theme+'-'+font+'-'+width+'-'+state+'.png';
   await page.waitForTimeout(350);
   await page.screenshot({path:path.join(output,file),fullPage:true});report.screenshots.push(file);
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflow');
  }
 }
 await page.evaluate(()=>window.renderRecovery('exhausted'));
 const send=page.locator('[data-wuu-component="composer-send"]');
 await send.click();
 await page.evaluate(()=>{document.querySelector('[data-wuu-component="composer-send"]').click();});
 assert.equal(await page.evaluate(()=>window.continueCalls),1,'double click must start once');
 assert.equal(await page.evaluate(()=>window.sendCalls),0,'continue must not send a user prompt');
 await page.evaluate(()=>window.finishContinue());
 report.checks.push('four distinct recovery states','light 15px/wide and dark 20px/narrow render','no page overflow','empty continue does not send a prompt','in-flight double click starts once');
 assert.deepEqual(report.errors,[]);
} finally {
 await fs.writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2));
 await browser?.close();await dev.close();
}
console.log(JSON.stringify(report,null,2));
