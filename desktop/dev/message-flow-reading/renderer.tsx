import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { TurnView } from "../../src/renderer/TurnView";
import { CONTINUE_TURN_EVENT, type ContinueTurnDetail } from "../../src/renderer/TurnNotice";
import type { Turn } from "../../src/shared/protocol";
import { ThreadItemView } from "../../src/renderer/ThreadItemView";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { RichContent } from "../../src/renderer/RichContent";
import { StreamingMarkdown } from "../../src/renderer/StreamingMarkdown";
import { streamTextStore } from "../../src/renderer/StreamText";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";
import "./fixture.css";
import mediaSource from "./media.svg?raw";
import mediaURL from "./media.svg?no-inline";
import portraitURL from "./media-portrait.svg?no-inline";
import panoramaURL from "./media-panorama.svg?no-inline";

const sample = `阅读体验来自稳定的节奏。文字大小可以按个人习惯调整，而段落、标题和列表之间的关系应该始终清楚，让读者能顺着内容自然往下读。

同一段内容里会混合中文、English、**强调文字**和 \`conversation-shell.css\`。这些元素需要共享平稳的行距，长句换行时也能迅速找到下一行的起点。

## 把相关内容放在一起

列表适合呈现并列关系。下面包含长条目、二级缩进和一个包含两段文字的条目：

- **稳定的左侧对齐线。** 标题与正文从同一位置开始，列表符号留在文字左侧。即使这一项跨越两行，后续文字也应该对齐条目开头，而不是对齐圆点。
- **适度的层级缩进。** 第二层说明只向内移动一级。
  - 子项继续使用同一套行高，长句折行后也要保持相同的文字起点，避免左侧边界逐渐变得混乱。
  - 另一个子项用于比较同组内容之间的距离。
- **一个条目可以有多个段落。** 这是第一段，说明当前选择。

  这是同一个条目里的第二段。两段应该分开呈现，同时保持在同一条目内，不能被挤成连续的一行。

## 标题应当靠近下文
这个段落与标题之间只有一个 Markdown 换行，用来检查同一个流式分块内部的排版。

### 编号与任务列表

9. 第九步，检查较宽的编号是否仍能留在缩进范围内。
10. 第十步，确认换行后的文字与第一行一致，特别是在较窄的窗口或放大字号时。

- [x] 已完成：正文、标题、列表关系。
- [ ] 待确认：长路径与嵌套内容。

> 引用是一组独立的内容。边线提示它与正文的关系，内部段落继续遵循统一的阅读节奏。
>
> 第二段引用需要保留自己的段距。

## 代码和宽内容

[网页链接](https://example.com)和[文件链接](/tmp/wuu-reading-example.ts)需要在消息背景上保持清晰。

The reading rhythm should also work for English prose. Inline paths such as \`desktop/src/renderer/styles/conversation-shell.css\` need to wrap safely without changing the spacing of the surrounding paragraph.

\`\`\`ts
const size = preferences.messageFlowFontSize;
applyMessageFlowFontSize(size);
\`\`\`

| 场景 | 需要检查的关系 |
| --- | --- |
| 默认字号 | 段落、标题和列表的距离 |
| 放大字号 | 行高、缩进和勾选框同步变化 |
| 窄窗口 | 换行、嵌套和长路径不溢出 |

正文结束后，下一轮对话应有清楚的间隔。`;

const userSample = `请检查这段长消息的阅读效果：中文、English、**强调**和 \`inline code\` 应该在浅色背景上保持清楚。

[网页链接](https://example.com)也需要足够的对比度。

> 引用保留层级，与正文共享文字颜色。

\`\`\`ts
const theme = "light";
applyTheme(theme);
\`\`\`

| 内容 | 预期 |
| --- | --- |
| 正文 | 深色文字 |
| 链接 | 清晰可辨 |

长路径 \`desktop/src/renderer/styles/conversation-shell.css\` 在窄窗口和大字号下也应能够阅读。`;

