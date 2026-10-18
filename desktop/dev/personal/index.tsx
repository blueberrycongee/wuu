/** Synthetic runtime fixture around the production Personal contribution. No account or external writes. */
import * as React from "react";
import { createRoot } from "react-dom/client";
import { PluginHost } from "../../src/renderer/plugins/PluginHost";
import { PluginViewContent, WorkbenchController } from "../../src/renderer/plugins/Workbench";
import { MarkdownContent } from "../../src/renderer/RichContent";
import { I18nProvider } from "../../src/renderer/i18n";
import { applyThemePreference } from "../../src/renderer/Theme";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";
// Production bundled extension, not a screenshot-only reimplementation.
// @ts-expect-error Bundled desktop contributions are runtime JavaScript.
import { activate } from "../../../internal/plugin/bundled/personal/desktop.js";
// @ts-expect-error Bundled desktop contributions are runtime JavaScript.
import { activate as activateCalendar } from "../../../internal/plugin/bundled/personal-calendar/desktop.js";
const now = "2026-10-18T10:00:00Z";
const agents = [
  { agent_id:"mosaic",user_space_id:"local",kind:"main",name:"Mosaic",responsibility:"",communication:"简洁、自然，重要的事情说清楚。",main_session_id:"synthetic-main",status:"active",revision:1 },
  { agent_id:"lin",user_space_id:"local",kind:"specialist",name:"小林",responsibility:"旅行安排",communication:"给出清楚的选择，也留一点发现的空间。",main_session_id:"synthetic-travel",status:"active",revision:1 },
];
const tasks = [
  {task_id:"trip",agent_id:"lin",title:"京都的四天，慢慢走",goal:"安排一个不赶路的四天行程，留出下午休息的时间。交通以公共交通为主，雨天也有替代方案。",state:"running",requirements_revision:2,revision:3,updated_at:now},
  {task_id:"dinner",agent_id:"mosaic",title:"周五和朋友吃饭",goal:"在三家安静的餐厅中选一家，确认后再预订。",state:"waiting_user",requirements_revision:1,revision:2,updated_at:now,result:{summary:"找到了三家合适的餐厅，等你选一个更方便的地点。"}},
  {task_id:"reading",agent_id:"mosaic",title:"这个月的阅读清单",goal:"整理最近提到的三本书。",state:"completed",requirements_revision:1,revision:4,updated_at:"2026-10-17T10:00:00Z",result:{summary:"三本书已整理好：先从你最感兴趣的城市生活开始，再读设计和自然。每本都保留了推荐来源。"}},
];
const memories = [
  {id:"m1",revision:2,status:"active",content:"旅行时喜欢留出空白，不把每天排得太满。下午能有一段休息时间更好。",contributor_agent_id:"lin",updated_by_agent_id:"lin",evidence:"explicit",source_at:now,effective_at:now,contributions:[{revision:2,agent_id:"lin",executor_session_id:"synthetic-travel",operation:"revise",source_at:now,effective_at:now,written_at:now}],sources:[{kind:"message",ref:"synthetic:1",occurred_at:now}]},
  {id:"m2",revision:1,status:"active",content:"约朋友吃饭时，更喜欢安静、方便聊天的地方。",contributor_agent_id:"mosaic",updated_by_agent_id:"mosaic",effective_at:now,evidence:"explicit",source_at:now,sources:[{kind:"message",ref:"synthetic:2",occurred_at:now}]},
  {id:"m3",revision:1,status:"active",content:"周末可能更愿意把时间留给户外活动。",contributor_agent_id:"mosaic",updated_by_agent_id:"mosaic",effective_at:now,evidence:"inferred",source_at:now,sources:[{kind:"message",ref:"synthetic:3",occurred_at:now}]},
];
const histories:Record<string,Array<{seq:number;role:string;content:string;at?:string;attachments?:Array<Record<string,unknown>>}>> = {
  mosaic:[{seq:1,role:"user",content:"最近事情有点多，帮我看看还有什么需要我决定？"},{seq:2,role:"assistant",at:now,content:"现在有一件事需要你决定：**周五晚餐的地点**。我已经把选择缩小到三家安静、方便聊天的餐厅，等你挑一个再预订。\n\n京都行程小林还在整理，今天不用操心。这个月的阅读清单也已经准备好了。"},{seq:3,role:"user",content:"好，京都那边记得每天留一点休息时间。"},{seq:4,role:"assistant",at:now,content:"记住了。每天都会留一段空白，下午可以歇一会儿。小林会按这个偏好继续安排。\n\n我们先看周五晚餐吧：你更想在公司附近，还是离家近一些？"}],
  lin:[{seq:1,role:"user",content:"京都四天不用排太满，我想慢慢走。"},{seq:2,role:"assistant",at:now,content:"好，我们把每天留给一小片街区。上午慢慢逛，下午留出休息时间。\n\n我正在确认几处地点的开放时间和雨天备选，整理好后会回到这里告诉你。你可以继续和我聊别的事。"}],
};
const syntheticDigest = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824";
const files = new Map<string, {data:string;name:string;mime:string}>();
histories.mosaic[3].attachments = [{ kind:"artifact",index:0,name:"合成示例.txt",mime_type:"text/plain",size:5,sha256:syntheticDigest }];
files.set(`mosaic:4:${syntheticDigest}`, {data:"aGVsbG8=",name:"合成示例.txt",mime:"text/plain"});
let calendarWriteDemo = false;
let calendarOperation:any = {
  operation_id:"synthetic-calendar-operation",revision:1,status:"proposed",kind:"reschedule",preview_digest:"a".repeat(64),
  user_space_id:"local",agent_id:"mosaic",session_id:"synthetic-main",task_id:"synthetic-lunch",requirements_revision:1,
  account_id:"demo@example.test",calendar_id:"synthetic-calendar",event_id:"synthetic-event",original_event_etag:"synthetic-etag",
  fields:{summary:"合成日程：周五午餐",location:"演示咖啡馆",start:{date_time:"2026-10-23T12:00:00+08:00",timezone:"Asia/Shanghai"},end:{date_time:"2026-10-23T13:00:00+08:00",timezone:"Asia/Shanghai"},attendees:[{email:"friend@example.test",optional:false}],send_updates:"all"},
  original_event:{id:"synthetic-event",etag:"synthetic-etag",fields:{summary:"合成原日程",location:"演示地点",start:{date_time:"2026-10-23T11:00:00+08:00",timezone:"Asia/Shanghai"},end:{date_time:"2026-10-23T12:00:00+08:00",timezone:"Asia/Shanghai"},attendees:[{email:"friend@example.test",optional:false}]}},
  availability:{checked_at:now,calendar_id:"synthetic-calendar",scope:"selected_calendar_only",conflicting_event_ids:[]},notification_notice:"合成演示：确认后会向列出的参与者请求发送更新邀请。",reconciliation_required:false,cancel_requested:false,
};
let offline=false, empty=false;
let settings={revision:1,quiet_hours:{enabled:true,start:"22:00",end:"08:00",timezone:"Asia/Shanghai"},notifications:true,notification_batch_seconds:0};
const host = new PluginHost({react:React,renderConversationContent:({content,linkPolicy})=><MarkdownContent text={content} linkPolicy={linkPolicy}/>,invokeRuntime:async({method,input,userInput})=>{
  if(offline)throw new Error("演示：宿主暂时离线。未发送的内容会保留，请连接后重试。");
  const data=input as Record<string,any> || {};
  switch(method){
    case "agents.list":return {agents:empty?[]:agents,default_agent_id:"mosaic"};
    case "agents.ensure_default":empty=false;agents[0].name=data.name;return {agent:agents[0]};
    case "agents.open":return {agent:agents.find(a=>a.agent_id===data.agent_id),session:{state:data.agent_id==="lin"?"running":"completed"}};
    case "agents.history":return {records:(histories[data.agent_id]||[]).filter(record=>(!data.start_seq||record.seq>=data.start_seq)&&(!data.end_seq||record.seq<=data.end_seq))};
    case "agents.search":{const matches=(histories[data.agent_id]||[]).filter(record=>record.content.includes(data.query)&&(!data.time_min||(record.at&&record.at>=data.time_min))&&(!data.time_max||(record.at&&record.at<data.time_max))).map(record=>({seq:record.seq,role:record.role,excerpt:record.content.slice(0,120),at:record.at}));return {matches,has_more:false};}
    case "agents.proposals":return {proposals:[]};
    case "agents.send":{const list=histories[data.agent_id],seq=list.length+1; const attachments = userInput?.attachments?.map((file,index)=>{files.set(`${data.agent_id}:${seq}:${file.sha256}`,{data:file.data,name:file.filename,mime:file.media_type});return {kind:file.media_type.startsWith("image/")?"image":"file",index,name:file.filename,mime_type:file.media_type,size:atob(file.data).length,sha256:file.sha256};});list.push({seq,role:"user",at:now,content:data.prompt,attachments},{seq:list.length+2,role:"assistant",content:"这是合成演示回复。真实使用时，你会一直和这位助理继续交流。"});return {state:"accepted"};}
    case "agents.attachments.read":{const file=files.get(`${data.agent_id}:${data.seq}:${data.sha256}`);if(!file)throw new Error("Synthetic file missing");const offset=data.offset||0;return {data:file.data.slice(offset,offset+128*1024),offset,total:file.data.length,content_type:file.mime,name:file.name,sha256:data.sha256};}
    case "calendar.status":case "calendar.agenda":return {state:"unconnected",read_only:!calendarWriteDemo,freshness:"empty",events:[]};
    case "calendar.operations.list":return {operations:[calendarOperation]};
    case "calendar.operations.confirm":if(!calendarWriteDemo)throw new Error("Synthetic writer is disabled");calendarOperation={...calendarOperation,status:"uncertain",revision:calendarOperation.revision+1,reconciliation_required:true,error_code:"synthetic_acknowledgement_lost"};return calendarOperation;
    case "calendar.operations.cancel":calendarOperation={...calendarOperation,status:calendarOperation.status==="proposed"?"cancelled":"uncertain",cancel_requested:true,revision:calendarOperation.revision+1};return calendarOperation;
    case "calendar.operations.reconcile":return calendarOperation;
    case "agents.stop_reply":return {cancelled:true};
    case "routines.list":return {routines:[]};
    case "tasks.history":return {records:[{seq:1,role:"assistant",content:"合成过程记录：已检查要求版本，并整理出可供选择的方案。"}]};
    case "tasks.list":return {tasks:empty?[]:tasks};
    case "tasks.stop":{const task=tasks.find(t=>t.task_id===data.task_id)!;task.state="stopped";task.revision++;return {task};}
    case "memory.query":return {records:empty?[]:memories.filter(m=>m.status!=="forgotten")};
    case "memory.correct":{const memory=memories.find(m=>m.id===data.id)!;memory.content=data.content;memory.revision++;return {record:memory};}
    case "memory.forget":{const memory=memories.find(m=>m.id===data.id)!;memory.status="forgotten";return {record:memory};}
    case "agents.update":{const agent=agents.find(a=>a.agent_id===data.agent_id)!;Object.assign(agent,data,{revision:agent.revision+1});return {agent};}
    case "agents.pause":case "agents.resume":{const agent=agents.find(a=>a.agent_id===data.agent_id)!;agent.status=method.endsWith("pause")?"paused":"active";agent.revision++;return {agent};}
    case "settings.get":return {settings};
    case "settings.update":settings={...settings,...data,revision:settings.revision+1};return {settings};
    default:throw new Error(`Unsupported synthetic operation: ${method}`);
  }
}});
const controller=new WorkbenchController(host);
const params=new URLSearchParams(location.search);
applyThemePreference(params.get("theme")==="dark"?"dark":"light");
document.documentElement.style.setProperty("--conversation-message-font-size",`${params.get("font")==="19"?19:15}px`);
document.documentElement.dataset.platform="linux";
function FixtureSurface(){
  const [view,setView]=React.useState("personal");
  return <I18nProvider preferenceStore={{get:()=>"zh-CN",set:async()=>{},subscribe:()=>()=>{}}}><WuuUIRoot><div className="mosaic-fixture"><div className="mosaic-fixture-label"><span>Mosaic · 生产组件 · 合成演示数据</span><div><button onClick={()=>setView(view==="personal"?"calendar":"personal")}>Personal / 日历</button><button onClick={()=>{calendarWriteDemo=!calendarWriteDemo;}}>合成写入开关</button><button onClick={()=>applyThemePreference(document.documentElement.dataset.theme==="dark"?"light":"dark")}>明 / 暗</button><button onClick={()=>document.documentElement.style.setProperty("--conversation-message-font-size",document.documentElement.style.getPropertyValue("--conversation-message-font-size")==="19px"?"15px":"19px")}>15 / 19 px</button><button onClick={()=>{offline=!offline;host.publishHostEvent({type:"fixture"});}}>离线 / 在线</button></div></div><div className="mosaic-fixture-view"><PluginViewContent key={view} controller={controller} pluginId={view==="personal"?"personal":"personal-calendar"} viewTypeId={view==="personal"?"personal.home":"personal-calendar.operations"} region="primary"/></div></div></WuuUIRoot></I18nProvider>;
}
async function start(){
  await host.activateGeneration({pluginId:"personal",generation:"synthetic-ui-fixture",register:activate});
  await host.activateGeneration({pluginId:"personal-calendar",generation:"synthetic-ui-fixture",register:activateCalendar});
  createRoot(document.getElementById("root")!).render(<FixtureSurface/>);
  (window as any).mosaicFixture={host,controller,setOffline:(value:boolean)=>{offline=value;},setEmpty:(value:boolean)=>{empty=value;},state:()=>({agents,tasks,memories}),synthetic:true};
}
start().catch(error=>{document.body.textContent=String(error);console.error(error);});
