import type * as React from 'react';
import { normalizeShareCapture } from '../src/share-capture.js';
type Invoke=(method:string,input?:unknown)=>Promise<unknown>;
/** The import remains an explicit owner gesture; Close never sends. Retain the
 * same capture_id after uncertain delivery so the host can deduplicate retries. */
export function createShareCapturePanel(react:typeof React,_ui:unknown,invoke:Invoke){
 const h=react.createElement;
 return function ShareCapturePanel(){
  const [url,setURL]=react.useState(''),[body,setBody]=react.useState(''),[capture,setCapture]=react.useState<Record<string,unknown>|null>(null),[busy,setBusy]=react.useState(false),[message,setMessage]=react.useState('');
  const live=react.useRef(true), pending=react.useRef(false), generation=react.useRef(0);
  react.useEffect(()=>{live.current=true;return()=>{live.current=false;generation.current++;};},[]);
  const submit=async()=>{if(pending.current)return;pending.current=true;setBusy(true);setMessage('');const epoch=++generation.current;
   try{const value=capture??{schema:'wuu.share.v1',capture_id:crypto.randomUUID(),kind:url.trim()?'bookmark':'text',...(url.trim()?{url:url.trim()}:{}),...(body.trim()?{text:body}:{})};const normalized=normalizeShareCapture(value);setCapture(value);await invoke('capture.send',normalized);if(live.current&&epoch===generation.current){setMessage('已交给主聊天；请以主聊天中的接收记录为准。');setCapture(null);setURL('');setBody('');}}
   catch{if(live.current&&epoch===generation.current)setMessage('未确认接收。可以用同一份材料重试；不要重新生成副本。');}
   finally{pending.current=false;if(live.current&&epoch===generation.current)setBusy(false);}
  };
  const load=async(event:React.ChangeEvent<HTMLInputElement>)=>{const file=event.target.files?.[0];if(!file)return;const epoch=++generation.current;setMessage('');try{if(file.size>100000)throw Error();const raw=JSON.parse(await file.text());if(raw.attachments!==undefined)throw Error();normalizeShareCapture(raw);if(live.current&&epoch===generation.current){setCapture(raw);setURL(typeof raw.url==='string'?raw.url:'');setBody(typeof raw.text==='string'?raw.text:'');}}catch{if(live.current&&epoch===generation.current)setMessage('文件无效：请选择不超过 100 KB 的 .wuu-share.json。');}finally{event.target.value='';}};
  const edit=()=>{generation.current++;setCapture(null);setMessage('');};
  return h('section',{'data-personal-share':true},h('h3',null,'分享与收藏'),h('p',{className:'personal-share-desc'},'接收主动分享的文字、链接或当前页。'),
   h('div',{className:'personal-share-fields'},
    h('label',null,h('span',null,'链接'),h('input',{className:'plugin-ui-input',type:'url',value:url,disabled:busy,onChange:(e:React.ChangeEvent<HTMLInputElement>)=>{edit();setURL(e.target.value);},placeholder:'https://…'})),
    h('label',null,h('span',null,'文字'),h('textarea',{className:'plugin-ui-input plugin-ui-textarea',value:body,disabled:busy,maxLength:65536,rows:3,onChange:(e:React.ChangeEvent<HTMLTextAreaElement>)=>{edit();setBody(e.target.value);}})),
    h('label',null,h('span',null,'导入浏览器当前页文件'),h('input',{className:'plugin-ui-input',type:'file',accept:'.json,application/json',disabled:busy,onChange:load}))),
   h('div',{className:'personal-share-actions'},h('button',{className:'plugin-ui-button plugin-ui-button-secondary',type:'button',disabled:busy,onClick:()=>{generation.current++;setCapture(null);setURL('');setBody('');setMessage('');}},'清空'),h('button',{className:'plugin-ui-button plugin-ui-button-primary',type:'button',disabled:busy||(!url.trim()&&!body.trim()),onClick:submit},busy?'正在接收…':'保存到主聊天')),
   message&&h('p',{role:'status',className:'personal-share-status'},message));
 };
}
export const shareCaptureStyles='[data-personal-share]{margin-top:var(--space-6);padding-top:var(--space-5);border-top:1px solid var(--hairline-soft)}[data-personal-share] h3{margin:0;color:var(--ink);font-size:var(--font-title,16px);font-weight:var(--weight-semibold,600);letter-spacing:-0.012em}[data-personal-share] .personal-share-desc{margin:4px 0 0;color:var(--ink-tertiary);font-size:var(--font-sm,14px)}[data-personal-share] .personal-share-fields{display:flex;flex-direction:column;margin-top:var(--space-3);border-top:1px solid var(--hairline-soft)}[data-personal-share] label{display:grid;grid-template-columns:minmax(0,180px) minmax(0,1fr);align-items:center;gap:var(--space-4);padding:var(--settings-row-pad-block,12px) 0;border-bottom:1px solid var(--hairline-soft);margin:0;color:var(--ink);font-size:var(--font-ui,15px)}[data-personal-share] label:has(textarea){align-items:start}[data-personal-share] input,[data-personal-share] textarea{font:inherit;width:100%;max-width:100%;box-sizing:border-box}[data-personal-share] .personal-share-actions{display:flex;justify-content:flex-end;flex-wrap:wrap;gap:var(--space-2);margin-top:var(--space-3)}[data-personal-share] .personal-share-status{margin:var(--space-2) 0 0;color:var(--ink-tertiary);font-size:var(--font-sm,14px)}';
