import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { RichContent } from "../../src/renderer/RichContent";
import { StreamingMarkdown } from "../../src/renderer/StreamingMarkdown";
import { streamTextStore } from "../../src/renderer/StreamText";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";
import "./fixture.css";

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

function Fixture(): JSX.Element {
  const [size, setSize] = useState<number>(MESSAGE_FLOW_FONT_SIZE_RANGE.default);
  const [theme, setTheme] = useState("light");
  const [surface, setSurface] = useState("stream");
  const [live, setLive] = useState(false);
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
    : <RichContent text={sample} />;
  return <ImagePreviewProvider>
    <header className="fixture-toolbar">
      <strong>Wuu · 消息流排版验收</strong>
      <label>字号<select aria-label="字号" value={size} onChange={event => setSize(Number(event.target.value))}>{[MESSAGE_FLOW_FONT_SIZE_RANGE.min, 14, 16, 18, MESSAGE_FLOW_FONT_SIZE_RANGE.max].map(value => <option key={value}>{value}</option>)}</select></label>
      <label>主题<select aria-label="主题" value={theme} onChange={event => setTheme(event.target.value)}><option value="light">浅色</option><option value="dark">深色</option></select></label>
      <label>渲染方式<select aria-label="渲染方式" value={surface} onChange={event => setSurface(event.target.value)}><option value="stream">流式分块</option><option value="rich">普通 Markdown</option><option value="chat">聊天气泡</option><option value="workspace">文件预览</option></select></label>
      <label><input type="checkbox" checked={live} onChange={event => setLive(event.target.checked)} />显示流式光标</label>
      <button type="button" disabled={playing || surface !== "stream"} onClick={() => { setRun(value => value + 1); setPlaying(true); }}>逐段播放</button>
    </header>
    <main className="conversation-pane fixture-pane">
      <div className="fixture-column">
        <section className="turn">
          <div className="message user-message">请用一组包含段落、分点和嵌套列表的内容，检查消息流的阅读节奏。</div>
          <div className="fixture-process turn-process-entry">已完成 3 项操作 · 排版验收示例</div>
          <article className={surface === "workspace" ? "workspace-markdown-reading" : surface === "chat" ? "chat-bubble" : "agent-block"} data-testid="reading-answer">{markdown}</article>
        </section>
        <section className="turn"><div className="message user-message">简短回答也检查一下。</div><article className="agent-block"><RichContent text="短回答直接开始正文，不需要额外标题。" /></article></section>
      </div>
    </main>
  </ImagePreviewProvider>;
}

if (import.meta.env.DEV) createRoot(document.getElementById("root")!).render(<Fixture />);
