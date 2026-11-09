/**
 * Stream update behavior for StreamingMarkdown.
 *
 * The streaming surface must render provider chunks directly. It must not
 * manufacture a second RAF loop that advances a few visible characters per
 * frame after the provider text has already arrived. That old character
 * chase created thousands of React/Markdown renders for one long answer and
 * starved input such as the interrupt button. The contract enforced here:
 *
 *   - a long buffered answer needs no artificial follow-up animation frames
 *   - the full buffered answer renders immediately
 */
import { afterEach, describe, expect, it } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { StreamingMarkdown } from "./StreamingMarkdown";
import { streamTextKey, streamTextStore } from "./StreamText";

const sectionTemplate = `## 标题 N

这是一个 \`useEffect\` 段落，包含 **粗体**、*斜体* 以及 [链接](https://example.com)。
依赖数组决定了 effect 何时重新执行。

\`\`\`ts
useEffect(() => {
  const id = setInterval(() => count++, 1000);
  return () => clearInterval(id);
}, []);
\`\`\`

- 第一项：列表里再嵌一段 \`code\`
- 第二项：lint 会警告依赖
- 第三项：每次都是新的引用

> 引用块：99% 的问题都是"我以为它没变，其实它变了"。

`;

// Long-ish answer (~12 000 chars) — well past anything a typical
// response would produce. If this stays smooth, shorter answers are
// trivially fine.
const longText = Array.from({ length: 40 }, (_, i) =>
  sectionTemplate.replace("N", String(i + 1))
).join("");

let root: Root | null = null;
let container: HTMLDivElement | null = null;

afterEach(() => {
  if (root) {
    act(() => { root!.unmount(); });
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
  streamTextStore.clearItem("turn", "perf");
});

describe("StreamingMarkdown frame loop", () => {
  it("does not create a character-chase frame loop for long answers", () => {
    const key = streamTextKey("turn", "perf", "text");
    streamTextStore.seed(key, longText);
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    // Capture any scheduled frame. Provider text must render directly without
    // manufacturing a second animation loop.
    const realRAF = window.requestAnimationFrame;
    const pending: FrameRequestCallback[] = [];
    let nextHandle = 1;
    window.requestAnimationFrame = ((cb: FrameRequestCallback) => {
      pending.push(cb);
      return nextHandle++;
    }) as typeof window.requestAnimationFrame;

    act(() => {
      root!.render(
        <StreamingMarkdown
          streamKey={key}
          initialText=""
          isLive={true}
          phase="final_answer"
        />
      );
    });

    window.requestAnimationFrame = realRAF;

    expect(pending).toHaveLength(0);
    expect(container.textContent).toContain("标题 40");
  });
});
