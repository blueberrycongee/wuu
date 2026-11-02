import { createConnectorOAuth, connectorOAuthStyles, type OAuthService } from './connector-oauth.js';
import type * as React from 'react';
import { decodeRulePreview, decodeReviewRules, type ReviewRulePreview, type createPersonalReviewPanel } from './action-review-panel.js';
export type ConnectorService = {rulePreview?(input:{action_id:string;digest:string}):Promise<unknown>;ruleSave?(input:{action_id:string;digest:string;expected_revision:number;decision:'allow'|'ask'}):Promise<unknown>;oauth?:OAuthService;list():Promise<unknown>;actions():Promise<unknown>;decide(input:{action_id:string;digest:string;approve:boolean}):Promise<unknown>;disconnect(input:{connection_id:string}):Promise<unknown>};
type Connection={connection_id:string;provider:string;account_id:string;domain:string;state:string;scopes:string[];resources:string[];capabilities?:Array<{operation:string;write:boolean;available?:boolean;reason?:string}>};
type Action={state:string;action_id:string;digest?:string;summary?:string;connection_id?:string;operation?:string;arguments_json?:string;account_id?:string;code?:string;review_reason?:string};
const providers=[['gmail','Gmail','邮件、线程与附件；草稿和发送'],['notion','Notion','页面、数据库与报告保存'],['drive','Google Drive','已授权文件、Docs 与 PDF'],['github','GitHub','PR、Issue、CI 与 Release'],['calendar','Google Calendar','创建、改期与取消'],['slack','Slack','工作资料：搜索、消息与提及'],['feishu','飞书','工作资料：已授权群消息']] as const;
const states:Record<string,string>={awaiting_confirmation:'等待你的确认',completed:'已完成',failed:'执行失败',rejected:'已拒绝',uncertain:'结果未知，先核对',executing:'正在执行',cancelled:'已取消'};
function object(value:unknown):Record<string,unknown>{if(!value||typeof value!=='object'||Array.isArray(value))throw Error('Invalid connector response');return value as Record<string,unknown>}
function connections(raw:unknown):Connection[]{const rows=object(raw).connections;if(!Array.isArray(rows)||rows.length>100)throw Error('Invalid connector list');return rows.map(row=>{const v=object(row);if(!['connection_id','provider','account_id','domain','state'].every(key=>typeof v[key]==='string')||!Array.isArray(v.resources)||!v.resources.every(x=>typeof x==='string')||!Array.isArray(v.scopes)||!v.scopes.every(x=>typeof x==='string'))throw Error('Invalid connector identity');return structuredClone(v) as unknown as Connection})}
function actions(raw:unknown):Action[]{const rows=object(raw).actions;if(!Array.isArray(rows)||rows.length>1000)throw Error('Invalid connector actions');return rows.map(row=>{const v=object(row);if(typeof v.state!=='string'||typeof v.action_id!=='string')throw Error('Invalid connector action');for(const key of ['digest','summary','connection_id','operation','arguments_json','account_id','code','review_reason'])if(v[key]!==undefined&&typeof v[key]!=='string')throw Error('Invalid connector action');return structuredClone(v) as Action})}
function confirmable(a:Action){if(a.state!=='awaiting_confirmation'||!a.digest||!/^[a-f0-9]{64}$/.test(a.digest)||!a.arguments_json)return false;try{return !!object(JSON.parse(a.arguments_json))}catch{return false}}
function actionContent(a:Action): Array<[string,string]> {
 if(!a.arguments_json)return [];
 let data:Record<string,unknown>;
 try{data=object(JSON.parse(a.arguments_json))}catch{return []}
 const fields:Array<[string,string]>=[];
 const add=(label:string,value:unknown)=>{if(typeof value==='string')fields.push([label,value]);else if(Array.isArray(value)&&value.every(item=>typeof item==='string'))fields.push([label,value.join('、')]);};
 if(a.operation==='gmail.send'||a.operation==='gmail.draft') {
  add('收件人',data.to);add('主题',data.subject);add('正文',data.body);
 }else if(a.operation?.startsWith('calendar.')){
  add('日历',data.calendar_id);add('日程',data.summary);add('开始',data.start);add('结束',data.end);add('时区',data.timezone);add('参与者',data.attendees);
  if(data.send_updates==='all')add('通知','通知所有参与者');else if(data.send_updates==='externalOnly')add('通知','只通知外部参与者');else if(data.send_updates==='none')add('通知','不发送通知');
 }
 return fields;
}
function actionTitle(a:Action):string {
 switch(a.operation){case 'gmail.send':return '发送这封邮件？';case 'gmail.draft':return '保存这封邮件草稿？';case 'calendar.create':return '创建这个日程？';case 'calendar.reschedule':return '调整这个日程？';case 'calendar.cancel':return '取消这个日程？';default:return a.summary||'确认这项操作？'}
}
function capabilitySummary(c:Connection):string {
 const granted=(scope:string)=>c.state==='connected'&&c.scopes.includes(scope);
 const read:Record<string,string[]>= {gmail:['https://www.googleapis.com/auth/gmail.readonly'],'google-calendar':['https://www.googleapis.com/auth/calendar.events.readonly','https://www.googleapis.com/auth/calendar.events'],drive:['https://www.googleapis.com/auth/drive.file'],notion:['read_content'],github:['pull_requests:read','issues:read','checks:read','contents:read'],slack:['channels:history','groups:history'],feishu:['im:message:readonly']};
 const write:Record<string,string[]>={gmail:['https://www.googleapis.com/auth/gmail.send','https://www.googleapis.com/auth/gmail.compose'],'google-calendar':['https://www.googleapis.com/auth/calendar.events'],notion:['insert_content']};
 const reads=(read[c.provider]??[]).filter(granted),writes=(write[c.provider]??[]).filter(granted);
 return `读取：${reads.length?'在所选资源内逐操作校验':'缺少读取授权'}；写入：${writes.length?'已授权能力仍需精确确认':write[c.provider]?'未获写入授权':'本连接器只读'}`;
}
/** Owner-only surface: it never accepts model text as an approval decision. */
export function createConnectorsPanel(react:typeof React,ui:Parameters<typeof createPersonalReviewPanel>[1]) {
 const h=react.createElement,Button=ui.Button,OAuth=createConnectorOAuth(react);
 return function Connectors({service,conversation=false}:{service:ConnectorService;conversation?:boolean}) {
  const [rows,setRows]=react.useState<Connection[]>([]),[saved,setSaved]=react.useState<Action[]>([]),[error,setError]=react.useState(''),[loading,setLoading]=react.useState(true),[busy,setBusy]=react.useState(false),[uncertain,setUncertain]=react.useState(false),[disconnectID,setDisconnectID]=react.useState('');
  const [rulePreview,setRulePreview]=react.useState<{action:Action;value:ReviewRulePreview}>();
  const gate=react.useRef(false),alive=react.useRef(true),generation=react.useRef(0);
  const load=async()=>{const n=++generation.current;setLoading(true);try{const [list,history]=await Promise.all([conversation?Promise.resolve({connections:[]}):service.list(),service.actions()]);if(!alive.current||n!==generation.current)return;setRows(connections(list));setSaved(actions(history));setError('');setUncertain(false)}catch{if(alive.current&&n===generation.current)setError('暂时无法核实连接或动作状态，请刷新。')}finally{if(alive.current&&n===generation.current)setLoading(false)}};
  react.useEffect(()=>{alive.current=true;gate.current=false;setBusy(false);setUncertain(false);setRulePreview(undefined);setSaved([]);setError('');void load();const timer=conversation?setInterval(()=>{if(!gate.current)void load()},5000):undefined;return()=>{alive.current=false;generation.current++;clearInterval(timer)}},[service]);
  const mutate=async(work:()=>Promise<unknown>)=>{
   if(gate.current||uncertain)return;
   gate.current=true;setBusy(true);setError('');const n=++generation.current;
   try{await work();if(alive.current&&n===generation.current){gate.current=false;setBusy(false);await load()}}
   catch{if(alive.current&&n===generation.current){setUncertain(true);setError('未收到可核实回执。请先刷新核对已保存状态，不要重复操作。')}}
   finally{if(alive.current&&n===generation.current){gate.current=false;setBusy(false)}}
  };
  const previewRule=async(action:Action)=>{
   if(gate.current||uncertain||!action.digest||!service.rulePreview)return;
   gate.current=true;setBusy(true);setError('');const n=++generation.current;
   try{const value=decodeRulePreview(await service.rulePreview({action_id:action.action_id,digest:action.digest}));if(alive.current&&n===generation.current)setRulePreview({action:structuredClone(action),value})}
   catch{if(alive.current&&n===generation.current)setError('暂时无法核实规则范围，请刷新后再试。')}
   finally{if(alive.current&&n===generation.current){gate.current=false;setBusy(false)}}
  };
  const saveRule=async()=>{
   if(gate.current||uncertain||!rulePreview||!service.ruleSave)return;
   const {action,value}=rulePreview;const current=saved.find(item=>item.action_id===action.action_id);
   if(!action.digest||!current||current.digest!==action.digest||!confirmable(current))return;
   gate.current=true;setBusy(true);setError('');const n=++generation.current;let ruleSaved=false;
   try{
    decodeReviewRules(await service.ruleSave({action_id:action.action_id,digest:action.digest,expected_revision:value.revision,decision:'allow'}));
    if(!alive.current||n!==generation.current)return;
    ruleSaved=true;setRulePreview(undefined);
    await service.decide({action_id:action.action_id,digest:action.digest,approve:true});
    if(alive.current&&n===generation.current){gate.current=false;setBusy(false);await load();}
   }catch{if(alive.current&&n===generation.current){setUncertain(true);setError(ruleSaved?'规则已保存，但这次操作的结果尚未确认。请刷新核对，不要重复执行。':'未能确认规则是否保存。请刷新核对；这次操作尚未批准。')}}
   finally{if(alive.current&&n===generation.current){gate.current=false;setBusy(false)}}
  };
  const visible=conversation?saved.filter(a=>['awaiting_confirmation','uncertain','executing'].includes(a.state)):saved;
  if(conversation&&!visible.length)return null;
  return h('section',{className:'personal-connectors','data-personal-connectors':conversation?'conversation':'settings'},
   h('div',{className:'personal-connectors-heading'},h('h3',null,conversation?'需要你确认':'账号与资料'),h(Button,{disabled:busy||loading,onClick:()=>void load(),'data-connector-refresh':true} as React.ComponentProps<typeof Button>,loading?'正在核对…':'刷新状态')),
   error?h('p',{role:'alert'},error):null,
   !conversation?h('p',null,'读取仅限已授权范围；外部写入先核对精确内容。工作资料单独隔离，不自动加入个人记忆。'):null,
   !conversation?h('div',{className:'personal-connectors-list'},...providers.map(([provider,title,description])=>{
    const matching=rows.filter(c=>c.provider===provider||provider==='calendar'&&c.provider==='google-calendar'||provider==='drive'&&c.provider==='google-drive');
    return h('div',{key:provider,className:'personal-connector-provider'},h('div',{className:'personal-connector-title'},h('h4',null,title),h('span',null,matching.some(c=>c.state==='connected')?'已连接':'未接通')),h('p',null,description),
     matching.length?matching.map(c=>h('div',{key:c.connection_id},h('p',null,`${c.account_id} · ${c.domain==='work'?'工作隔离域':'个人域'} · ${c.state}`),h('p',null,`范围：${c.resources.join('、')||'尚未选择资源'}`),
      h('p',null,capabilitySummary(c)),h('p',null,'变化比较：调用时比较同一执行会话内的有界页；新任务另建基线，无常驻推送或全量同步'),
      disconnectID===c.connection_id?h('div',{className:'personal-connectors-controls'},h('span',null,'断开后停止此账号后续调用。'),h(Button,{disabled:busy||uncertain,onClick:()=>void mutate(async()=>{const value=await service.disconnect({connection_id:c.connection_id});setDisconnectID('');return value})},'确认断开'),h(Button,{disabled:busy,onClick:()=>setDisconnectID('')},'取消')):h(Button,{disabled:busy||uncertain,onClick:()=>setDisconnectID(c.connection_id)},'断开连接'))):h('p',{className:'personal-connectors-muted'},'需先在宿主安全配置账号和资源范围；尚未进行真实账号验收。'),service.oauth?h('details',null,h('summary',null,matching.length?'管理授权范围':'配置账号授权'),h(OAuth,{provider:provider==='calendar'?'google-calendar':provider,service:service.oauth,onConnected:()=>void load(),existing:matching[0]})):null);
   })):null,
   visible.length?h('div',{className:'personal-connectors-actions'},!conversation?h('h3',null,'动作与回执'):null,...visible.map(a=>h('article',{key:a.action_id,'data-connector-state':a.state,className:'personal-connector-action'},h('div',{className:'personal-connector-title'},h('h4',null,actionTitle(a)),h('span',null,states[a.state]||'待核实状态')),a.account_id?h('p',null,`账号：${a.account_id}`):null,
    actionContent(a).length?h('dl',{className:'personal-connector-fields'},actionContent(a).flatMap(([label,value])=>[h('dt',{key:label+'-label'},label),h('dd',{key:label},value)])):null,
    a.review_reason?h('p',null,a.review_reason):null,
    a.arguments_json?h('details',null,h('summary',null,'技术详情'),h('p',null,a.operation),h('pre',null,a.arguments_json)):null,
    a.state==='uncertain'?h('p',null,'外部服务可能已接受此操作。此处不会重发；先到来源核对结果。'):null,
    a.state==='awaiting_confirmation'?h('div',{className:'personal-connectors-controls'},h(Button,{'data-connector-approve':true,disabled:busy||uncertain||!confirmable(a),onClick:()=>void mutate(()=>service.decide({action_id:a.action_id,digest:a.digest!,approve:true}))} as React.ComponentProps<typeof Button>,'允许这次'),service.rulePreview&&service.ruleSave?h(Button,{'data-connector-remember':true,disabled:busy||uncertain||!confirmable(a),onClick:()=>void previewRule(a)} as React.ComponentProps<typeof Button>,'以后自动处理…'):null,h(Button,{'data-connector-reject':true,disabled:busy||uncertain||!a.digest,onClick:()=>void mutate(()=>service.decide({action_id:a.action_id,digest:a.digest!,approve:false}))} as React.ComponentProps<typeof Button>,'不允许')):null,
    rulePreview?.action.action_id===a.action_id&&a.state==='awaiting_confirmation'?h('section',{className:'personal-review-rule-preview','data-connector-rule-preview':true},h('h4',null,'记住这项许可？'),h('p',null,a.operation==='gmail.send'||a.operation==='gmail.draft'?'以后允许此账号重复完全相同的操作，收件人和内容都必须相同。':'以后允许此账号重复完全相同的操作，目标和内容都必须相同。'),h('p',null,'可在“审批与规则”中修改或移除。'),h('details',null,h('summary',null,'查看许可范围'),h('p',null,rulePreview.value.preview.description),h('pre',null,a.arguments_json)),h('div',{className:'personal-connectors-controls'},h(Button,{'data-connector-save-rule':true,disabled:busy||uncertain||a.digest!==rulePreview.action.digest,onClick:()=>void saveRule()} as React.ComponentProps<typeof Button>,'保存并允许这次'),h(Button,{disabled:busy,onClick:()=>setRulePreview(undefined)},'取消'))):null,
    a.code?h('details',null,h('summary',null,'状态详情'),h('p',null,a.code)):null))):null,
  );
 };
}
export const connectorsStyles=connectorOAuthStyles+`.personal-connectors{font:inherit;margin-block:var(--space-5,20px);}.personal-connectors h3{color:var(--ink);font-size:var(--font-title,16px);font-weight:var(--weight-semibold,600);letter-spacing:-0.012em;margin:0;}.personal-connectors h4{color:var(--ink);font-size:var(--font-ui,15px);font-weight:var(--weight-medium,500);margin:0;}.personal-connectors p{margin:var(--space-1,4px) 0;color:var(--ink-tertiary);font-size:var(--font-sm,14px);overflow-wrap:anywhere;}.personal-connectors-heading,.personal-connector-title{display:flex;align-items:center;justify-content:space-between;gap:var(--space-3,12px);flex-wrap:wrap;}.personal-connector-title span{font-size:var(--font-xs,12px);color:var(--ink-tertiary);padding:1px 8px;border-radius:var(--radius-pill,999px);background:var(--surface-2);}.personal-connectors-list{display:flex;flex-direction:column;margin-block:var(--space-3,12px);border-top:1px solid var(--hairline-soft);}.personal-connector-provider{padding-block:var(--settings-row-pad-block,12px);border-bottom:1px solid var(--hairline-soft);min-width:0;}.personal-connector-provider details{margin-top:var(--space-2);font-size:var(--font-sm,14px);color:var(--ink-soft);}.personal-connector-provider summary{cursor:pointer;color:var(--ink-tertiary);font-weight:var(--weight-medium,500);}.personal-connector-fields{display:grid;grid-template-columns:auto minmax(0,1fr);gap:var(--space-2,8px) var(--space-4,16px);margin-block:var(--space-3,12px);}.personal-connector-fields dt{color:var(--ink-muted);}.personal-connector-fields dd{margin:0;white-space:pre-wrap;overflow-wrap:anywhere;}.personal-connectors-controls{display:flex;gap:var(--space-2,8px);align-items:center;flex-wrap:wrap;margin-top:var(--space-2,8px);}.personal-connectors-muted{color:var(--ink-tertiary);font-size:var(--font-xs,12px)!important;}.personal-connector-action{border-top:1px solid var(--hairline-soft);padding-block:var(--space-3,12px);min-width:0;}.personal-connector-action pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:18em;overflow:auto;font:var(--font-code,13px)/1.6 var(--font-mono,monospace);padding:var(--space-3,12px);background:var(--surface-2);border-radius:var(--radius-sm,6px);}.personal-connectors [role=alert]{color:var(--danger);border-left:2px solid var(--danger);padding-left:var(--space-3,12px);}.personal-connectors button{white-space:normal;}`;