const conversationTurns: Turn[] = [
  {
    id: "sample-first", status: "completed", items_view: "full", duration_ms: 42000,
    started_at: "2026-09-17T05:58:00Z", answer_ready_at: "2026-09-17T05:58:42Z", completed_at: "2026-09-17T05:58:45Z",
    items: [
      { id: "sample-request", type: "user_message", status: "completed", text: "请帮我梳理这个项目的消息流。保留浅灰色用户气泡，让长回答更容易阅读，输入框与消息流保持同宽。代码、执行过程与最终结果也需要有清楚的层次。" },
      { id: "sample-commentary", type: "agent_message", status: "completed", terminal: false, text: "我先检查消息的渲染结构，再比较代码块、过程信息和最终回答的呈现方式。" },
      { id: "sample-tool", type: "tool_call", status: "completed", name: "bash", arguments: JSON.stringify({command: "npm run check"}), display: {kind: "command", capability: "command.bash"}, result: JSON.stringify({exit_code: 0, stdout: "Checked the example workspace."}) },
      { id: "sample-answer", type: "agent_message", status: "completed", terminal: true, text: "消息流可以围绕三个角色组织：请求提供上下文，过程说明正在发生什么，最终回答承载结果。\n\n## 阅读与操作的边界\n\n- 用户请求靠右，回复正文沿左侧展开。\n- 过程收起后保留入口，需要时能展开检查。\n- 代码和产物采用同一种内嵌容器，输入区保持完整的操作空间。\n\n```ts\nconst view = { column: 768, message: 'readable', composer: 'aligned' };\n```\n\n这段示例只用于视觉验收，不代表真实执行结果。" },
    ],
  },
  { id: "sample-second", status: "completed", items_view: "full", duration_ms: 6000, started_at: "2026-09-17T06:00:00Z", completed_at: "2026-09-17T06:00:06Z", items: [
    { id: "sample-followup", type: "user_message", status: "completed", text: "短回答也要自然一点。" },
    { id: "sample-queued", type: "user_message", status: "completed", text: "还有连续补充的消息。" },
    { id: "sample-short", type: "agent_message", status: "completed", terminal: true, text: "可以。短回答直接呈现内容，保持与上一个回答相同的文字起点和行距。" },
  ] },
];

const mediaAnswer = `图片使用独立的阅读空间，前后的文字保持清楚的分组。

![示例插画](${new URL(mediaURL, location.origin).href})

这张图片展示完整内容；点击图片可以查看原图。

![竖向截图](${new URL(portraitURL, location.origin).href})

竖图不应占满整屏，也不应裁掉内容。

![横向示意图](${new URL(panoramaURL, location.origin).href})

横图在窄窗口内按比例缩小。`;
const mediaTurns: Turn[] = [{
  id: "media-turn", status: "completed", items_view: "full", duration_ms: 1200,
  items: [
    { id: "media-request", type: "user_message", status: "completed", text: "查看这张图片，再展示插画、竖图和横图。", images: [{ media_type: "image/svg+xml", data: btoa(mediaSource) }] },
    { id: "media-tool", type: "tool_call", name: "view_image", status: "completed", display: { kind: "read", label: "查看图片" }, result_detail: { content: [{ type: "image", mime_type: "image/svg+xml", data: btoa(mediaSource), name: "Example illustration" }] } },
    { id: "media-answer", type: "agent_message", status: "completed", terminal: true, text: mediaAnswer },
  ],
}];

// One first-query turn per failure the reader meets most. The live replay
// starts the latest case in progress so its arrival can be inspected.
const failureRequest = { type: "user_message" as const, status: "completed" as const, text: "帮我总结一下这个仓库最近一周的改动。" };
const failureCases: Record<string, Turn> = {
  rateLimit: { id: "failure-rate", status: "failed", items_view: "full", items: [{ ...failureRequest, id: "failure-rate-q" }],
    error: { message: "HTTP 429: rate_limit_error: Number of request tokens has exceeded your per-minute rate limit (org_123, model gpt-x)", category: "provider", status_code: 429,
      recovery: { attempt_count: 4, retry_count: 3, max_attempts: 6, submission_count: 4, stop_reason: "non_retryable", failure_category: "rate_limit" } } },
  offline: { id: "failure-offline", status: "failed", items_view: "full", items: [{ ...failureRequest, id: "failure-offline-q" }],
    error: { message: "stream request failed: dial tcp: lookup api.example.com: no such host", category: "network" } },
  dropped: { id: "failure-dropped", status: "failed", items_view: "full", items: [
    { ...failureRequest, id: "failure-dropped-q" },
    { id: "failure-dropped-a", type: "agent_message", status: "completed", terminal: false, text: "最近一周主要有三类改动。\n\n**桌面端消息流**：用户消息的悬停动作移到气泡下方，失败卡片改为原地展开，长回复的段落间距统一到阅读节奏。\n\n**自动化页面**：列表与详情重新分栏，运行记录按日期分组，并补上了进入和切换时的动效。\n\n**工作区标签**：展开的工作区标签现在与对话共享，新建标签会" },
  ], error: { message: "stream error: unexpected EOF before response.completed", category: "network", recovery: { attempt_count: 1, retry_count: 0, max_attempts: 6, submission_count: 1, stop_reason: "replay_unsafe", failure_category: "incomplete_stream" } } },
  auth: { id: "failure-auth", status: "failed", items_view: "full", items: [{ ...failureRequest, id: "failure-auth-q" }],
    error: { message: "stream request failed: HTTP 401: 401 Unauthorized: {\"error\":{\"message\":\"Incorrect API key provided.\"}}", category: "auth", status_code: 401,
      recovery: { attempt_count: 1, retry_count: 0, max_attempts: 6, submission_count: 1, stop_reason: "non_retryable", failure_category: "authentication" } } },
  unknown: { id: "failure-unknown", status: "failed", items_view: "full", items: [{ ...failureRequest, id: "failure-unknown-q" }],
    error: { message: "HTTP 400: invalid_request_error: messages.1.content.0.tool_use_id: unexpected tool_use_id found in tool_result blocks: toolu_01ABC", category: "invalid_request", status_code: 400, code: "invalid_request_error" } },
  retrying: { id: "failure-retrying", status: "in_progress", items_view: "full", items: [
    { ...failureRequest, id: "failure-retrying-q" },
    { id: "failure-retrying-r", type: "stream_reconnect", status: "in_progress", reason: "overloaded", retry_count: 2, retry_at_ms: Date.now() + 600_000, text: "Upstream overloaded" },
  ] },
};

