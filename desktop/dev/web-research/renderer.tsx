import { useState } from "react";
import { createRoot } from "react-dom/client";
import { AssistantTurnShell } from "../../src/renderer/AssistantTurnShell";
import { buildAssistantTurnDisplay } from "../../src/renderer/AssistantTurnDisplay";
import { I18nProvider } from "../../src/renderer/i18n";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { ConversationRenderActivityProvider } from "../../src/renderer/ConversationRenderActivity";
import type { TurnSource } from "../../src/renderer/ToolActivityHelpers";
import type { ThreadItem, Turn } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";
import "./fixture.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
document.documentElement.dataset.appearanceMotion = params.get("motion") || "full";
applyMessageFlowFontSize(Number(params.get("size")) || 14);
const sources: TurnSource[] = [
  { host: "developer.mozilla.org", title: "Using CSS animations", url: "https://developer.mozilla.org/en-US/docs/Web/CSS/CSS_animations/Using_CSS_animations", origin: "web_search" },
  { host: "webkit.org", title: "A guide to view transitions", url: "https://webkit.org/blog/", origin: "web_search" },
  { host: "w3.org", title: "Animation from interactions", url: "https://www.w3.org/WAI/WCAG22/Understanding/animation-from-interactions.html", origin: "web_search" },
  { host: "react.dev", title: "Preserving and resetting state", url: "https://react.dev/learn/preserving-and-resetting-state", origin: "web_search" },
  { host: "github.com", title: "Motionbook · Interaction studies", url: "https://github.com/blueberrycongee/motionbook", origin: "web_search" },
  { host: "web.dev", title: "Animations and performance", url: "https://web.dev/learn/css/animations", origin: "web_search" },
  { host: "developer.chrome.com", title: "View transitions", url: "https://developer.chrome.com/docs/web-platform/view-transitions", origin: "web_search" },
  { host: "caniuse.com", title: "CSS animation support", url: "https://caniuse.com/css-animation", origin: "web_search" },
  ...Array.from({ length: 12 }, (_, i): TurnSource => ({ host: `source-${i}.example.com`, title: `Long research result ${i} with mixed 中文与 English text that should stay accessible in the tooltip`, url: `https://source-${i}.example.com/article`, origin: "web_search" })),
];
const noOp = () => {};

function Fixture() {
  const [phase, setPhase] = useState<"search" | "done" | "failed" | "stopped">("search");
  const [count, setCount] = useState(0);
  const [revision, setRevision] = useState(0);
  const [active, setActive] = useState(true);
  const [opened, setOpened] = useState("");
  const query = params.has("long")
    ? "如何设计自然、可访问且适合桌面应用的界面动效 — practical interface motion and spatial continuity"
    : "自然、克制的界面动效";
  const tool: ThreadItem = {
    id: "web", name: "web_search", type: "tool_call",
    status: count > 0 || phase === "done" ? "completed" : phase === "failed" ? "failed" : "in_progress",
    arguments: JSON.stringify({ query }),
    result: count > 0 ? JSON.stringify({ results: sources.slice(0, Math.min(count, 3)) }) : undefined,
  };
  const extra: ThreadItem = {
    id: "web-extra", name: "web_search", type: "tool_call", status: "completed",
    arguments: JSON.stringify({ query: "界面动效 可访问性" }),
    result: JSON.stringify({ results: sources.slice(3, count) }),
  };
  const turn: Turn = {
    id: `preview-${revision}`, items_view: "full",
    status: phase === "done" ? "completed" : phase === "stopped" ? "interrupted" : phase === "failed" ? "failed" : "in_progress",
    duration_ms: phase === "search" ? undefined : 7000,
    items: [
      { id: "commentary", type: "agent_message", status: "completed", terminal: false, role: "assistant", text: "我会找一些交互参考，也看看可访问性方面的建议。" },
      tool,
      ...(count > 3 ? [extra] : []),
      ...(phase === "done" ? [{ id: "answer", type: "agent_message", status: "completed", terminal: true, role: "assistant",
        text: "可以从三个地方入手：让变化有来源，让反馈及时，让动效在任务完成后安静下来。\n\n例如搜索时，网站图标轻轻出现。需要了解来源时，再悬停查看标题或打开原文。" } as ThreadItem] : []),
    ],
  };
  const display = buildAssistantTurnDisplay(turn, undefined, item => <span>{item.text}</span>)!;
  return (
    <main className="research-preview">
      <header className="research-preview-chrome"><strong>wuu</strong><span>网页搜索</span><span>···</span></header>
      <div className="research-preview-conversation">
        <div className="research-preview-user">帮我查一下，怎样让界面动效自然又有品味？</div>
        <ConversationRenderActivityProvider active={active}>
          <AssistantTurnShell key={revision} turn={turn} display={display}
            onStreamFrame={noOp} onOpenURL={url => setOpened(url)} />
        </ConversationRenderActivityProvider>
      </div>
      <div className="research-preview-composer">继续提问…<span>↑</span></div>
      <footer className="research-preview-controls">
        <button id="reset" onClick={() => { setCount(0); setPhase("search"); setRevision(v => v + 1); }}>开始搜索</button>
        <button id="results" onClick={() => setCount(3)}>结果到达</button>
        <button id="more-results" onClick={() => setCount(8)}>更多结果</button>
        <button id="finish" onClick={() => setPhase("done")}>搜索完成</button>
        <button id="fail" onClick={() => setPhase("failed")}>失败</button>
        <button id="stop" onClick={() => setPhase("stopped")}>停止</button>
        <button id="many" onClick={() => setCount(20)}>长列表</button>
        <button id="background" onClick={() => setActive(v => !v)}>切换后台</button>
      </footer>
      <output id="opened">{opened}</output>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<I18nProvider><Fixture /></I18nProvider>);
