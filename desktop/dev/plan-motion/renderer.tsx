// Production component and styles with local data; no preload or model calls.
import * as React from "react";
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { ConversationStatusCluster } from "../../src/renderer/ConversationStatusCluster";
import { PluginHost } from "../../src/renderer/plugins/PluginHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import "../../src/renderer/styles.css";
import "./fixture.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
document.documentElement.dataset.appearanceMotion = params.get("motion") || "full";
applyMessageFlowFontSize(Number(params.get("size")) || 14);
const host = new PluginHost({ react: React });
const short = ["梳理当前界面的交互", "加入任务推进动效", "检查键盘与减少动态效果", "验证并整理结果"];
const long = [
  "梳理当前界面的交互，包括很长的任务描述、中文与 English 混排时的换行和标记对齐。",
  ...short.slice(1),
  ...Array.from({ length: 10 }, (_, index) => `补充验收 ${index + 1}：检查不同窗口宽度与文字大小`),
];

function Fixture() {
  const [completed, setCompleted] = useState(1);
  const [empty, setEmpty] = useState(false);
  const content = params.has("long") ? long : short;
  return (
    <main className="plan-preview">
      <header>
        <span className="plan-preview-eyebrow">WUU · MOTION STUDY</span>
        <h1>每一步，都有回应。</h1>
        <p>悬停或用 Tab 聚焦 TODO，查看任务推进。</p>
      </header>
      <section className="plan-preview-stage" aria-label="任务状态预览">
        <div className="plan-preview-message">
          <p>让变化发生在任务所在的位置。</p>
          <p>完成一步后，进度环向前推进，行内圆环收成勾号。</p>
        </div>
        <ConversationStatusCluster
          host={host} visible threadId="plan-preview"
          hostStatus={params.has("crowded") ? <div className="conversation-status-capsule"><span className="conversation-status-label">项目进行中</span></div> : undefined}
          navigation={params.has("crowded") ? <button className="jump-to-latest-pill" aria-label="回到最新">↓</button> : undefined}
          todoUpdate={{ todos: empty ? [] : content.map((text, index) => ({
            content: text,
            status: index < completed ? "completed" : index === completed ? "in_progress" : "pending",
          })) }}
          onOpenSession={() => {}}
        />
        <div className="plan-preview-composer">继续处理这个计划…<span>↑</span></div>
      </section>
      <footer>
        <button id="advance" onClick={() => setCompleted(value => Math.min(content.length, value + 1))}>完成一步</button>
        <button id="reverse" onClick={() => setCompleted(value => Math.max(0, value - 1))}>回退一步</button>
        <button id="reset" onClick={() => { setCompleted(0); setEmpty(false); }}>重新开始</button>
        <button id="empty" onClick={() => setEmpty(value => !value)}>空计划</button>
      </footer>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<I18nProvider><Fixture /></I18nProvider>);