function liveFailureTurn(stage: number): Turn {
  const base = failureCases.rateLimit;
  if (stage === 0) return { ...base, status: "in_progress", error: undefined };
  if (stage === 1) return { ...base, status: "in_progress", error: undefined, items: [...base.items,
    { id: "failure-live-r", type: "stream_reconnect", status: "in_progress", reason: "rate_limit", retry_count: 1, retry_at_ms: Date.now() + 1500, text: "Too many requests" }] };
  return { ...base, status: "failed", items: [...base.items,
    { id: "failure-live-r", type: "stream_reconnect", status: "failed", reason: "rate_limit", retry_count: 3, text: "Too many requests" }] };
}

function FailureSurface(): JSX.Element {
  const [stage, setStage] = useState<number | undefined>(undefined);
  useEffect(() => {
    if (stage === undefined || stage >= 2) return;
    const timer = window.setTimeout(() => setStage(stage + 1), stage === 0 ? 900 : 1700);
    return () => window.clearTimeout(timer);
  }, [stage]);
  const retry = () => new Promise<void>(resolve => window.setTimeout(resolve, 600));
  useEffect(() => {
    const resume = (event: Event): void => {
      event.preventDefault();
      window.setTimeout(() => (event as CustomEvent<ContinueTurnDetail>).detail.done(true), 600);
    };
    window.addEventListener(CONTINUE_TURN_EVENT, resume);
    return () => window.removeEventListener(CONTINUE_TURN_EVENT, resume);
  }, []);
  return <WuuUIRoot>
    <button type="button" className="fixture-replay" onClick={() => setStage(0)}>回放：发送 → 重试 → 失败</button>
    {stage !== undefined ? <div className="fixture-failure-case" data-case="live"><TurnView turn={liveFailureTurn(stage)} isLatestTurn onStreamFrame={() => {}}
      streamStatus={stage === 0 ? { text: "正在思考", liveProgress: true } : undefined}
      onEditMessage={() => {}} onSubmitEditMessage={retry} /></div> : null}
    {Object.entries(failureCases).map(([name, turn]) => <div key={name} className="fixture-failure-case" data-case={name}>
      <TurnView turn={turn} threadID="fixture" isLatestTurn onStreamFrame={() => {}} onEditMessage={() => {}} onSubmitEditMessage={retry} />
    </div>)}
  </WuuUIRoot>;
}

