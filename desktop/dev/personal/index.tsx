/** Synthetic runtime fixture around the production Personal contribution. No account, native local file reads or external writes. */
import * as React from "react";
import { createRoot } from "react-dom/client";
import type { ClientInputReceipt, MessageRelation } from "../../../packages/plugin-sdk/src/index";
import type { PluginHostOptions } from "../../src/renderer/plugins/PluginHost";
import { PluginConversationMessage, PluginConversationView } from "../../src/renderer/plugins/PluginConversation";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationWorkbenchV1 } from "../../../packages/plugin-sdk/src/ui-contributions";
import { PluginConversationScope } from "../../src/renderer/plugins/PluginConversationScope";
import { ApplicationShellOutlet } from "../../src/renderer/plugins/ApplicationShell";
import { activate as activateApplication } from "../../src/renderer/plugins/DefaultApplicationProvider";
import { activate as activateShell } from "../../src/renderer/plugins/DefaultApplicationShell";
import { activate as activateNavigation } from "../../src/renderer/modules/DefaultNavigationSurface";
import { activate as activateComposer } from "../../src/renderer/modules/DefaultComposerSurface";
import { activate as activateConversation } from "../../src/renderer/modules/DefaultConversationSurface";
import { activate as activatePreview } from "../../src/renderer/modules/DefaultPreviewSurface";
import { activate as activateSettings } from "../../src/renderer/modules/DefaultSettingsSurface";
import { desktopPluginHost as host } from "../../src/renderer/plugins/DesktopPluginRuntime";
import { installFixtureBridge } from "./shell";
import { I18nProvider } from "../../src/renderer/i18n";
import { applyThemePreference } from "../../src/renderer/Theme";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";
// Production bundled extension, not a screenshot-only reimplementation.
// @ts-expect-error Bundled desktop contributions are runtime JavaScript.
import { activate } from "../../../internal/plugin/bundled/personal/desktop.js";
// @ts-expect-error Bundled desktop contributions are runtime JavaScript.
import { activate as activateCalendar } from "../../../internal/plugin/bundled/personal-calendar/desktop.js";
let fixtureApplication: ApplicationServiceV1;
let controller: ApplicationWorkbenchV1;
const fixtureLifetime = new AbortController();
const surfaceBindings = Object.fromEntries(["navigation", "composer", "conversation", "preview", "settings"].map(name => [`ui.${name}`, {pluginId: `wuu.default-${name}`, generation: "fixture", id: `ui.${name}`} ]));
const now = "2026-10-18T10:00:00Z";
// Explicit opt-in fixture for the registered Personal file action gate. All
// changes below are in memory; this preview cannot access files or model APIs.
const reviewScenario = new URLSearchParams(location.search).get("review") || "unavailable";
const reviewSnapshot = {
  schema:"personal-review.v1",kind:"ready",
  settings:{auto_review_enabled:false,revision:1,policy_version:"personal-file-actions.v1",scope:["write_file","edit_file"]},
  requests: reviewScenario === "empty" ? [] : [{
    revision:2,status:"awaiting_confirmation",outcome:"ask",reason:"内容已整理好；保存到这个文件还需要你确认。",
    action:{id:"synthetic-file-1",digest:"a".repeat(64),operation:"write_file",scope:{space_id:"local",session_id:"synthetic-worker",task_id:"reading",requirements_revision:1},effect:"data_write",summary:{title:"保存整理好的阅读清单",account:"当前 Personal 文件空间",target:reviewScenario === "long" ? "notes/"+"本周阅读材料与整理要点-".repeat(10)+".md" : "notes/weekly-reading.md",consequence:"创建或替换此文件；不会发信、共享文件或访问其他账号。"},arguments_json:'{"content":"# 本周阅读清单\\n这是一份合成预览内容。","path":"notes/weekly-reading.md"}',expires_at:reviewScenario === "expired" ? "2000-01-01T00:00:00Z" : new Date(Date.now()+9*60_000).toISOString()},
    receipt_json:undefined as string | undefined,
  }],
};
// Omit absent optional fields, matching the production JSON transport.
const reviewValue = () => JSON.parse(JSON.stringify(reviewSnapshot));
const reviewCalls: Array<{method:string;input:unknown}> = [];
Object.assign(window,{__reviewFixture:{snapshot:reviewSnapshot,calls:reviewCalls}});
if (["blocked","stale","denied","revoked","uncertain","succeeded"].includes(reviewScenario)) {
  reviewSnapshot.requests[0].status=reviewScenario;
  reviewSnapshot.requests[0].reason=reviewScenario === "uncertain" ? "结果未知；先核对，绝不会自动重放。" : "合成的已保存动作状态。";
  if (reviewScenario === "succeeded") reviewSnapshot.requests[0].receipt_json='{"receipt_id":"synthetic-file-receipt","path":"notes/weekly-reading.md","verified":true}';
}

