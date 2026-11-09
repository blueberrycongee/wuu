import * as React from "react";
import { createRoot } from "react-dom/client";
import { createPluginUIKit } from "../../src/shared/workbench";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { createPersonalReviewPanel, type ReviewRecord, type ReviewService, type ReviewSnapshot } from "../../../plugins/personal/ui/action-review-panel";
import "../../src/renderer/styles/base.css";
import "../../src/renderer/styles/spacing.css";
import "../../src/renderer/styles/theme.css";
import "../../src/renderer/styles/settings.css";
import "../../src/renderer/styles/workbench.css";
import "../../src/renderer/styles/focus.css";

const ui = createPluginUIKit(React);
startFocusModality();
const Panel = createPersonalReviewPanel(React, ui);
const query = new URLSearchParams(location.search);
const dark = query.get("theme") === "dark", large = query.get("font") === "20", scenario = query.get("scenario") || "ready";
document.documentElement.dataset.theme = dark ? "dark" : "light";
document.documentElement.style.setProperty("--font-ui", large ? "20px" : "15px");
document.documentElement.style.fontSize = large ? "20px" : "15px";

const pending: ReviewRecord = {
  revision:2,status:"awaiting_confirmation",outcome:"ask",reason:"活动已准备好，还没有收到写入这个日历的明确批准。",
  action:{id:"synthetic-event-1",digest:"4497ca58e02070c86b45e1a05c910203cd6c0294f4bda749473ba74e33a7fa21",operation:"synthetic.calendar.create",
    scope:{space_id:"synthetic-personal",session_id:"synthetic-worker",task_id:"周末阅读",requirements_revision:3},effect:"data_write",
    summary:{title:"把周末阅读时间加入日历",account:"demo@example.invalid",target:"合成日历 / 阅读计划",consequence:"新增一个周六 15:00–16:00 的日历活动。没有邀请参与人。"},
    arguments_json:'{"attendees":[],"calendar_id":"synthetic-calendar","end":"2026-10-24T16:00:00+08:00","start":"2026-10-24T15:00:00+08:00","title":"周末阅读"}',expires_at:new Date(Date.now()+9*60_000).toISOString()},
};
const completed:ReviewRecord={...structuredClone(pending),revision:5,status:"succeeded",outcome:"allow",reason:"原始用户指令已覆盖此动作，审核通过，合成执行器返回回执。",action:{...structuredClone(pending.action),id:"synthetic-authorized-1",scope:{...pending.action.scope,task_id:"整理阅读笔记"},summary:{title:"保存已明确要求的阅读笔记",account:"合成笔记空间",target:"阅读 / 本周笔记",consequence:"可信授权精确覆盖此次保存，未再次询问。"},operation:"synthetic.notes.save",arguments_json:'{"body":"合成示例","path":"synthetic/reading-note"}'}};
const uncertain:ReviewRecord={...structuredClone(pending),revision:4,status:"uncertain",outcome:"deny",reason:"模拟执行器已开始，但回执未保存。需要核对结果，不会重放动作。",action:{...structuredClone(pending.action),id:"synthetic-uncertain-1",summary:{...pending.action.summary,title:"回执缺失的合成动作"}}};
let snapshot:ReviewSnapshot=scenario==="unavailable"?{schema:"personal-review.v1",kind:"unavailable",reason:"生产动作门禁尚未接入。此处不会开启现有账户的自动批准。"}:{schema:"personal-review.v1",kind:"ready",requests:scenario==="empty"?[]:[pending,completed,uncertain]};
if(scenario==="expired")pending.action.expires_at="2000-01-01T00:00:00Z";
if(scenario==="blocked"){
  pending.status="blocked";pending.revision=4;
  pending.reason="合成审核服务暂不可用；动作未执行，原本的单次批准仍可撤回。";
}
if(scenario==="long")pending.action.summary.target="合成工作空间 / "+"需要完整显示的较长目标名称与账号范围 / ".repeat(5);
if(scenario==="numbers"){
  pending.action.operation="synthetic.numbers.save";
  pending.action.scope.task_id="参数精度校验";
  pending.action.summary={title:"核对完整数值参数（合成）",account:"合成数据集",target:"合成数值存储",consequence:"保存所显示的精确数值。此 fixture 不执行动作。"};
  pending.reason="此数据用于核对完整参数显示，没有连接真实服务。";
  pending.action.arguments_json='{"fraction":0.123456789012345678901,"integer":9007199254740993,"overflow":1e400}';
}

// This service changes only synthetic in-memory presentation data. It never
// calls a plugin, host capability, credential store, network or execution API.
const service:ReviewService={
  async list(){return scenario==="unknown-schema"?{...structuredClone(snapshot),schema:"personal-review.future"}:structuredClone(snapshot)},
  async resolve(command){
    if(scenario==="error")throw new Error("合成保存失败。批准未确认，请刷新后核对。");
    if(snapshot.kind!=="ready")throw new Error("unavailable");
    const record=snapshot.requests.find(item=>item.action.id===command.id);
    if(!record||record.revision!==command.expected_revision||record.action.digest!==command.digest)throw new Error("合成动作已变化，请刷新。");
    record.status=command.choice==="approve_once"?"approved":"denied";record.revision++;
    record.reason=command.choice==="approve_once"?"本次决定已保存在合成内存中。此 fixture 不执行动作。":"已拒绝此合成动作。";
    return structuredClone(snapshot);
  },
  async revoke(command){
    if(snapshot.kind!=="ready")throw new Error("unavailable");
    const record=snapshot.requests.find(item=>item.action.id===command.id);
    if(!record||record.revision!==command.expected_revision||record.action.digest!==command.digest)throw new Error("合成动作已变化，请刷新。");
    record.status="revoked";record.revision++;record.reason="本次合成批准已撤回，没有执行。";return structuredClone(snapshot);
  },
};

function Fixture(){
  return <div className="review-fixture">
    <aside className="review-fixture-label"><span>真实组件 fixture · 全部为合成数据 · 尚未接入生产动作</span><span><a href={`?theme=${dark?"light":"dark"}&font=${large?"20":"15"}&scenario=${scenario}`}>切换主题</a> · <a href={`?theme=${dark?"dark":"light"}&font=${large?"15":"20"}&scenario=${scenario}`}>字号</a></span></aside>
    <main className="review-fixture-main settings-scroll">
      <p className="review-fixture-path">账号菜单 / 设置 / 插件 / twin</p><h1>twin</h1>
      <ui.Section title="连接与工具" description="仅管理 twin 的账号连接。">
        <p className="personal-review-muted">连接面板由独立模块接入。此预览不连接账号，也不展示 Coding 的 MCP 凭据。</p>
      </ui.Section>
      <Panel service={service} locale="zh-CN"/>
    </main>
  </div>;
}
const container=document.getElementById("root");if(!container)throw new Error("fixture root missing");createRoot(container).render(<Fixture/>);