function Fixture(): JSX.Element {
  const params = new URLSearchParams(location.search);
  const [size, setSize] = useState<number>(Number(params.get("size")) || MESSAGE_FLOW_FONT_SIZE_RANGE.default);
  const [theme, setTheme] = useState(params.get("theme") || "light");
  const [surface, setSurface] = useState(params.get("surface") || "stream");
  const turns = surface === "images" ? mediaTurns : conversationTurns;
  const [live, setLive] = useState(params.has("lateTerminal"));
  const [playing, setPlaying] = useState(false);
  const [run, setRun] = useState(0);
  const key = `reading-fixture-${run}`;
  useEffect(() => applyMessageFlowFontSize(size), [size]);
  useEffect(() => { document.documentElement.dataset.theme = theme; }, [theme]);
  useEffect(() => {
    if (!playing) return;
    let offset = 0;
    streamTextStore.seed(key, "");
    setLive(true);
    const timer = window.setInterval(() => {
      const chunk = sample.slice(offset, offset + 60);
      streamTextStore.append(key, chunk);
      offset += chunk.length;
      if (offset >= sample.length) { setLive(false); setPlaying(false); }
    }, 70);
    return () => window.clearInterval(timer);
  }, [key, playing]);
  const markdown = surface === "stream"
    ? <StreamingMarkdown key={key} streamKey={key} initialText={playing ? "" : sample} isLive={live} phase="final_answer" />
    : <RichContent text={surface === "user" ? userSample : sample} />;
  return <ImagePreviewProvider>
    <header className="fixture-toolbar">
      <strong>Wuu · 消息流排版验收</strong>
      <label>字号<select aria-label="字号" value={size} onChange={event => setSize(Number(event.target.value))}>{[MESSAGE_FLOW_FONT_SIZE_RANGE.min, 14, 14.5, 16, 18, MESSAGE_FLOW_FONT_SIZE_RANGE.max].map(value => <option key={value}>{value}</option>)}</select></label>
      <label>主题<select aria-label="主题" value={theme} onChange={event => setTheme(event.target.value)}><option value="light">浅色</option><option value="dark">深色</option></select></label>
      <label>渲染方式<select aria-label="渲染方式" value={surface} onChange={event => setSurface(event.target.value)}><option value="conversation">完整对话</option><option value="images">消息图片</option><option value="lifecycle">回复完成与动作占位</option><option value="stream">流式分块</option><option value="rich">普通 Markdown</option><option value="chat">聊天气泡</option><option value="user">用户消息</option><option value="edit">编辑用户消息</option><option value="workspace">文件预览</option><option value="failures">失败与中断</option></select></label>
      <label><input aria-label="流式状态" type="checkbox" checked={live} onChange={event => setLive(event.target.checked)} />显示流式光标</label>
      <button type="button" disabled={playing || surface !== "stream"} onClick={() => { setRun(value => value + 1); setPlaying(true); }}>逐段播放</button>
    </header>
    <main className="conversation-pane fixture-pane">
      <div className={`fixture-column${(surface === "conversation" || surface === "images" || surface === "failures") ? " fixture-production-turns" : ""}`}>
        {surface === "failures" ? <FailureSurface /> : surface === "conversation" || surface === "images" ? <WuuUIRoot>{turns.map((turn, index) => <TurnView key={turn.id} turn={turn} onStreamFrame={() => {}} isLatestTurn={index === turns.length - 1} latestAgentMessageID={surface === "images" ? "media-answer" : "sample-short"} />)}</WuuUIRoot> : <>
        <section className="turn">
          <div className="message user-message">请用一组包含段落、分点和嵌套列表的内容，检查消息流的阅读节奏。</div>
          <div className="fixture-process turn-process-entry">已完成 3 项操作 · 排版验收示例</div>
          {surface === "lifecycle" ? <WuuUIRoot><TurnView
            turn={{ id: "fixture-lifecycle", status: live ? "in_progress" : "completed", started_at: "2026-09-17T06:00:00Z", answer_ready_at: live ? undefined : "2026-09-17T06:00:06Z", items_view: "full", items: [
              { id: "fixture-request", type: "user_message", status: "completed", text: "检查回复完成后的空白。" },
              { id: "fixture-answer", type: "agent_message", terminal: live && params.has("lateTerminal") ? undefined : true, status: live ? "in_progress" : "completed", text: params.has("lateTerminal") ? Array(4).fill("在技术演进与协作模式持续变迁的当下，软件工程的重心正逐渐从单纯的代码编写转向更高维度的系统设计、上下文维护与心智模型对齐。现代开发工作流不再是由孤立的函数或离散的代码提交拼凑而成，而是一个需要持续演进、自我校准并包容复杂性的动态生态系统。").join("\n\n") : "这段回答结束后，操作出现，但下一条消息的位置不应变化。" },
            ] }}
            isLatestTurn latestAgentMessageID="fixture-answer"
            streamStatus={live && params.has("notice") ? { text: "连接恢复提示需要在窄窗口和大字号下自然换行，而不是用一个固定高度的空白代替。", liveProgress: false } : undefined}
            onStreamFrame={() => {}}
          /></WuuUIRoot> : surface === "edit" ? <WuuUIRoot><ThreadItemView
            turnID="fixture-turn" turnStatus="completed" streaming={false}
            item={{ id: "fixture-user", type: "user_message", status: "completed", text: "编辑这条消息，检查文字、光标和附件的可读性。", files: [{ filename: "example.pdf", media_type: "application/pdf", data: "" }] }}
            editing onStreamFrame={() => {}} onCancelEditMessage={() => setSurface("user")}
          /></WuuUIRoot> : <article className={surface === "workspace" ? "workspace-markdown-reading" : surface === "chat" ? "chat-bubble" : surface === "user" ? "message user-message" : "agent-block"} data-testid="reading-answer">{markdown}</article>}
        </section>
        <section className="turn" data-testid="following-turn"><div className="message user-message">简短回答也检查一下。</div><article className="agent-block"><RichContent text="短回答直接开始正文，不需要额外标题。" /></article></section>
        </>}
      </div>
    </main>
  </ImagePreviewProvider>;
}

if (import.meta.env.DEV) createRoot(document.getElementById("root")!).render(<Fixture />);
