/**
 * Tests for StreamingMarkdown.
 *
 * Contract: render the markdown progressively, show a 1px cursor at
 * the end during streaming, and fade the cursor out after settle
 * without removing or remounting the Markdown tail.
 */
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
  containsMermaidFence,
  StreamingMarkdown,
  splitIntoStableBlocks,
  splitStreamWords,
} from "./StreamingMarkdown";
import {
  STREAM_TEXT_NOTIFY_INTERVAL_MS,
  streamTextKey,
  streamTextStore,
} from "./StreamText";

// jsdom doesn't implement layout. Stub getBoundingClientRect so React
// doesn't crash on layout queries.
beforeAll(() => {
  Element.prototype.getBoundingClientRect = function (): DOMRect {
    return {
      x: 0, y: 0, top: 0, left: 0, right: 0, bottom: 0,
      width: 0, height: 0,
      toJSON() { return this; }
    } as DOMRect;
  };
});

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(props: Parameters<typeof StreamingMarkdown>[0]): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<StreamingMarkdown {...props} />);
  });
}

function rerender(props: Parameters<typeof StreamingMarkdown>[0]): void {
  act(() => {
    root!.render(<StreamingMarkdown {...props} />);
  });
}

function unmount(): void {
  if (root) {
    act(() => {
      root!.unmount();
    });
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
}

afterEach(() => {
  unmount();
  for (const itemID of ["s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10", "s11", "s12", "s13"]) {
    streamTextStore.clearItem("turn", itemID);
  }
});

describe("StreamingMarkdown", () => {
  it("renders strong markers next to Chinese prose and opening punctuation", async () => {
    const key = streamTextKey("turn", "s1", "text");
    const text = "但**「多想的意愿」来自 RL 训练，「预算」是工程兜底**——所以不是换了一句话。";
    streamTextStore.seed(key, text);
    mount({ streamKey: key, initialText: text, isLive: false, phase: "final_answer" });

    await act(async () => {
      await Promise.resolve();
    });

    const paragraph = document.querySelector(".rich-paragraph");
    const strong = paragraph?.querySelector("strong");
    expect(paragraph?.textContent).toBe("但「多想的意愿」来自 RL 训练，「预算」是工程兜底——所以不是换了一句话。");
    expect(strong?.textContent).toBe("「多想的意愿」来自 RL 训练，「预算」是工程兜底");
    expect(paragraph?.textContent).not.toContain("**");
  });

  it("keeps bold CJK prose with inline code parsed after the stream settles", async () => {
    const key = streamTextKey("turn", "s2", "text");
    const text =
      "**不是让 `apply_patch` 模仿现有 `edit_file` 的裁剪行为，而是让整个 edit 工具族一起采用 Codex 式分轨结果设计。**";
    streamTextStore.seed(key, text);
    mount({ streamKey: key, initialText: text, isLive: false, phase: "final_answer" });

    await act(async () => {
      await Promise.resolve();
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const paragraph = surface.querySelector(".rich-paragraph");
    const strong = paragraph?.querySelector("strong");
    expect(paragraph?.textContent).toBe(
      "不是让 apply_patch 模仿现有 edit_file 的裁剪行为，而是让整个 edit 工具族一起采用 Codex 式分轨结果设计。",
    );
    expect(strong?.textContent).toBe(paragraph?.textContent);
    expect(Array.from(strong?.querySelectorAll("code") ?? []).map((code) => code.textContent)).toEqual([
      "apply_patch",
      "edit_file",
    ]);
  });

  it("keeps a terminal fenced code block closed when adding the stream cursor", () => {
    const key = streamTextKey("turn", "s10", "text");
    const text = "重启开发环境：\n\n```bash\ncd desktop\nnpm run dev\n```";
    streamTextStore.seed(key, text);
    mount({ streamKey: key, initialText: text, isLive: false, phase: "final_answer" });

    const code = document.querySelector(".rich-code-block code");
    expect(code?.textContent).toBe("cd desktop\nnpm run dev\n");
    expect(code?.textContent).not.toContain("```");
  });

  it("leaves no trailing cursor paragraph under a fence-final message after settle", () => {
    const key = streamTextKey("turn", "s11", "text");
    const text = "```text\n请使用 web-shader-extractor skill\n```";
    streamTextStore.seed(key, text);
    mount({ streamKey: key, initialText: text, isLive: false, phase: "final_answer" });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    // The fence is the only markdown block: no stray trailing paragraph may
    // survive between the card and the message action bar.
    expect(surface.querySelector(".rich-code-block")).toBeTruthy();
    expect(surface.querySelector(".rich-paragraph")).toBeNull();
    expect(surface.textContent).not.toContain("\uE000");
    // The cursor keeps its settle-stable DOM slot as a zero-height sibling.
    const cursor = surface.querySelector(".stream-cursor") as HTMLElement | null;
    expect(cursor).not.toBeNull();
    expect(cursor?.classList.contains("stream-cursor-block-tail")).toBe(true);
    expect(cursor?.parentElement).toBe(surface);
  });

  it("keeps the block-tail cursor sibling out of the markdown source while live", async () => {
    const key = streamTextKey("turn", "s12", "text");
    streamTextStore.seed(key, "```bash\nnpm run dev\n```");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 300));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const code = surface.querySelector(".rich-code-block code");
    expect(code?.textContent).toBe("npm run dev\n");
    expect(surface.querySelector(".rich-paragraph")).toBeNull();
    const cursor = surface.querySelector(".stream-cursor") as HTMLElement | null;
    expect(cursor?.classList.contains("stream-cursor-block-tail")).toBe(true);
  });

  it.each(["\n", "\r\n", "\n\n", "\n\n\n", "\n\n\n\n\n"])("keeps the cursor on visible prose with trailing blank lines %j", (ending) => {
    const key = streamTextKey("turn", "s13", "text");
    const text = `好的，当前在 \`main\` 分支。我现在启动 dev 环境。${ending}`;
    streamTextStore.seed(key, text);
    mount({ streamKey: key, initialText: text, isLive: false, phase: "commentary" });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const paragraphs = surface.querySelectorAll(".rich-paragraph");
    expect(paragraphs).toHaveLength(1);
    expect(paragraphs[0].textContent).toBe("好的，当前在 main 分支。我现在启动 dev 环境。");

    const cursor = surface.querySelector(".stream-cursor") as HTMLElement | null;
    expect(cursor?.classList.contains("stream-cursor-block-tail")).toBe(false);
    expect(cursor?.closest(".rich-paragraph")).toBe(paragraphs[0]);
    expect(Array.from(surface.children).filter((child) => !child.textContent?.trim())).toHaveLength(0);
  });

  it("does not add empty paragraphs as blank lines arrive between streamed updates", () => {
    const props = {
      streamKey: streamTextKey("turn", "blank-line-updates", "text"),
      initialText: "第一组验证通过。\n\n",
      isLive: true,
      phase: "commentary" as const,
    };
    mount(props);
    for (const initialText of [
      "第一组验证通过。\n\n\n\n",
      "第一组验证通过。\n\n\n\n正在检查桌面接入。\n\n\n",
    ]) {
      rerender({ ...props, initialText });
      const surface = container!.querySelector(".streaming-markdown")!;
      expect(surface.querySelector(".rich-paragraph")?.textContent).toBe("第一组验证通过。");
      expect(Array.from(surface.children).every((child) => Boolean(child.textContent?.trim()))).toBe(true);
      const paragraphs = surface.querySelectorAll(".rich-paragraph");
      expect(surface.querySelector(".stream-cursor")?.closest(".rich-paragraph")).toBe(paragraphs[paragraphs.length - 1]);
    }
  });

  it("renders the visible text as markdown during streaming", async () => {
    const key = streamTextKey("turn", "s1", "text");
    streamTextStore.seed(key, "");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      streamTextStore.append(key, "**hi**");
      // Wait for full reveal: ~3 frames at 2 chars/frame in jsdom.
      await new Promise((resolve) => setTimeout(resolve, 300));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    expect(surface).toBeTruthy();
    const strong = surface.querySelector("strong");
    expect(strong?.textContent).toBe("hi");
  });

  it("keeps paragraph and its no-blank-line list as siblings inside one stable block", async () => {
    const key = streamTextKey("turn", "s9", "text");
    // Paragraph directly followed by a list (single newline) parses as
    // two block siblings but lands in ONE stable block wrapper — the
    // wrapper's adjacency rules must space them just like separate blocks.
    streamTextStore.seed(key, "分类如下：\n- 只读：a\n- 可编辑：b\n\n后续段落。\n\n");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 500));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const wrappers = surface.querySelectorAll(".streaming-markdown-block");
    expect(wrappers.length).toBeGreaterThanOrEqual(1);
    const first = wrappers[0] as HTMLElement;
    expect(first.querySelector(".rich-paragraph")).toBeTruthy();
    expect(first.querySelector("ul")).toBeTruthy();
  });

  it("renders markdown headings as whisper-level rich-heading anchors", async () => {
    const key = streamTextKey("turn", "s10", "text");
    streamTextStore.seed(key, "");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      streamTextStore.append(key, "## 方案\n\n正文段落");
      await new Promise((resolve) => setTimeout(resolve, 500));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const heading = surface.querySelector(".rich-heading");
    expect(heading?.textContent).toBe("方案");
    // Keep the existing heading DOM contract; message and file surfaces
    // apply their own visual hierarchy through the level modifier classes.
    expect(surface.querySelector("h1, h2, h3, h4, h5, h6")).toBeNull();
  });

  it("keeps a loose list's paragraphs and numbering together across streamed blank lines", async () => {
    const key = streamTextKey("turn", "s13", "text");
    const prefix = "9. 第一项\n\n";
    const continuation = "   同一项的第二段。\n\n10. 第二项\n\n## 下一节\n\n正文";
    streamTextStore.seed(key, prefix);
    mount({ streamKey: key, initialText: prefix, isLive: true, phase: "final_answer" });
    await act(async () => {
      streamTextStore.append(key, continuation);
      await new Promise(resolve => setTimeout(resolve, STREAM_TEXT_NOTIFY_INTERVAL_MS + 30));
    });
    const list = container!.querySelector("ol")!;
    expect(container!.querySelectorAll("ol")).toHaveLength(1);
    expect(list.getAttribute("start")).toBe("9");
    expect(list.children).toHaveLength(2);
    expect(list.children[0].querySelectorAll("p")).toHaveLength(2);
    expect(list.children[0].textContent).toContain("同一项的第二段。");
    const paragraphs = Array.from(list.children[0].querySelectorAll("p"));
    rerender({ streamKey: key, initialText: prefix + continuation, isLive: false, phase: "final_answer" });
    expect(container!.querySelector("ol")).toBe(list);
    expect(Array.from(list.children[0].querySelectorAll("p"))).toEqual(paragraphs);
  });

  it("shows a 1px cursor span during streaming", async () => {
    const key = streamTextKey("turn", "s2", "text");
    streamTextStore.seed(key, "Hello world");
    mount({ streamKey: key, initialText: "Hello", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const cursor = surface.querySelector(".stream-cursor") as HTMLElement | null;
    expect(cursor).toBeTruthy();
    expect(cursor?.tagName).toBe("SPAN");
    expect(cursor?.closest(".rich-paragraph")).toBeTruthy();
  });

  it("keeps streamed body text fully legible beside the cursor", async () => {
    const key = streamTextKey("turn", "s2", "text");
    streamTextStore.seed(key, "Hello world");
    mount({ streamKey: key, initialText: "Hello", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    expect(surface.textContent).toContain("Hello world");
    expect(surface.querySelector(".stream-feather-enter")).toBeNull();
    expect(surface.querySelector(".stream-cursor")).not.toBeNull();
  });

  it("keeps existing text stable when an inline Markdown delimiter closes", async () => {
    const key = streamTextKey("turn", "s2", "text");
    streamTextStore.seed(key, "**bold");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 40));
      streamTextStore.append(key, "**");
      await new Promise((resolve) =>
        setTimeout(resolve, STREAM_TEXT_NOTIFY_INTERVAL_MS + 20),
      );
    });

    expect(document.querySelector("strong")).not.toBeNull();
  });

  it("uses the same live cursor treatment for commentary text", async () => {
    const key = streamTextKey("turn", "s8", "text");
    streamTextStore.seed(key, "Working through it");
    mount({ streamKey: key, initialText: "Working", isLive: true, phase: "commentary" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 80));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    expect(surface.classList.contains("streaming-commentary-live")).toBe(false);
    expect(surface.querySelector(".stream-cursor")).toBeTruthy();
  });

  it("does not use a clip-path mask (no .streaming-cover)", async () => {
    const key = streamTextKey("turn", "s3", "text");
    streamTextStore.seed(key, "Hello world");
    mount({ streamKey: key, initialText: "Hello", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 80));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    expect(surface.querySelector(".streaming-cover")).toBeNull();
  });

  it("renders the full text immediately when not live", () => {
    const key = streamTextKey("turn", "s4", "text");
    streamTextStore.seed(key, "Hello world");
    mount({ streamKey: key, initialText: "Hello world", isLive: false, phase: "final_answer" });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    expect(surface.textContent).toContain("Hello world");
  });

  it("notifies a frame when non-live text snaps to its final length", async () => {
    const key = streamTextKey("turn", "s4", "text");
    streamTextStore.seed(key, "Hello world after completion");
    let frameCount = 0;
    mount({
      streamKey: key,
      initialText: "",
      isLive: false,
      phase: "final_answer",
      onFrame: () => {
        frameCount += 1;
      },
    });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(frameCount).toBeGreaterThan(0);
  });

  it("hides the settled cursor from the parent state without changing the cursor node class", async () => {
    const key = streamTextKey("turn", "s5", "text");
    streamTextStore.seed(key, "Hello world");
    mount({ streamKey: key, initialText: "Hello world", isLive: false, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const cursor = surface.querySelector(".stream-cursor");
    // Cursor must stay in the DOM with a stable class so hiding it does
    // not reparse or remount the Markdown tail.
    expect(cursor).not.toBeNull();
    expect(cursor?.className).toBe("stream-cursor");
    expect(surface.dataset.cursorState).toBe("fading");
  });

  it("notifies once when isLive flips off and the cursor is caught up", async () => {
    const key = streamTextKey("turn", "s6", "text");
    streamTextStore.set(key, "Done");
    let settledCount = 0;
    mount({
      streamKey: key,
      initialText: "",
      isLive: false,
      phase: "final_answer",
      onSettled: () => {
        settledCount += 1;
      }
    });

    // isLive=false immediately calls syncImmediate + trySettle, so the
    // settled callback fires synchronously in the mount's commit pass.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    expect(settledCount).toBe(1);
  });

  it("keeps completed visible text when the stream cache is released before an older parent snapshot updates", async () => {
    const key = streamTextKey("turn", "s9", "text");
    streamTextStore.set(key, "Complete answer text");
    mount({ streamKey: key, initialText: "Complete", isLive: false, phase: "final_answer" });

    expect(document.querySelector(".streaming-markdown")?.textContent).toContain(
      "Complete answer text",
    );

    streamTextStore.clearItem("turn", "s9");
    rerender({ streamKey: key, initialText: "Complete", isLive: false, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(document.querySelector(".streaming-markdown")?.textContent).toContain(
      "Complete answer text",
    );
  });

  it("accepts shorter text when the replacement comes from the stream store", async () => {
    const key = streamTextKey("turn", "s10", "text");
    streamTextStore.set(key, "stale partial answer");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 300));
    });
    expect(document.querySelector(".streaming-markdown")?.textContent).toContain(
      "stale partial answer",
    );

    await act(async () => {
      streamTextStore.replace(key, "");
      streamTextStore.append(key, "fresh");
      await new Promise((resolve) => setTimeout(resolve, 300));
    });

    expect(document.querySelector(".streaming-markdown")?.textContent).toContain(
      "fresh",
    );
    expect(document.querySelector(".streaming-markdown")?.textContent).not.toContain(
      "stale partial answer",
    );
  });

  it("keeps the cursor inside a streaming list item", async () => {
    const key = streamTextKey("turn", "s7", "text");
    streamTextStore.seed(key, "- first item");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 300));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    const cursor = surface.querySelector(".stream-cursor") as HTMLElement | null;
    expect(surface.textContent).not.toContain("\uE000");
    expect(cursor?.closest("li")).toBeTruthy();
  });

  it("wraps live streamed words in arrival spans and settles to plain text", async () => {
    const key = streamTextKey("turn", "s-words", "text");
    streamTextStore.seed(key, "");
    mount({ streamKey: key, initialText: "", isLive: true, phase: "final_answer" });

    await act(async () => {
      streamTextStore.append(key, "你好 world");
      await new Promise((resolve) => setTimeout(resolve, 300));
    });

    const surface = document.querySelector(".streaming-markdown") as HTMLElement;
    // CJK and Latin words both ride the arrival animation while live.
    expect(surface.querySelectorAll(".stream-word").length).toBeGreaterThanOrEqual(2);
    // Word spans never alter the visible text.
    expect(surface.textContent).toContain("你好 world");

    rerender({ streamKey: key, initialText: "", isLive: false, phase: "final_answer" });
    expect(surface.querySelector(".stream-word")).toBeNull();
    expect(surface.textContent).toContain("你好 world");
  });
});

