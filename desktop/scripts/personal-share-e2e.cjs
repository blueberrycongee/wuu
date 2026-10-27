// Repeatable synthetic receiver acceptance; does not contact real accounts.
const {createRequire}=require('node:module');
const {mkdir,writeFile}=require('node:fs/promises');
const path=require('node:path');const http=require('node:http');const assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../..');const req=createRequire(path.join(root,'plugins/package.json'));
(async()=>{
 const output=process.env.WUU_SHARE_E2E_OUTPUT||path.join(root,'.tmp/e2e/personal-share');await mkdir(output,{recursive:true});
 const source=`import React from 'react';import {createRoot} from 'react-dom/client';import {createShareCapturePanel,shareCaptureStyles} from '../plugins/personal/ui/share-capture-panel.ts';
 window.calls=[];window.fail=true;const Panel=createShareCapturePanel(React,{},async(method,input)=>{window.calls.push({method,input});await new Promise(r=>setTimeout(r,30));if(window.fail)throw Error('synthetic transport loss');return {state:'queued'};});
 const css=document.createElement('style');css.textContent=shareCaptureStyles;document.head.append(css);createRoot(document.getElementById('root')).render(React.createElement(Panel));`;
 const built=await req('esbuild').build({stdin:{contents:source,resolveDir:path.join(root,'desktop'),loader:'tsx'},bundle:true,write:false,platform:'browser',format:'iife',nodePaths:[path.join(root,'desktop/node_modules')]});
 const html=`<!doctype html><meta charset=utf-8><title>Personal share acceptance</title><style>:root{--space-2:8px;--space-3:12px;--space-5:20px;--space-6:24px;--hairline:#ccc;color-scheme:light dark}body{font:15px system-ui;max-width:720px;margin:24px auto;padding:0 20px}input,textarea,button{padding:10px;border:1px solid #888;border-radius:8px;background:transparent;color:inherit;font:inherit}p{line-height:1.6}button{cursor:pointer}label{min-width:0}input[type=file]{width:100%}</style><div id=root></div><script>${built.outputFiles[0].text}</script>`;
 const server=http.createServer((_,res)=>{res.setHeader('Content-Type','text/html; charset=utf-8');res.end(html);});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const {chromium}=require('playwright');let browser;try{browser=await chromium.launch({executablePath:'/usr/bin/chromium',headless:true,args:['--no-sandbox']});}catch(error){await new Promise(r=>server.close(r));throw error;}
 try{const page=await browser.newPage();await page.goto(`http://127.0.0.1:${server.address().port}`);
 await page.getByLabel('链接',{exact:true}).fill('https://example.org/article');await page.getByLabel('文字',{exact:true}).fill('A shared quote, not an instruction.');
 await page.getByRole('button',{name:'保存到主聊天'}).click();await page.getByRole('status').filter({hasText:'未确认'}).waitFor();
 await page.getByRole('button',{name:'保存到主聊天'}).click();await page.getByRole('status').filter({hasText:'未确认'}).waitFor();
 const calls=await page.evaluate(()=>window.calls);assert.equal(calls.length,2);assert.equal(calls[0].input.request_id,calls[1].input.request_id);
 await page.evaluate(()=>window.fail=false);await page.getByRole('button',{name:'保存到主聊天'}).click();await page.getByRole('status').filter({hasText:'已交给'}).waitFor();
 assert.equal(await page.getByLabel('链接',{exact:true}).inputValue(),'');
 await page.getByLabel('文字',{exact:true}).fill('discarded');await page.getByRole('button',{name:'清空',exact:true}).click();assert.equal((await page.evaluate(()=>window.calls)).length,3);
 await page.getByLabel('导入浏览器当前页文件',{exact:true}).setInputFiles({name:'bad.wuu-share.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify({schema:'wuu.share.v1',capture_id:'capture-123',kind:'current_page',url:'file:///etc/passwd'}))});await page.getByRole('status').filter({hasText:'文件无效'}).waitFor();
 for(const [name,width,font,scheme] of [['wide',1100,15,'light'],['narrow',390,15,'light'],['large',390,20,'light'],['dark',1100,15,'dark']]){await page.setViewportSize({width,height:900});await page.emulateMedia({colorScheme:scheme});await page.evaluate(font=>document.body.style.fontSize=font+'px',font);await page.screenshot({path:path.join(output,name+'.png'),fullPage:true});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);}
 await writeFile(path.join(output,'result.json'),JSON.stringify({passed:true,checks:['uncertain outcome retries retain identity','successful receipt clears draft','clear never sends','unsafe URL import rejected','wide/narrow/largefont/dark rendered without horizontal overflow'],scope:'synthetic browser receiver, not installed extension or native device'},null,2));console.log(output);
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