// Synthetic connector owner boundary; does not touch accounts or provider APIs.
const connectorScenario = new URLSearchParams(location.search).get("connectors") || "empty";
const connectorActions: Array<Record<string,unknown>> = connectorScenario === "empty" ? [] : [{state:connectorScenario === "uncertain" ? "uncertain" : "awaiting_confirmation",action_id:"connector-synthetic",digest:"b".repeat(64),connection_id:"synthetic-gmail",account_id:"demo@example.invalid",operation:"gmail.send",summary:"发送一封合成示例邮件",arguments_json:JSON.stringify({to:["reader@example.invalid"],subject:"合成报告",body:"这是一份供界面验收的合成内容，不会发送真实邮件。"})}];
const connectorCalls:Array<{method:string;input:unknown}>=[];
Object.assign(window,{__connectorFixture:{actions:connectorActions,calls:connectorCalls}});
const sendRecoveryFixture = { accepted: 0, refreshFailures: 0, failNextRefresh: false, requests: [] as unknown[] };
Object.assign(window, { __sendRecoveryFixture: sendRecoveryFixture });
const agents = [
  { agent_id:"mosaic",user_space_id:"local",kind:"main",name:"twin",responsibility:"",communication:"简洁、自然，重要的事情说清楚。",main_session_id:"synthetic-main",status:"active",revision:1 },
];
const savedSyntheticName = localStorage.getItem("wuu.fixture.personal-name");
if (savedSyntheticName?.trim() && Array.from(savedSyntheticName.trim()).length <= 64) agents[0].name = savedSyntheticName.trim();
const tasks = [
  {task_id:"trip",agent_id:"lin",title:"京都的四天，慢慢走",goal:"安排一个不赶路的四天行程，留出下午休息的时间。交通以公共交通为主，雨天也有替代方案。",state:"running",requirements_revision:2,revision:3,updated_at:now},
  {task_id:"dinner",agent_id:"mosaic",title:"周五和朋友吃饭",goal:"在三家安静的餐厅中选一家，确认后再预订。",state:"waiting_user",requirements_revision:1,revision:2,updated_at:now,result:{summary:"找到了三家合适的餐厅，等你选一个更方便的地点。"}},
  {task_id:"reading",agent_id:"mosaic",title:"这个月的阅读清单",goal:"整理最近提到的三本书。",state:"completed",requirements_revision:1,revision:4,updated_at:"2026-10-17T10:00:00Z",result:{summary:"三本书已整理好：先从你最感兴趣的城市生活开始，再读设计和自然。每本都保留了推荐来源。"}},
];
// Synthetic responsibilities never contact a scheduler or run external work.
const routineFixtureParams = new URLSearchParams(location.search);
const routineFixtureFaults = {
  history: routineFixtureParams.get("routineHistory") || "ready",
  save: routineFixtureParams.get("routineSave") || "ready",
  delay: Math.min(10000, Math.max(0, Number(routineFixtureParams.get("routineDelay") || 0))),
};
const fixtureRoutines: Array<Record<string, any>> = [{
  routine_id:"weekly", agent_id:"mosaic", title:"每周整理阅读清单", kind:"task", status:"active",
  prompt:"整理这一周提到的书、文章和播客。\n\n先按主题归类，再挑出最值得读的三篇，保留原始链接与推荐理由。已有条目不要重复加入；没有新内容时保持安静。\n\n输出使用简洁的中文。只整理示例内容，不发送消息，不触发真实任务。",
  revision:1, config_revision:1, cron:"0 9 * * 1", timezone:"Asia/Shanghai", missed_policy:"run_once",
  created_at:"2026-10-01T01:00:00Z", updated_at:now,
  binding:{binding_id:"synthetic-weekly", status:"active", next_run_at:"2026-10-26T01:00:00Z"},
}, {
  routine_id:"daily", agent_id:"mosaic", title:"每日灵感速览", kind:"task", status:"paused",
  prompt:"整理设计与技术领域的新灵感。只使用合成示例，不访问真实来源。", revision:1, config_revision:1,
  cron:"0 10 * * *", timezone:"Asia/Shanghai", missed_policy:"skip", updated_at:now,
  binding:{binding_id:"synthetic-daily",status:"paused"},
}];
if (routineFixtureParams.get("routineStopped") === "1") fixtureRoutines[0].status = "stopped";
const routineFixtureControl = { faults:routineFixtureFaults, routines:fixtureRoutines, calls:[] as Array<{method:string,input:unknown}> };
Object.assign(window, { __routineFixture: routineFixtureControl });
const fixtureRoutineDelay = async () => { if (routineFixtureFaults.delay) await new Promise(resolve => setTimeout(resolve,routineFixtureFaults.delay)); };
const routineTaskRecords=new Map<string,Record<string,any>>();
function fixtureRoutineTasks(routineID:string) {
  return ["completed","running","failed","waiting_user","stopped"].map((state,index)=>{
    const task_id=`synthetic-routine-${routineID}-${index}`;
    if(!routineTaskRecords.has(task_id)) routineTaskRecords.set(task_id,{
      task_id,routine_id:routineID,agent_id:"mosaic",title:["本周阅读整理","正在整理新条目","来源暂时无法读取","等待选择阅读主题","已停止的整理"][index],
      goal:"合成运行记录：不执行实际后台任务。",state,requirements_revision:1,revision:1,
      created_at:`2026-10-${String(20-index).padStart(2,"0")}T01:00:00Z`, updated_at:`2026-10-${String(20-index).padStart(2,"0")}T01:06:00Z`,
      ...(state==="completed"?{completed_at:"2026-10-20T01:06:00Z",result:{summary:"合成示例：整理了三篇文章，没有发送外部消息。"}}:{}),
      ...(state==="failed"?{error:"合成来源读取失败"}:{}),
    });
    return structuredClone(routineTaskRecords.get(task_id));
  });
}
const memories = [
  {id:"m1",revision:2,status:"active",content:"旅行时喜欢留出空白，不把每天排得太满。下午能有一段休息时间更好。",contributor_agent_id:"lin",updated_by_agent_id:"lin",evidence:"explicit",source_at:now,effective_at:now,contributions:[{revision:2,agent_id:"lin",executor_session_id:"synthetic-travel",operation:"revise",source_at:now,effective_at:now,written_at:now}],sources:[{kind:"message",ref:"synthetic:1",occurred_at:now}]},
  {id:"m2",revision:1,status:"active",content:"约朋友吃饭时，更喜欢安静、方便聊天的地方。",contributor_agent_id:"mosaic",updated_by_agent_id:"mosaic",effective_at:now,evidence:"explicit",source_at:now,sources:[{kind:"message",ref:"synthetic:2",occurred_at:now}]},
  {id:"m3",revision:1,status:"active",content:"周末可能更愿意把时间留给户外活动。",contributor_agent_id:"mosaic",updated_by_agent_id:"mosaic",effective_at:now,evidence:"inferred",source_at:now,sources:[{kind:"message",ref:"synthetic:3",occurred_at:now}]},
];
const histories:Record<string,Array<{seq:number;role:string;content:string;receipt?:ClientInputReceipt;at?:string;reactions?:{owner?:string;agent?:string;revision:number};relation?:{reply_to?:{session_id:string;seq:number}};attachments?:Array<Record<string,unknown>>}>> = {
  mosaic:[{seq:1,role:"user",content:"最近事情有点多，帮我看看还有什么需要我决定？"},{seq:2,role:"assistant",at:now,content:"现在有一件事需要你决定：**周五晚餐的地点**。我已经把选择缩小到三家安静、方便聊天的餐厅，等你挑一个再预订。\n\n京都行程还在整理，今天不用操心。这个月的阅读清单也已经准备好了。"},{seq:3,role:"user",content:"好，京都那边记得每天留一点休息时间。"},{seq:4,role:"assistant",at:now,content:"记住了。每天都会留一段空白，下午可以歇一会儿。我会按这个偏好继续安排。\n\n我们先看周五晚餐吧：你更想在公司附近，还是离家近一些？"}],
};
for (const history of Object.values(histories)) for (const record of history) record.at ??= now;
const receiptRows = new Map<string, {receipt: ClientInputReceipt; prompt:string; relation?:MessageRelation; attachments?:Array<{filename:string;media_type:string;sha256:string}>}>();
const fixtureReceipt = (request:string,state:ClientInputReceipt["execution_state"],seq?:number):ClientInputReceipt => ({request_id:request,receipt_id:`receipt-${request}`,host_request_id:`host-${request}`,client_id:`client-${request}`,queue_id:`queue-${request}`,session_id:"synthetic-main",plugin_id:"personal",owner:"plugin:personal",user_space_id:"local",product_domain:"personal",agent_id:"mosaic",receipt_state:seq?"in_history":"accepted",execution_state:state,accepted_at:now,revision:seq?4:2,...(seq?{message_seq:seq,hostturn_id:`turn-${request}`}:{})});
if(new URLSearchParams(location.search).has("receipts")) {
 histories.mosaic = [
 {seq:1,role:"user",content:"这条已收到，处理已经完成。",receipt:fixtureReceipt("done","completed",1)},
 {seq:2,role:"assistant",content:"好的，已经整理完成。"},
 {seq:3,role:"user",content:"这条已收到，但处理遇到了错误。",receipt:fixtureReceipt("failed","failed",3)},
 {seq:4,role:"user",content:"宿主重启前已开始处理；结果未知，不会自动重放。",receipt:fixtureReceipt("unknown","unconfirmed",4)}
 ];
 receiptRows.set("held",{receipt:fixtureReceipt("held","held"),prompt:"附件已经保存在宿主，等我明确继续。",attachments:[{filename:"合成附件.txt",media_type:"text/plain",sha256:"a".repeat(64)}]});
}
if (new URLSearchParams(location.search).has("replyReceipts") && receiptRows.has("held")) {
  receiptRows.get("held")!.relation = {reply_to:{session_id:"synthetic-main",seq:2}};
  receiptRows.set("main-only",{receipt:fixtureReceipt("main-only","held"),prompt:"仅主会话的 pending，不应出现在 Reply。"});
}
const scenarioLabels = {
  default: "已送达结果 / Held", sending: "发送中（手动释放 ACK）", processing: "处理中",
  "reject-before-accept": "接收前明确拒绝", "ack-unknown": "已接收但 ACK 未知", "retry-same-id": "未接收的网络失败 / 同 ID 重试",
};
type SendScenario = keyof typeof scenarioLabels;
const requestedScenario = new URLSearchParams(location.search).get("sendScenario") || "default";
const receiptScenario = {
  mode: (requestedScenario in scenarioLabels ? requestedScenario : "default") as SendScenario,
  attempts: [] as Array<{request_id:string}>, accepted: 0, hidden: new Set<string>(),
  release: undefined as (() => void) | undefined,
};
Object.assign(window, { __receiptScenario: receiptScenario });
const refreshScenarioControls = () => window.dispatchEvent(new Event("receipt-scenario-change"));
async function sendReceiptScenario(data:Record<string,any>, userInput?:{attachments?:ReadonlyArray<{filename:string;media_type:string;sha256:string}>}) {
  receiptScenario.attempts.push(structuredClone(data) as {request_id:string});
  refreshScenarioControls();
  if (receiptScenario.mode === "reject-before-accept") throw new Error("request_id_conflict: synthetic rejection before acceptance");
  if (receiptScenario.mode === "retry-same-id" && receiptScenario.attempts.length === 1) throw new Error("合成网络故障：送达状态暂时无法确认");
  if (receiptScenario.mode === "sending") await new Promise<void>(resolve => {receiptScenario.release=resolve;refreshScenarioControls();});
  const prior=receiptRows.get(data.request_id);
  if (prior) return {receipt:prior.receipt,state:prior.receipt.execution_state};
  const list=histories[data.agent_id], seq=(list.at(-1)?.seq ?? 0)+1;
  const processing=receiptScenario.mode === "processing";
  const receipt=fixtureReceipt(data.request_id,processing?"running":"queued",processing?seq:undefined);
  receiptRows.set(data.request_id,{receipt,prompt:data.prompt,relation:data.relation,attachments:userInput?.attachments?.map(({filename,media_type,sha256})=>({filename,media_type,sha256}))});
  if(processing) list.push({seq,role:"user",at:now,content:data.prompt,receipt,relation:data.relation});
  receiptScenario.accepted++;
  if(receiptScenario.mode === "ack-unknown") receiptScenario.hidden.add(data.request_id);
  refreshScenarioControls();
  if(receiptScenario.mode === "ack-unknown") throw new Error("合成 ACK 丢失：状态查询暂不可用");
  return {receipt,state:receipt.execution_state};
}
function ReceiptScenarioControls() {
  const [, redraw]=React.useState(0);
  React.useEffect(()=>{const update=()=>redraw(n=>n+1);window.addEventListener("receipt-scenario-change",update);return()=>window.removeEventListener("receipt-scenario-change",update);},[]);
  return <div style={{display:"flex",flexWrap:"wrap",gap:8,alignItems:"center",padding:"8px 12px"}}>
    <label>合成发送场景：<select data-receipt-scenario value={receiptScenario.mode} onChange={e=>{const url=new URL(location.href);url.searchParams.set("receipts","1");url.searchParams.set("sendScenario",e.target.value);location.href=url.href;}}>{Object.entries(scenarioLabels).map(([value,label])=><option key={value} value={value}>{label}</option>)}</select></label>
    <span>在输入框发送一条消息。请求 {receiptScenario.attempts.length} 次 / 接收 {receiptScenario.accepted} 条。</span>
    {receiptScenario.mode === "sending" && <button data-release-synthetic-send disabled={!receiptScenario.release} onClick={()=>{receiptScenario.release?.();receiptScenario.release=undefined;refreshScenarioControls();}}>释放合成 ACK</button>}
    {receiptScenario.mode === "ack-unknown" && <button data-reveal-synthetic-receipt disabled={!receiptScenario.hidden.size} onClick={()=>{receiptScenario.hidden.clear();refreshScenarioControls();}}>恢复状态查询，再点击发送重试</button>}
    <span>历史“处理结果待确认”表示已送达；发送“送达状态待确认”尚未获得回执。</span>
  </div>;
}
const syntheticDigest = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824";
const files = new Map<string, {data:string;name:string;mime:string}>();
histories.mosaic[3].attachments = [{ kind:"artifact",index:0,name:"合成示例.txt",mime_type:"text/plain",size:5,sha256:syntheticDigest }];
files.set(`mosaic:4:${syntheticDigest}`, {data:"aGVsbG8=",name:"合成示例.txt",mime:"text/plain"});
// Disclosed byte fixtures exercise real verified transport and internal file tabs.
histories.mosaic[3].attachments!.push({"kind": "artifact", "index": 1, "name": "合成说明.md", "mime_type": "text/markdown", "size": 98, "sha256": "0447ade207af639c000604ac65231097ab74411a3cbe4c0d8e2e1ffc9932805a"});
files.set("mosaic:4:0447ade207af639c000604ac65231097ab74411a3cbe4c0d8e2e1ffc9932805a", {"data": "IyDlkIjmiJDmlofku7bpooTop4gKCui/meWPquaYr+engeaciemihOiniOmhteWGhee9rueahOaVsOaNru+8jOS4jeS8muivu+WPluS9oOeahOacrOWcsOaWh+S7tuOAggo=", "name": "合成说明.md", "mime": "text/markdown"});
histories.mosaic[3].attachments!.push({"kind": "artifact", "index": 2, "name": "合成网页.html", "mime_type": "text/html", "size": 139, "sha256": "b9f6336d9ce78ecd2fd24ceabd5c587b8d67b1c5f6926d248ff566df13338428"});
files.set("mosaic:4:b9f6336d9ce78ecd2fd24ceabd5c587b8d67b1c5f6926d248ff566df13338428", {"data": "PCFkb2N0eXBlIGh0bWw+PGgxPuWQiOaIkCBIVE1MPC9oMT48c2NyaXB0PndpbmRvdy5fX3Vuc2FmZUF0dGFjaG1lbnRFeGVjdXRlZCA9IHRydWU8L3NjcmlwdD4KPHA+6ISa5pys5L2c5Li65rqQ56CB5pi+56S677yM5LiN5omn6KGM44CCPC9wPg==", "name": "合成网页.html", "mime": "text/html"});
histories.mosaic[3].attachments!.push({"kind": "artifact", "index": 3, "name": "合成归档.zip", "mime_type": "application/zip", "size": 46, "sha256": "aba16615ae4487d98eddf1d76c7e4c08dea0c71bc99009c3a543f5090ad00586"});
files.set("mosaic:4:aba16615ae4487d98eddf1d76c7e4c08dea0c71bc99009c3a543f5090ad00586", {"data": "UEsDBFN5bnRoZXRpYyBhcmNoaXZlIGZpeHR1cmU7IG5vdCBhIHJlYWwgWklQLg==", "name": "合成归档.zip", "mime": "application/zip"});
histories.mosaic[3].attachments!.push({"kind": "artifact", "index": 4, "name": "合成读取失败.txt", "mime_type": "text/plain", "size": 13, "sha256": "72e3ff58d7416ac8436ed00564dda7b4f6d81b5f54ac903a555f3a234c74a9b9"});
files.set("mosaic:4:72e3ff58d7416ac8436ed00564dda7b4f6d81b5f54ac903a555f3a234c74a9b9", {"data": "ZXJyb3IgZml4dHVyZQ==", "name": "合成读取失败.txt", "mime": "text/plain"});
let calendarWriteDemo = false;
let calendarOperation:any = {
  operation_id:"synthetic-calendar-operation",revision:1,status:"proposed",kind:"reschedule",preview_digest:"a".repeat(64),
  user_space_id:"local",agent_id:"mosaic",session_id:"synthetic-main",task_id:"synthetic-lunch",requirements_revision:1,
  account_id:"demo@example.test",calendar_id:"synthetic-calendar",event_id:"synthetic-event",original_event_etag:"synthetic-etag",
  fields:{summary:"合成日程：周五午餐",location:"演示咖啡馆",start:{date_time:"2026-10-23T12:00:00+08:00",timezone:"Asia/Shanghai"},end:{date_time:"2026-10-23T13:00:00+08:00",timezone:"Asia/Shanghai"},attendees:[{email:"friend@example.test",optional:false}],send_updates:"all"},
  original_event:{id:"synthetic-event",etag:"synthetic-etag",fields:{summary:"合成原日程",location:"演示地点",start:{date_time:"2026-10-23T11:00:00+08:00",timezone:"Asia/Shanghai"},end:{date_time:"2026-10-23T12:00:00+08:00",timezone:"Asia/Shanghai"},attendees:[{email:"friend@example.test",optional:false}]}},
  availability:{checked_at:now,calendar_id:"synthetic-calendar",scope:"selected_calendar_only",conflicting_event_ids:[]},notification_notice:"合成演示：确认后会向列出的参与者请求发送更新邀请。",reconciliation_required:false,cancel_requested:false,
};
const reactionStore: Record<string, {owner?:string;agent?:string;revision:number}> = JSON.parse(localStorage.getItem("wuu.fixture.reactions") || "{}");
const reactionReceipts = new Map<string, unknown>();
const reactionsFor = (seq: number) => reactionStore[String(seq)] || { revision: 0 };
const persistReactions = () => localStorage.setItem("wuu.fixture.reactions", JSON.stringify(reactionStore));
let offline=false, empty=false;
let calendarReads=0;
const calendarFixture=new URLSearchParams(location.search).get("calendar")==="stale";
let fixtureActivity: "idle" | "thinking" | "responding" = "idle";
let settings={revision:1,quiet_hours:{enabled:true,start:"22:00",end:"08:00",timezone:"Asia/Shanghai"},notifications:true,notification_batch_seconds:0};
const invokeRuntime: NonNullable<PluginHostOptions["invokeRuntime"]> = async({method,input,userInput})=>{
  if(offline)throw new Error("演示：宿主暂时离线。未发送的内容会保留，请连接后重试。");
  const data=input as Record<string,any> || {};
  switch(method){
    case "agents.list":return {agents:empty?[]:agents,default_agent_id:"mosaic"};
    case "agents.ensure_default":empty=false;return {agent:agents[0]};
    case "agents.update": {
      const agent = agents.find(value => value.agent_id === data.agent_id);
      if (!agent || agent.revision !== data.expected_revision) throw new Error("revision conflict");
      const name = typeof data.name === "string" ? data.name.trim() : "";
      if (!name || Array.from(name).length > 64) throw new Error("name must contain 1–64 characters");
      localStorage.setItem("wuu.fixture.personal-name", name);
      agent.name = name; agent.revision++; return { agent: { ...agent } };
    }
    case "agents.open":return {agent:agents.find(a=>a.agent_id===data.agent_id),session:{state:fixtureActivity === "idle" ? "completed" : "running"},pending_inputs:[...receiptRows.values()].filter(r=>r.receipt.receipt_state==="accepted"&&r.receipt.execution_state!=="cancelled"&&!receiptScenario.hidden.has(r.receipt.request_id))};
    case "agents.messages.status": return {receipt:receiptScenario.hidden.has(data.request_id)?undefined:receiptRows.get(data.request_id)?.receipt};
    case "agents.messages.resume": {const row=receiptRows.get(data.request_id);if(!row||row.receipt.receipt_id!==data.receipt_id||userInput?.resumeReceiptId!==data.receipt_id)throw new Error("Synthetic receipt proof mismatch");row.receipt={...row.receipt,execution_state:"queued",revision:row.receipt.revision+1};return {receipt:row.receipt};}
    case "agents.messages.cancel": {const row=receiptRows.get(data.request_id);if(!row||row.receipt.receipt_id!==data.receipt_id)throw new Error("Synthetic identity mismatch");row.receipt={...row.receipt,execution_state:"cancelled",revision:row.receipt.revision+1};return {cancelled:true};}
    case "agents.history": {
      if (data.reply_seq === undefined && sendRecoveryFixture.failNextRefresh) {
        sendRecoveryFixture.failNextRefresh = false; sendRecoveryFixture.refreshFailures++;
        throw new Error("合成记录刷新失败：发送已被接收，可安全刷新记录。");
      }
      const all = (histories[data.agent_id] || []).map(record => ({ ...record, reactions: reactionsFor(record.seq), reaction_eligible:true }));
      if (data.reply_seq !== undefined) {
        const roots = new Map<number, number>();
        for (const row of all) roots.set(row.seq, roots.get(row.relation?.reply_to?.seq ?? 0) ?? row.seq);
        const root = roots.get(data.reply_seq);
        const matching = all.filter(row => roots.get(row.seq) === root && (!data.end_seq || row.seq <= data.end_seq));
        const records = matching.slice(-10);
        return { records, reply_root_seq: root, first_seq: records[0]?.seq, has_earlier: matching.length > records.length };
      }
      return {records:all.filter(record=>(!data.start_seq||record.seq>=data.start_seq)&&(!data.end_seq||record.seq<=data.end_seq)).slice(params.get("replies") === "1" ? -2 : 0)};
    }
    case "agents.reactions": return { session_id: "synthetic-main", records: data.message_seqs.map((seq:number) => ({seq, reactions: reactionsFor(seq)})) };
    case "agents.react": {
      if (reactionReceipts.has(data.request_id)) return reactionReceipts.get(data.request_id);
      const record = histories[data.agent_id]?.find(record => record.seq === data.message_seq);
      if (data.session_id !== "synthetic-main" || record?.role !== "assistant") throw new Error("invalid reaction target");
      const current = reactionsFor(data.message_seq);
      if (data.expected_revision !== current.revision) throw new Error("reaction revision conflict");
      reactionStore[String(data.message_seq)] = {...current, owner: data.emoji ?? undefined, revision: current.revision + 1}; persistReactions();
      const result = {session_id:"synthetic-main", message_seq:data.message_seq, reactions:reactionsFor(data.message_seq)};
      reactionReceipts.set(data.request_id, result); return result;
    }
    case "agents.search":{const matches=(histories[data.agent_id]||[]).filter(record=>record.content.includes(data.query)&&(!data.time_min||(record.at&&record.at>=data.time_min))&&(!data.time_max||(record.at&&record.at<data.time_max))).map(record=>({seq:record.seq,role:record.role,excerpt:record.content.slice(0,120),at:record.at}));return {matches,has_more:false};}
    case "agents.proposals":return {proposals:[]};
    case "agents.send":{if(receiptScenario.mode!=="default")return sendReceiptScenario(data,userInput);const prior=receiptRows.get(data.request_id);if(prior)return {receipt:prior.receipt,state:prior.receipt.execution_state};sendRecoveryFixture.requests.push(structuredClone(data)); const list=histories[data.agent_id],seq=(list.at(-1)?.seq ?? 0)+1; const attachments = userInput?.attachments?.map((file,index)=>{files.set(`${data.agent_id}:${seq}:${file.sha256}`,{data:file.data,name:file.filename,mime:file.media_type});return {kind:file.media_type.startsWith("image/")?"image":"file",index,name:file.filename,mime_type:file.media_type,size:atob(file.data).length,sha256:file.sha256};});list.push({seq,role:"user",at:now,content:data.prompt,attachments,relation:data.relation},{seq:seq+1,role:"assistant",at:now,content:"这是合成演示回复。主对话保持不变，后台事项仍会继续。",relation:data.relation});sendRecoveryFixture.accepted++; const delay = Math.min(10000, Math.max(0, Number(params.get("sendDelay")) || 0)); if (delay) await new Promise(resolve => setTimeout(resolve, delay)); sendRecoveryFixture.failNextRefresh = params.get("sendRefresh") === "error"; const receipt=fixtureReceipt(data.request_id,"completed",seq);list.find(row=>row.seq===seq)!.receipt=receipt;receiptRows.set(data.request_id,{receipt,prompt:data.prompt,relation:data.relation});return {state:"completed",receipt};}
    case "agents.attachments.read":{const file=files.get(`${data.agent_id}:${data.seq}:${data.sha256}`);if(!file)throw new Error("Synthetic file missing");if(file.name === "合成读取失败.txt")throw new Error("合成读取失败：用于展示错误与重试，不涉及真实文件。");const offset=data.offset||0;return {data:file.data.slice(offset,offset+128*1024),offset,total:file.data.length,content_type:file.mime,name:file.name,sha256:data.sha256};}
    case "calendar.status":case "calendar.agenda":return {state:"unconnected",read_only:!calendarWriteDemo,freshness:"empty",events:[]};
    case "calendar.operations.list":return {operations:calendarWriteDemo?[calendarOperation]:[]};
    case "calendar.operations.confirm":if(!calendarWriteDemo)throw new Error("Synthetic writer is disabled");calendarOperation={...calendarOperation,status:"uncertain",revision:calendarOperation.revision+1,reconciliation_required:true,error_code:"synthetic_acknowledgement_lost"};return calendarOperation;
    case "calendar.operations.cancel":calendarOperation={...calendarOperation,status:calendarOperation.status==="proposed"?"cancelled":"uncertain",cancel_requested:true,revision:calendarOperation.revision+1};return calendarOperation;
    case "calendar.operations.reconcile":return calendarOperation;
    case "agents.stop_reply":return {cancelled:true};
    case "routines.list":return {routines:empty?[]:structuredClone(fixtureRoutines)};
    case "routines.history": {
      routineFixtureControl.calls.push({method,input:structuredClone(data)});
      await fixtureRoutineDelay();
      if(routineFixtureFaults.history === "pending") await new Promise(() => {});
      if(routineFixtureFaults.history === "error") throw new Error("合成演示：运行记录暂时无法读取，请重试。");
      if(routineFixtureFaults.history === "empty") return {routine_id:data.routine_id,events:[],tasks:[]};
      const allTasks=fixtureRoutineTasks(data.routine_id);
      const paged=routineFixtureFaults.history === "paged";
      return {routine_id:data.routine_id,events:data.cursor?[]:[
        {event_id:"synthetic-dispatched",routine_id:data.routine_id,routine_revision:1,kind:"task",status:"accepted",state:"dispatched",created_at:"2026-10-22T01:00:00Z",request_id:"synthetic-dispatched"},
        {event_id:"synthetic-pending",routine_id:data.routine_id,routine_revision:1,kind:"task",status:"pending",state:"pending",created_at:"2026-10-21T01:00:00Z",request_id:"synthetic-pending"},
      ],tasks:paged?(data.cursor?allTasks.slice(2):allTasks.slice(0,2)):allTasks,
      has_more:paged&&!data.cursor, cursor:paged&&!data.cursor?"synthetic-page-2":undefined, order:"storage_scan",skipped_records:0};
    }

    case "routines.update":case "routines.pause":case "routines.resume": {
      routineFixtureControl.calls.push({method,input:structuredClone(data)});
      await fixtureRoutineDelay();
      if(routineFixtureFaults.save === "pending") await new Promise(() => {});
      if(routineFixtureFaults.save === "error") throw new Error("合成演示：保存失败，请重试。没有修改真实任务。");
      const routine=fixtureRoutines.find(value=>value.routine_id===data.routine_id);
      if(!routine) throw new Error("routine not found");
      if(routineFixtureFaults.save === "conflict") { routineFixtureFaults.save="ready"; routine.revision++; throw new Error("revision conflict"); }
      if(routine.revision!==data.expected_revision) throw new Error("revision conflict");
      if(method === "routines.update") {
        for(const key of ["title","prompt","cron","run_at","timezone","kind","missed_policy"]) if(key in data) routine[key]=data[key];
        if(data.cron) delete routine.run_at;
        if(data.run_at) delete routine.cron;
        routine.config_revision++;
      } else { routine.status=method.endsWith("pause")?"paused":"active"; routine.binding.status=routine.status; }
      routine.revision++; routine.updated_at=new Date().toISOString();
      return {routine:structuredClone(routine)};
    }
    case "tasks.history":return {requirements_revision:1,records:[{seq:1,role:"assistant",content: data.task_id === "reading" ? "阅读清单已整理好。" : "合成过程记录：已检查要求版本，并整理出可供选择的方案。",...(data.task_id === "reading"?{attachments:[{kind:"artifact",index:0,name:"合成示例.txt",mime_type:"text/plain",size:5,sha256:syntheticDigest}]}:{})}]};
    case "tasks.attachments.read":return {data:"aGVsbG8=",offset:0,total:8,content_type:"text/plain",name:"合成示例.txt",sha256:syntheticDigest};
    case "tasks.get":return {task:tasks.find(task=>task.task_id===data.task_id)||routineTaskRecords.get(data.task_id)};
    case "tasks.reconcile":return {task:tasks.find(task=>task.task_id===data.task_id)||routineTaskRecords.get(data.task_id)};
    case "tasks.list":return {tasks:empty?[]:tasks};
    case "tasks.stop":{const task=tasks.find(t=>t.task_id===data.task_id)||routineTaskRecords.get(data.task_id)!;task.state="stopped";task.revision++;return {task};}
    case "memory.query":return {records:empty?[]:memories.filter(m=>m.status!=="forgotten")};
    case "memory.correct":{const memory=memories.find(m=>m.id===data.id)!;memory.content=data.content;memory.revision++;return {record:memory};}
    case "memory.forget":{const memory=memories.find(m=>m.id===data.id)!;memory.status="forgotten";return {record:memory};}
    case "agents.pause":case "agents.resume":{const agent=agents.find(a=>a.agent_id===data.agent_id)!;agent.status=method.endsWith("pause")?"paused":"active";agent.revision++;return {agent};}
    case "connectors.list":return {ok:true,value:{state:"available",connections:connectorScenario === "empty" ? [] : [{connection_id:"synthetic-gmail",provider:"gmail",account_id:"demo@example.invalid",domain:"personal",state:"connected",revision:"one",scopes:["gmail.readonly","gmail.send"],resources:["me"]}]}};
    case "connectors.actions":return {ok:true,value:{actions:structuredClone(connectorActions)}};
    case "connectors.decide": {
      connectorCalls.push({method,input});const action=connectorActions.find(a=>a.action_id===data.action_id);if(!action||action.digest!==data.digest||action.state!=="awaiting_confirmation")throw Error("Stale synthetic decision");
      action.state=data.approve?"completed":"rejected";if(connectorScenario==="lost-ack")throw Error("Synthetic lost response");return {ok:true,value:structuredClone(action)};
    }
    case "connectors.disconnect":connectorCalls.push({method,input});return {ok:true,value:{ok:true}};
    case "connections.status":return {ok:true,value:calendarFixture?{product_domain:"personal",provider:"google-calendar",user_space_id:"local",account_id:"fixture-account",connection_id:"fixture-connection",selection_revision:"1",state:"connected",selected_calendars:["fixture-calendar"],authorization_diagnostics:[{attempt_id:"previous-synthetic-attempt",error_code:"credential_cleanup_failed"}]}:{product_domain:"personal",provider:"google-calendar",user_space_id:"local",state:"unavailable",selected_calendars:[],error_code:"oauth_client_not_configured"}};
    case "connections.calendars":return {ok:true,value:[{id:"fixture-calendar",summary:"合成日历",timeZone:"Asia/Shanghai"}]};
    case "connections.agenda": {
      const stale=++calendarReads>1;
      return {state:stale?"error":"ready",freshness:stale?"stale":"fresh",error_code:stale?"provider_unavailable":undefined,source:{...data,provider:"google-calendar",read_only:true,user_space_id:"local",account_id:"fixture-account",connection_id:"fixture-connection",selection_revision:"1",fetched_at:new Date().toISOString()},events:[{id:"fixture-event",status:"confirmed",summary:"合成日程 · 上次读取结果",all_day:true}],cancelled_events:[]};
    }
    case "review.list":return {ok:true,value:reviewScenario === "unavailable" ? {schema:"personal-review.v1",kind:"unavailable",reason:"尚未配置审核服务，动作审批当前不可用。"} : reviewValue()};
    case "review.settings": {
      reviewCalls.push({method,input:structuredClone(data)});
      if (data.expected_revision!==reviewSnapshot.settings.revision) throw new Error("设置版本已变化，请刷新。");
      reviewSnapshot.settings.auto_review_enabled=data.enabled;reviewSnapshot.settings.revision++;
      return {ok:true,value:reviewValue()};
    }
    case "review.resolve":case "review.revoke": {
      reviewCalls.push({method,input:structuredClone(data)});
      const item=reviewSnapshot.requests.find(value=>value.action.id===data.id);
      if (!item||item.revision!==data.expected_revision||item.action.digest!==data.digest)throw new Error("动作版本已变化，请刷新。");
      if (reviewScenario === "stale-response") { item.status="stale";item.revision++;throw new Error("任务已变更；此动作没有执行，请刷新核对。"); }
      item.status=method === "review.revoke" ? "revoked" : data.choice === "reject" ? "denied" : "approved";item.revision++;
      item.reason=item.status === "approved" ? "合成批准已保存；此预览不运行真实动作。" : "合成决定已保存，没有执行。";
      if (reviewScenario === "lost-ack")throw new Error("响应丢失；请刷新核对已保存的状态，不要重复提交。");
      return {ok:true,value:reviewValue()};
    }
    case "settings.get":return {settings};
    case "settings.update":settings={...settings,...data,revision:settings.revision+1};return {settings};
    default:throw new Error(`Unsupported synthetic operation: ${method}`);
  }
};
installFixtureBridge(invokeRuntime);
const params=new URLSearchParams(location.search);
if (!localStorage.getItem("wuu.fixture.reactions")) { reactionStore["3"] = {agent:"✅",revision:1}; persistReactions(); }
// Normal preview stays representative; stress/error files remain opt-in for QA.
if (!["all", "reference"].includes(params.get("attachments") || "")) histories.mosaic[3].attachments = histories.mosaic[3].attachments!.slice(0, 2);
if (params.get("attachments") === "layout") {
  const sample = histories.mosaic[3].attachments![0];
  const note = histories.mosaic[3].attachments![1];
  const longName = "合成的很长很长文件名，用来检查窄窗口截断与时间显示.txt";
  files.set(`mosaic:5:${sample.sha256}`, {...files.get(`mosaic:4:${sample.sha256}`)!,name:longName});
  files.set(`mosaic:5:${note.sha256}`, files.get(`mosaic:4:${note.sha256}`)!);
  files.set(`mosaic:6:${sample.sha256}`, files.get(`mosaic:4:${sample.sha256}`)!);
  histories.mosaic.push(
    {seq:5,role:"user",at:now,content:"这些是我想一起看的文件。",attachments:[{...sample,name:longName},note]},
    {seq:6,role:"assistant",at:now,content:"",attachments:[sample]},
  );
}
if (params.get("attachments") === "reference") {
  // Self-generated 0.2 second white video: visual fixture, not a user file.
  const video = {"kind": "artifact", "index": 0, "name": "synthetic-preview.mp4", "mime_type": "video/mp4", "size": 1694, "sha256": "89df5a6a21329d6817067a34e6e258317b44a942fea192985a67b5700e9de36c"};
  const html = histories.mosaic[3].attachments![2], archive = histories.mosaic[3].attachments![3];
  histories.mosaic = [
    {seq:1,role:"assistant",at:now,content:"文件会独立展示，说明文字保留在自己的消息气泡里。"},
    {seq:2,role:"assistant",at:now,content:"这是一段合成的空白视频，只用于检查文件图标和卡片样式。",attachments:[video]},
    {seq:3,role:"assistant",at:now,content:"网页和压缩包也放在这里，可以点击文件在 Wuu 内打开。",attachments:[html,archive]},
  ];
  files.set(`mosaic:2:${video.sha256}`, {"data": "AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDEAAAN0bW9vdgAAAGxtdmhkAAAAAAAAAAAAAAAAAAAD6AAAAMgAAQAAAQAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAp90cmFrAAAAXHRraGQAAAADAAAAAAAAAAAAAAABAAAAAAAAAMgAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAACAAAAAgAAAAAAAkZWR0cwAAABxlbHN0AAAAAAAAAAEAAADIAAAEAAABAAAAAAIXbWRpYQAAACBtZGhkAAAAAAAAAAAAAAAAAAAyAAAACgBVxAAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAAAAAAAAAABWaWRlb0hhbmRsZXIAAAABwm1pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRkaW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAAYJzdGJsAAAAvnN0c2QAAAAAAAAAAQAAAK5hdmMxAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAACAAIABIAAAASAAAAAAAAAABFUxhdmM2MS4xOS4xMDEgbGlieDI2NAAAAAAAAAAAAAAAGP//AAAANGF2Y0MBZAAK/+EAF2dkAAqs2UlsBEAAAAMAQAAADIPEiWWAAQAGaOvjyyLA/fj4AAAAABBwYXNwAAAAAQAAAAEAAAAUYnRydAAAAAAAAHcQAAAAAAAAABhzdHRzAAAAAAAAAAEAAAAFAAACAAAAABRzdHNzAAAAAAAAAAEAAAABAAAAOGN0dHMAAAAAAAAABQAAAAEAAAQAAAAAAQAACgAAAAABAAAEAAAAAAEAAAAAAAAAAQAAAgAAAAAcc3RzYwAAAAAAAAABAAAAAQAAAAUAAAABAAAAKHN0c3oAAAAAAAAAAAAAAAUAAALKAAAADAAAAAwAAAAMAAAADAAAABRzdGNvAAAAAAAAAAEAAAOkAAAAYXVkdGEAAABZbWV0YQAAAAAAAAAhaGRscgAAAAAAAAAAbWRpcmFwcGwAAAAAAAAAAAAAAAAsaWxzdAAAACSpdG9vAAAAHGRhdGEAAAABAAAAAExhdmY2MS43LjEwMwAAAAhmcmVlAAADAm1kYXQAAAKuBgX//6rcRem95tlIt5Ys2CDZI+7veDI2NCAtIGNvcmUgMTY0IHIzMTA4IDMxZTE5ZjkgLSBILjI2NC9NUEVHLTQgQVZDIGNvZGVjIC0gQ29weWxlZnQgMjAwMy0yMDIzIC0gaHR0cDovL3d3dy52aWRlb2xhbi5vcmcveDI2NC5odG1sIC0gb3B0aW9uczogY2FiYWM9MSByZWY9MyBkZWJsb2NrPTE6MDowIGFuYWx5c2U9MHgzOjB4MTEzIG1lPWhleCBzdWJtZT03IHBzeT0xIHBzeV9yZD0xLjAwOjAuMDAgbWl4ZWRfcmVmPTEgbWVfcmFuZ2U9MTYgY2hyb21hX21lPTEgdHJlbGxpcz0xIDh4OGRjdD0xIGNxbT0wIGRlYWR6b25lPTIxLDExIGZhc3RfcHNraXA9MSBjaHJvbWFfcXBfb2Zmc2V0PS0yIHRocmVhZHM9MSBsb29rYWhlYWRfdGhyZWFkcz0xIHNsaWNlZF90aHJlYWRzPTAgbnI9MCBkZWNpbWF0ZT0xIGludGVybGFjZWQ9MCBibHVyYXlfY29tcGF0PTAgY29uc3RyYWluZWRfaW50cmE9MCBiZnJhbWVzPTMgYl9weXJhbWlkPTIgYl9hZGFwdD0xIGJfYmlhcz0wIGRpcmVjdD0xIHdlaWdodGI9MSBvcGVuX2dvcD0wIHdlaWdodHA9MiBrZXlpbnQ9MjUwIGtleWludF9taW49MjUgc2NlbmVjdXQ9NDAgaW50cmFfcmVmcmVzaD0wIHJjX2xvb2thaGVhZD00MCByYz1jcmYgbWJ0cmVlPTEgY3JmPTIzLjAgcWNvbXA9MC42MCBxcG1pbj0wIHFwbWF4PTY5IHFwc3RlcD00IGlwX3JhdGlvPTEuNDAgYXE9MToxLjAwAIAAAAAUZYiEADP//vbsvgU1/Z/QlnnHF8EAAAAIQZokbEK//lYAAAAIQZ5CeIX/VcEAAAAIAZ5hdEK/WsAAAAAIAZ5jakK/WsE=", "name": "synthetic-preview.mp4", "mime": "video/mp4"});
  for (const file of [html, archive]) files.set(`mosaic:3:${file.sha256}`,files.get(`mosaic:4:${file.sha256}`)!);
}
if (reviewScenario !== "unavailable") histories.mosaic = [
  {seq:1,role:"user",at:now,content:"把阅读资料整理成一个简洁的清单，先让我看看。"},
  {seq:2,role:"assistant",at:now,content:"清单已整理好。若要保存到 Personal 文件空间，请确认下面的具体动作；你也可以继续聊别的事情。"},
];
const motionScale = Math.min(8, Math.max(1, Number(params.get("motionScale")) || 1));
if (params.get("replies") === "1") histories.mosaic = [
  {seq:1,role:"assistant",at:now,content:"DISCUSSION ROOT: original authorization"},
  ...Array.from({length:24}, (_, index) => ({seq:index+2,role:index%2 ? "assistant" : "user",at:now,content:`Discussion message ${index+2}: ${"Historical context remains readable. ".repeat(5)}`,relation:{reply_to:{session_id:"synthetic-main",seq:index+1}}})),
  {seq:26,role:"assistant",at:now,content:"UNRELATED DISCUSSION"},
];
if (motionScale > 1) document.documentElement.style.setProperty("--mosaic-profile-motion-duration", `${300 * motionScale}ms`);
if (params.get("grouping") === "1") histories.mosaic.push(
  {seq:5,role:"assistant",at:now,content:"也可以先不做决定。等你忙完，我们再慢慢选。"},
  {seq:6,role:"user",at:now,content:"那就选离家近一点的。"},
  {seq:7,role:"user",at:now,content:"安静、方便聊天就好。"},
);
if(params.has("receipts")) histories.mosaic[3].attachments = undefined;
applyThemePreference(params.get("theme")==="dark"?"dark":"light");
document.documentElement.style.setProperty("--conversation-message-font-size",`${params.get("font")==="19"?19:15}px`);
document.documentElement.dataset.platform="linux";
if (params.get("font")==="20") { document.documentElement.style.setProperty("--font-ui","20px");document.documentElement.style.fontSize="20px"; }
function FixtureSurface(){
  return <I18nProvider preferenceStore={{get:()=>"zh-CN",set:async()=>{},subscribe:()=>()=>{}}}><WuuUIRoot><div className="mosaic-fixture"><div className="mosaic-fixture-label" style={{flexWrap:"wrap"}}><span>{reviewScenario !== "unavailable" ? "动作审批 · 当前实际桌面组件 · 合成数据 · 不执行文件写入或模型调用" : "twin · 实际桌面组件 · 合成演示数据 · 非生产账号 · 不会触发真实定时任务 · 修改仅用于当前预览"}{motionScale > 1 ? ` · 动画轨迹检查 ${motionScale}×慢速` : ""}</span><div hidden={reviewScenario !== "unavailable"}><a href="?receipts=1">回执状态场景</a><a href="?receipts=1&replyReceipts=1&sendScenario=sending">Reply 回执场景</a><a href="?attachments=reference">参考文件展示</a><a href="?attachments=all">文件类型测试</a><a href="?attachments=layout">附件布局测试</a><button onClick={()=>{const seq=3;const current=reactionsFor(seq);reactionStore[String(seq)]={...current,agent:current.agent=== "✅"?"❤️":"✅",revision:current.revision+1};persistReactions();host.publishHostEvent({kind:"notification",message:{method:"plugin/client/invalidated",params:{plugin_id:"personal",fingerprint:"synthetic-ui-fixture",user_space_id:"local",product_domain:"personal",topic:"reactions"}}});}}>Twin 表态（合成）</button><button onClick={()=>setActivity(fixtureActivity === "idle" ? "thinking" : fixtureActivity === "thinking" ? "responding" : "idle")}>合成状态：切换</button><button onClick={()=>applyThemePreference(document.documentElement.dataset.theme==="dark"?"light":"dark")}>明 / 暗</button><button onClick={()=>{offline=!offline;host.publishHostEvent({type:"fixture"});}}>离线 / 在线</button></div>{reviewScenario !== "unavailable" && <div><button data-review-open-settings onClick={()=>window.dispatchEvent(new CustomEvent("wuu:open-settings",{detail:{page:"plugin-view:personal:personal-preferences"}}))}>Auto Review 设置</button><button onClick={()=>applyThemePreference(document.documentElement.dataset.theme==="dark"?"light":"dark")}>明 / 暗</button><select aria-label="合成审批场景" value={reviewScenario} onChange={event=>{const url=new URL(location.href);url.searchParams.set("review",event.target.value);location.href=url.href;}}>{["ready","empty","expired","blocked","stale","denied","revoked","uncertain","succeeded","long","lost-ack","stale-response"].map(value=><option key={value} value={value}>{value}</option>)}</select></div>}</div>{params.has("receipts") && <ReceiptScenarioControls/>}<div className="mosaic-fixture-view">{params.get("presentation") === "standard" ? <PluginConversationScope application={fixtureApplication} signal={fixtureLifetime.signal}><PluginConversationView conversationId="standard-fixture" title="Standard conversation regression fixture" searchLabel="Search" searchContent={null} composer={null}>{histories.mosaic.map(record => <PluginConversationMessage key={record.seq} id={`standard:${record.seq}`} role={record.role as "user" | "assistant"} content={record.content} timestamp={record.at} />)}</PluginConversationView></PluginConversationScope> : <ApplicationShellOutlet host={host} binding={{pluginId:"wuu.default-shell",generation:"fixture",id:"ui.shell"}} surfaceBindings={surfaceBindings} />}</div></div></WuuUIRoot></I18nProvider>;
}
let fixtureTurn = 0;
function setActivity(status: "idle" | "thinking" | "responding") {
  fixtureActivity = status;
  const publish = (method: string, extra: Record<string, unknown> = {}) => host.publishHostEvent({kind:"notification",message:{method,params:{thread_id:"synthetic-main",turn_id:`synthetic-avatar-state:${fixtureTurn}`,...extra}}});
  publish("item/removed", {item_id:"synthetic-avatar-message"});
  if (status !== "idle") fixtureTurn++;
  if (status === "idle") publish("turn/completed", {turn:{id:`synthetic-avatar-state:${fixtureTurn}`}});
  else {
    publish("turn/started");
    if (status === "responding") publish("item/agentMessage/delta", {item_id:"synthetic-avatar-message",delta:"这是合成状态演示，正在回复。"});
  }
}
async function start(){
  await host.activateGeneration({graphGeneration:"fixture-graph",pluginId:"wuu.default-application",generation:"fixture",register:activateApplication});
  await host.selectApplicationBinding({pluginId:"wuu.default-application",generation:"fixture",id:"ui.application"});
  for (const [pluginId, register] of [
    ["wuu.default-navigation", activateNavigation], ["wuu.default-composer", activateComposer],
    ["wuu.default-conversation", activateConversation], ["wuu.default-preview", activatePreview],
    ["wuu.default-settings", activateSettings], ["wuu.default-shell", activateShell],
  ] as const) await host.activateGeneration({graphGeneration:"fixture-graph",pluginId,generation:"fixture",register});
  await host.activateGeneration({graphGeneration:"fixture-graph",pluginId:"fixture-controls",generation:"fixture",register(api){fixtureApplication=api.application;}});
  controller = fixtureApplication.workbench;
  window.addEventListener("pagehide", () => { fixtureLifetime.abort(); void host.selectApplicationBinding(undefined).finally(() => { for (const id of ["personal", "personal-calendar", "automations", "fixture-controls", "wuu.default-shell", "wuu.default-navigation", "wuu.default-composer", "wuu.default-conversation", "wuu.default-preview", "wuu.default-settings", "wuu.default-application"]) host.unload(id); }); }, {once:true});
  await host.activateGeneration({graphGeneration:"fixture-graph",pluginId:"personal",generation:"synthetic-ui-fixture",contributions:{navigation:[{id:"personal",title:"twin",view:"personal.home",icon:{name:"mascot"},order:41}],settings_pages:[{id:"personal-preferences",title:"twin",view:"personal.preferences",icon:{name:"mascot"},order:41}]},register:activate});
  await host.activateGeneration({graphGeneration:"fixture-graph",pluginId:"personal-calendar",generation:"synthetic-ui-fixture",register:activateCalendar});
  await host.activateGeneration({graphGeneration:"fixture-graph",pluginId:"automations",generation:"synthetic-ui-fixture",contributions:{navigation:[{id:"automations",title:"Automations",view:"automations.home",icon:{name:"clock"},order:40}]},register(api){api.registerViewType({id:"automations.home",title:"Automations",defaultRegion:"primary",render:()=> <div className="plugin-ui-page">合成 Automations 入口</div>});}});
  if (params.get("presentation") !== "standard") await controller.openPluginView("personal", "personal.home");
  createRoot(document.getElementById("root")!).render(<FixtureSurface/>);
  (window as any).mosaicFixture={host,controller,setOffline:(value:boolean)=>{offline=value;},setEmpty:(value:boolean)=>{empty=value;},state:()=>({agents,tasks,memories}),setActivity,synthetic:true};
}
start().catch(error=>{document.body.textContent=String(error);console.error(error);});