describe("containsMermaidFence", () => {
  it("matches only Mermaid fenced code blocks", () => {
    expect(containsMermaidFence("```mermaid\ngraph TD\n```")).toBe(true);
    expect(containsMermaidFence("intro\n\n``` mermaid \ngraph TD\n```")).toBe(true);
    expect(containsMermaidFence("```ts\nconst mermaid = true;\n```")).toBe(false);
    expect(containsMermaidFence("plain text mentioning mermaid")).toBe(false);
  });
});

describe("splitIntoStableBlocks", () => {
  it.each(["\n", "\r\n"])("defers list boundaries through indented paragraphs and nested lists (%j)", newline => {
    const list = ["- first", "", "  second paragraph", "", "  - nested", "", "    nested continuation", "- next", "", ""].join(newline);
    const text = `intro${newline}${newline}${list}## after${newline}tail`;
    const result = splitIntoStableBlocks(text);
    expect(result.blocks).toEqual([`intro${newline}${newline}`, list]);
    expect(result.tail).toBe(`## after${newline}tail`);
    expect(result.blocks.join("") + result.tail).toBe(text);
  });

  it("does not freeze a list when a continuation may still arrive", () => {
    const text = "- first\n\n  \n";
    expect(splitIntoStableBlocks(text)).toEqual({ blocks: [], tail: text });
  });

  it("preserves loose numbered items separated by blank lines", () => {
    const list = "9. first\n\n   continuation\n\n10. next\n\n";
    expect(splitIntoStableBlocks(list + "after\n")).toEqual({ blocks: [list], tail: "after\n" });
  });

  it("uses tab stops when checking list continuation indentation", () => {
    const list = "-\tfirst\n\n\tcontinuation\n\n";
    expect(splitIntoStableBlocks(list + "after\n")).toEqual({ blocks: [list], tail: "after\n" });
  });

  it("keeps a fenced code block inside its list item", () => {
    const list = "- example\n\n  ```ts\n  const a = 1;\n\n  const b = 2;\n  ```\n\n- next\n\n";
    expect(splitIntoStableBlocks(list + "after\n")).toEqual({ blocks: [list], tail: "after\n" });
  });

  it("returns the whole text as tail when there are no blank lines", () => {
    const result = splitIntoStableBlocks("a single paragraph still typing");
    expect(result.blocks).toEqual([]);
    expect(result.tail).toBe("a single paragraph still typing");
  });

  it("splits each `\\n\\n`-separated paragraph into its own block", () => {
    const text = "first paragraph\n\nsecond paragraph\n\nthird in progress";
    const result = splitIntoStableBlocks(text);
    expect(result.blocks).toEqual([
      "first paragraph\n\n",
      "second paragraph\n\n"
    ]);
    expect(result.tail).toBe("third in progress");
  });

  it("keeps an unclosed fenced code block in the tail", () => {
    // A `\n\n` inside an open ``` fence must NOT be a boundary —
    // the block isn't yet stable.
    const text =
      "intro paragraph\n\n```ts\nconst a = 1;\n\nconst b = 2;\nstill typing";
    const result = splitIntoStableBlocks(text);
    expect(result.blocks).toEqual(["intro paragraph\n\n"]);
    expect(result.tail).toBe(
      "```ts\nconst a = 1;\n\nconst b = 2;\nstill typing"
    );
  });

  it("treats a closed fenced code block as a single stable block", () => {
    const text =
      "intro\n\n```ts\nconst a = 1;\n\nconst b = 2;\n```\n\nafter";
    const result = splitIntoStableBlocks(text);
    expect(result.blocks).toEqual([
      "intro\n\n",
      "```ts\nconst a = 1;\n\nconst b = 2;\n```\n\n"
    ]);
    expect(result.tail).toBe("after");
  });

  it("ignores backticks that aren't at the start of a line", () => {
    // A ``` mid-line is part of inline content (or a heading), not a
    // fence opener.
    const text = "use the ```triple backtick``` syntax\n\ntail";
    const result = splitIntoStableBlocks(text);
    expect(result.blocks).toEqual(["use the ```triple backtick``` syntax\n\n"]);
    expect(result.tail).toBe("tail");
  });

  it("preserves the concatenation invariant: blocks + tail === text", () => {
    const text =
      "# heading\n\nparagraph one\n\n```ts\nfn()\n```\n\n- list item\n- another\n\ntail";
    const result = splitIntoStableBlocks(text);
    expect(result.blocks.join("") + result.tail).toBe(text);
  });

  it("does not split a fence when a body line starts with 3 backticks followed by other text", () => {
    // Per CommonMark a closing code fence is just backticks and trailing
    // whitespace. A line like ```other inside an already-open fence is
    // body content, not a closer — treating it as one would flip
    // inFence to false and let a later blank line split the fence in two.
    const text = "```ts\n```other\n\ncode\n```";
    const result = splitIntoStableBlocks(text);
    expect(result.blocks).toEqual([]);
    expect(result.tail).toBe(text);
  });

  it("does treat a bare ``` line as a closer when no content follows the backticks", () => {
    // Sanity check that the closer-validation guard above does not
    // regress the standard case: a fence followed by a line of just
    // three backticks must still close.
    const text = "```ts\ncode\n```\n\nafter";
    const result = splitIntoStableBlocks(text);
    expect(result.blocks).toEqual(["```ts\ncode\n```\n\n"]);
    expect(result.tail).toBe("after");
  });
});

describe("splitStreamWords", () => {
  it("splits CJK and Latin text into word-shaped segments without losing a character", () => {
    const text = "你好 world，这 is 混排";
    const segments = splitStreamWords(text);
    // Concatenation invariant: segments always rebuild the exact input.
    expect(segments.map((segment) => segment.text).join("")).toBe(text);
    // CJK prose rides the animation as word segments, not a raw text run.
    expect(
      segments
        .filter((segment) => segment.word)
        .map((segment) => segment.text)
        .join("")
    ).toContain("你好");
    expect(
      segments
        .filter((segment) => segment.word)
        .map((segment) => segment.text)
        .join("")
    ).toContain("world");
  });

  it("keeps whitespace as plain segments so spacing stays pixel-exact", () => {
    const segments = splitStreamWords("你好 world");
    expect(segments.some((segment) => !segment.word && segment.text === " ")).toBe(
      true
    );
  });

  it("handles empty and whitespace-only input", () => {
    expect(splitStreamWords("")).toEqual([]);
    expect(
      splitStreamWords("  ").every((segment) => !segment.word)
    ).toBe(true);
  });
});
