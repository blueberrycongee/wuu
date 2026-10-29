import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceFileReferenceResolveResult } from "../shared/protocol";
import { ImagePreviewProvider } from "./ImagePreview";
import { ArtifactsProvider } from "./ArtifactsContext";
import { ApplicationArtifacts } from "./plugins/ApplicationArtifacts";
import { MarkdownContent, RichContent } from "./RichContent";
import { BrowserControlsProvider } from "./BrowserControlsContext";
import { PreviewContentProvider } from "./PreviewContentContext";
import { ApplicationBrowser } from "./plugins/ApplicationBrowser";
import { ApplicationPreviewContent } from "./plugins/ApplicationPreviewContent";
import { StreamingMarkdown } from "./StreamingMarkdown";

const { mermaidRender } = vi.hoisted(() => ({
  mermaidRender: vi.fn(async (_code: string, options: { idPrefix: string }) => ({
    svg: `<svg data-diagram-id="${options.idPrefix}"></svg>`,
  })),
}));

vi.mock("pele", () => ({ render: mermaidRender }));

let container: HTMLDivElement;
let root: Root | null = null;
let writeTextMock: ReturnType<typeof vi.fn>;
let resolveWorkspaceFileReferenceMock: ReturnType<typeof vi.fn>;
let openExternalMock: ReturnType<typeof vi.fn>;
let openBrowserMock: ReturnType<typeof vi.fn>;
let browser: ApplicationBrowser;
let fileService: ApplicationPreviewContent;
let openFileMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  document.documentElement.dataset.theme = "light";
  mermaidRender.mockReset();
  mermaidRender.mockImplementation(async (_code, options) => ({ svg: `<svg data-diagram-id="${options.idPrefix}"></svg>` }));
  writeTextMock = vi.fn().mockResolvedValue(undefined);
  // jsdom does not implement the clipboard API; inject a mock for the
  // success-path tests.
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: writeTextMock }
  });
  resolveWorkspaceFileReferenceMock = vi.fn(async (reference: string): Promise<WorkspaceFileReferenceResolveResult> => ({
    root: "/repo/wuu",
    reference,
    status: "missing",
  }));
  openExternalMock = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      openExternal: openExternalMock,
      resolveWorkspaceFileReference: resolveWorkspaceFileReferenceMock,
    },
  });
  openBrowserMock = vi.fn();
  browser = new ApplicationBrowser({ api: window.wuu, onActivityChanged: vi.fn(), workspaceNavigation: {
    enabled: true, getFocusState: () => ({ rightPanelOpen: false, browserForegroundOccupied: false }), openBrowser: openBrowserMock,
  } });
  openFileMock = vi.fn();
  fileService = new ApplicationPreviewContent(window.wuu, { openFile: openFileMock });
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  browser.dispose();
  container.remove();
  delete (window as { wuu?: unknown }).wuu;
  delete document.documentElement.dataset.theme;
});

function render(element: JSX.Element, fileCapability = true): void {
  act(() => {
    if (!root) {
      root = createRoot(container);
    }
    root.render(<BrowserControlsProvider service={browser}>{fileCapability ? <PreviewContentProvider service={fileService}>{element}</PreviewContentProvider> : element}</BrowserControlsProvider>);
  });
}

function renderWithImagePreview(element: JSX.Element): void {
  render(<ArtifactsProvider application={{ artifacts: new ApplicationArtifacts(window.wuu, fileService) }}>
    <ImagePreviewProvider>{element}</ImagePreviewProvider>
  </ArtifactsProvider>);
}

async function settleFileReferenceResolution(): Promise<void> {
  for (let index = 0; index < 3; index += 1) {
    await act(async () => {
      await Promise.resolve();
    });
  }
}

function resolvedFileReference(
  reference: string,
  path: string,
): WorkspaceFileReferenceResolveResult {
  return {
    root: "/repo/wuu",
    reference,
    status: "resolved",
    path,
    absolute_path: `/repo/wuu/${path}`,
  };
}

describe("RichContent code block", () => {
  it.each([
    "价格 $100，$200。",
    "价格 $100、$200。",
    "价格 $100；$200。",
    "Prices: $100; $200",
    "Prices: $100 & $200",
    "Prices: $100 vs. $200",
    "Total: $100 + $200",
  ])("preserves amounts separated by punctuation (%#)", text => {
    render(<RichContent text={text} />);
    expect(container.querySelector(".katex")).toBeNull();
    expect(container.querySelector(".rich-paragraph")?.textContent).toBe(text);
  });

  it("preserves numeric math expressions adjacent to a following number", () => {
    render(<RichContent text={String.raw`$2x+1$3 and $2\text{ dogs}$4`} />);
    expect(Array.from(container.querySelectorAll(".katex annotation"), node => node.textContent)).toEqual([
      "2x+1", String.raw`2\text{ dogs}`,
    ]);
  });

  it.each(["message", "markdown"])("keeps dollar amounts as text without swallowing surrounding Markdown in %s", (mode) => {
    const text = "**最高 $500/月 API 额度**\n套餐对应 $100/$200/$500，月上限 $500。";
    const element = mode === "message" ? <RichContent text={text} /> : <MarkdownContent text={text} />;
    render(element);

    expect(container.querySelector(".katex")).toBeNull();
    expect(container.querySelector("strong")?.textContent).toBe("最高 $500/月 API 额度");
    expect(container.querySelector(".rich-paragraph")?.textContent).toBe(
      "最高 $500/月 API 额度\n套餐对应 $100/$200/$500，月上限 $500。",
    );
  });

  it("distinguishes dollar amounts from adjacent mathematical expressions and code", () => {
    const text = String.raw`Price $5 and formula $x+1$, then $2x+1$, $5$ and \(x+1\). Code: ` + "`$5 and $10`.";
    render(<RichContent text={text} />);

    expect(container.querySelector(".rich-paragraph")?.textContent).toContain("Price $5 and formula ");
    expect(Array.from(container.querySelectorAll(".katex annotation"), node => node.textContent)).toEqual([
      "x+1", "2x+1", "5", "x+1",
    ]);
    expect(container.querySelector("code")?.textContent).toBe("$5 and $10");
  });

  it("keeps numeric math containing text when its closing delimiter ends the expression", () => {
    render(<RichContent text={String.raw`$2中文$，$5 \text{ dogs}$。`} />);

    expect(Array.from(container.querySelectorAll(".katex annotation"), node => node.textContent)).toEqual([
      "2中文", String.raw`5 \text{ dogs}`,
    ]);
  });

  it("renders display math alongside dollar amounts", () => {
    render(<RichContent text={"成本 $100 和 $200。\n\n$$\nx^2 + y^2 = z^2\n$$"} />);

    expect(container.querySelector(".rich-paragraph")?.textContent).toBe("成本 $100 和 $200。");
    expect(container.querySelector(".katex-display annotation")?.textContent).toBe("x^2 + y^2 = z^2");
  });

  it("preserves dollar amounts in streamed answers and escaped dollar signs", () => {
    render(<StreamingMarkdown
      streamKey="currency-test"
      initialText={String.raw`从 $100 到 $200，另有 \$5。公式 $x+1$。`}
      isLive={false}
      phase="final_answer"
    />);

    expect(container.querySelector(".rich-paragraph")?.textContent).toContain("从 $100 到 $200，另有 $5。公式 ");
    expect(container.querySelector(".katex annotation")?.textContent).toBe("x+1");
    expect(container.querySelectorAll(".katex")).toHaveLength(1);
  });

  it.each(["message", "markdown"])("uses the same link rules in %s rendering", (mode) => {
    const openFile = vi.fn();
    const text = [
      "See src/main.ts and `src/main.ts:12`, or [source](src/main.ts#L12).",
      "Visit https://example.com/docs and www.example.org, or mail reader@example.com.",
      "`https://example.net/code` stays code. [unsafe](javascript:alert%281%29)",
    ].join("\n\n");
    const props = { text, cwd: "/repo/wuu", onOpenFile: openFile };
    render(mode === "message" ? <RichContent {...props} /> : <MarkdownContent {...props} />);

    const fileLinks = container.querySelectorAll<HTMLButtonElement>(".rich-file-link");
    expect(fileLinks).toHaveLength(1);
    act(() => fileLinks[0].click());
    expect(openFileMock).toHaveBeenCalledWith({ root: "/repo/wuu", path: "src/main.ts#L12" });
    expect(openFile).not.toHaveBeenCalled();
    expect(Array.from(container.querySelectorAll("a"), (link) => link.getAttribute("href"))).toEqual([
      "https://example.com/docs", "http://www.example.org/", "mailto:reader@example.com",
    ]);
    expect(container.querySelector("code a, code button")).toBeNull();
    expect(resolveWorkspaceFileReferenceMock).not.toHaveBeenCalled();
  });

  it.each(["~~~mermaid", "````mermaid", "   ```mermaid", "```mermaid title=Flow", "> ```mermaid", "- ```mermaid"]
    .flatMap((opener, index) => [false, true].map(isLive => ({ opener, index, isLive }))))(
    "renders a $opener fence as a diagram (live=$isLive)", async ({ opener, index, isLive }) => {
      const indent = opener.startsWith(">") ? "> " : opener.startsWith("-") ? "  " : "";
      const code = `graph TD\n%% example-${index}-${isLive}\nA --> B`;
      const source = `${opener}\n${code.split("\n").map(line => indent + line).join("\n")}`;
      const props = { streamKey: `mermaid-${opener}-${isLive}`, initialText: source, isLive, phase: "final_answer" as const };
      renderWithImagePreview(<StreamingMarkdown {...props} />);
      await act(async () => { await vi.waitFor(() => expect(mermaidRender).toHaveBeenCalledTimes(1)); });
      expect(mermaidRender.mock.calls[0][0]).toBe(code);
      expect(container.querySelector(".rich-mermaid svg")).not.toBeNull();
      expect(container.querySelector(".rich-code-block")).toBeNull();
      const shell = container.querySelector(".rich-mermaid");
      renderWithImagePreview(<StreamingMarkdown {...props} isLive={false} />);
      expect(container.querySelector(".rich-mermaid")).toBe(shell);
      expect(mermaidRender).toHaveBeenCalledTimes(1);
    },
  );

  it("preserves the diagram shell when loading succeeds or fails", async () => {
    let finishRender!: (value: { svg: string }) => void;
    mermaidRender.mockImplementationOnce(() => new Promise(resolve => { finishRender = resolve; }));
    renderWithImagePreview(<RichContent text={"```mermaid\ngraph TD\nA --> B\n```"} />);
    const shell = container.querySelector(".rich-mermaid");
    expect(shell?.getAttribute("aria-busy")).toBe("true");
    await act(async () => {
      await vi.waitFor(() => expect(finishRender).toBeTypeOf("function"));
      finishRender({ svg: "<svg></svg>" });
    });
    expect(container.querySelector(".rich-mermaid")).toBe(shell);
    expect(shell?.getAttribute("aria-busy")).toBe("false");
    mermaidRender.mockRejectedValueOnce(new Error("Invalid diagram"));
    renderWithImagePreview(<RichContent text={"```mermaid\ninvalid source\n```"} />);
    await act(async () => { await vi.waitFor(() => expect(mermaidRender).toHaveBeenCalledTimes(2)); });
    expect(container.querySelector(".rich-mermaid")).toBe(shell);
    expect(shell?.querySelector("code")?.textContent).toBe("invalid source");
    expect(shell?.textContent).toContain("Invalid diagram");
  });

  it("keeps the rendered diagram mounted when the applied theme changes", async () => {
    renderWithImagePreview(<RichContent text={"```mermaid\ngraph TD\nA --> B\n```"} />);
    await act(async () => {
      await vi.waitFor(() => expect(mermaidRender).toHaveBeenCalledTimes(1));
    });
    const svg = container.querySelector(".rich-mermaid svg");
    expect(svg).not.toBeNull();
    await act(async () => {
      document.documentElement.dataset.theme = "dark";
      await Promise.resolve();
    });
    expect(container.querySelector(".rich-mermaid svg")).toBe(svg);
    expect(mermaidRender).toHaveBeenCalledTimes(1);
  });

  it("opens a rendered Mermaid diagram in the shared image preview", async () => {
    renderWithImagePreview(<RichContent text={"```mermaid\ngraph LR\nA --> B\n```"} />);

    await act(async () => {
      await vi.waitFor(() => {
        expect(mermaidRender).toHaveBeenCalledTimes(1);
      });
    });
    expect(container.querySelector(".rich-mermaid-diagram svg")).not.toBeNull();

    act(() => {
      (container.querySelector(".rich-mermaid-diagram") as HTMLButtonElement).click();
    });

    const previewSvg = document.body.querySelector(".image-preview-svg svg");
    expect(document.body.querySelector(".image-preview-overlay")).not.toBeNull();
    expect(previewSvg?.getAttribute("data-diagram-id")).toMatch(/^wuu-mermaid-/);
    expect(document.body.querySelector("img.image-preview-image")).toBeNull();
  });

  it("renders an explicit markdown-link file reference as a clickable workspace file link", () => {
    const openFile = vi.fn();
    render(
      <RichContent
        text={"See [README_zh.md (line 19)](README_zh.md) before editing."}
        cwd="/repo/wuu"
        onOpenFile={openFile}
      />,
    );

    const link = container.querySelector(".rich-file-link") as HTMLButtonElement | null;
    expect(link).not.toBeNull();
    expect(link?.textContent).toContain("README_zh.md (line 19)");

    act(() => {
      link?.click();
    });

    expect(openFileMock).toHaveBeenCalledWith({ root: "/repo/wuu", path: "README_zh.md" });
    expect(openFile).not.toHaveBeenCalled();
    expect(resolveWorkspaceFileReferenceMock).not.toHaveBeenCalled();
  });

  it("keeps explicit markdown-link file references as plain text when no file opener is wired", () => {
    render(
      <RichContent
        text={"See [README_zh.md (line 19)](README_zh.md) before editing."}
        cwd="/repo/wuu"
      />, false,
    );

    expect(container.querySelector(".rich-file-link")).toBeNull();
    expect(container.textContent).toContain("README_zh.md (line 19)");
    expect(resolveWorkspaceFileReferenceMock).not.toHaveBeenCalled();
  });

  it("opens line-suffixed file links with a canonical selection fragment", () => {
    const openFile = vi.fn();
    render(
      <RichContent
        text={"See [the parser](src/parser.ts:12:4-18:9)."}
        cwd="/repo/wuu"
        onOpenFile={openFile}
      />,
    );

    const link = container.querySelector<HTMLButtonElement>(".rich-file-link");
    expect(link?.textContent).toContain("the parser");
    act(() => link?.click());
    expect(openFileMock).toHaveBeenCalledWith({ root: "/repo/wuu", path: "src/parser.ts#L12,4-L18,9" });
    expect(openFile).not.toHaveBeenCalled();
  });

  it("keeps same-document anchors inside the rendered Markdown", () => {
    const openFile = vi.fn();
    const scrollIntoView = vi.fn();
    Object.defineProperty(Element.prototype, "scrollIntoView", {
      configurable: true,
      value: scrollIntoView,
    });
    render(
      <RichContent
        text={"[Jump](#install-notes)\n\n## Install Notes"}
        cwd="/repo/wuu"
        onOpenFile={openFile}
      />,
    );

    const anchor = container.querySelector<HTMLAnchorElement>(".rich-anchor-link");
    const heading = container.querySelector<HTMLElement>("#install-notes");
    expect(anchor?.getAttribute("href")).toBe("#install-notes");
    expect(heading?.textContent).toContain("Install Notes");
    act(() => anchor?.click());
    expect(scrollIntoView).toHaveBeenCalledTimes(1);
    expect(openFile).not.toHaveBeenCalled();
  });

  it("does not auto-link bare workspace file paths in prose", () => {
    // Only `[label](path)` markdown links route to a file reference; bare
    // paths in prose stay plain text. A list like "see X, then Y, then Z"
    // should not acquire click affordances for items the model did not
    // explicitly mark as references.
    const openFile = vi.fn();
    render(
      <RichContent
        text={"The likely tool file is tool_search.go."}
        cwd="/repo/wuu"
        onOpenFile={openFile}
      />,
    );

    expect(container.querySelector(".rich-file-link")).toBeNull();
    expect(resolveWorkspaceFileReferenceMock).not.toHaveBeenCalled();
  });

  it("keeps bare image paths as mentions even when the file exists", async () => {
    const reference = "clients/mobile/assets/icon.png";
    resolveWorkspaceFileReferenceMock.mockImplementation(async (candidate: string) =>
      candidate === reference
        ? resolvedFileReference(reference, reference)
        : { root: "/repo/wuu", reference: candidate, status: "missing" },
    );

    renderWithImagePreview(
      <RichContent
        text={`已生成 ${reference}，可以先看这张。`}
        cwd="/repo/wuu"
        onOpenFile={vi.fn()}
      />,
    );
    await settleFileReferenceResolution();

    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".rich-file-link")).toBeNull();
    expect(container.textContent).toContain(reference);
    expect(resolveWorkspaceFileReferenceMock).not.toHaveBeenCalled();
  });

  it("keeps a non-interactive fallback when a Markdown image fails to load", () => {
    renderWithImagePreview(<RichContent text="![饿鹅骑自行车](missing.svg)" />);

    const image = container.querySelector<HTMLImageElement>("img.rich-image");
    expect(image).not.toBeNull();
    act(() => image?.dispatchEvent(new Event("error")));
    expect(container.querySelector("img.rich-image")).toBeNull();
    expect(container.querySelector(".rich-image-block [role=img]")).not.toBeNull();
    expect(container.querySelector(".rich-image-block [role=button]")).toBeNull();
  });

  it("decorates web links with an inline site icon", () => {
    render(<RichContent text={"Open https://github.com/blueberrycongee/wuu"} />);

    const link = container.querySelector("a.rich-web-link") as HTMLAnchorElement | null;
    expect(link).not.toBeNull();
    expect(link?.getAttribute("href")).toBe("https://github.com/blueberrycongee/wuu");
    expect(link?.hasAttribute("target")).toBe(false);

    act(() => link?.click());
    expect(openBrowserMock).toHaveBeenCalledWith(true);
  });

  it("opens a web link through the public browser service instead of the legacy callback", () => {
    const onOpenURL = vi.fn();
    render(
      <RichContent
        text={"Open https://github.com/blueberrycongee/wuu"}
        onOpenURL={onOpenURL}
      />,
    );

    const link = container.querySelector("a.rich-web-link") as HTMLAnchorElement | null;
    act(() => link?.click());
    expect(onOpenURL).not.toHaveBeenCalled();
    expect(openBrowserMock).toHaveBeenCalledWith(true);
    expect(openExternalMock).not.toHaveBeenCalled();
  });

  it("offers workspace and system browser actions without repeating the URL", () => {
    const href = "https://github.com/blueberrycongee/wuu/issues/398";
    const onOpenURL = vi.fn();
    render(<RichContent text={`See ${href}`} onOpenURL={onOpenURL} />);

    const openMenu = (): HTMLButtonElement[] => {
      const link = container.querySelector("a.rich-web-link");
      act(() => {
        link?.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
      });
      return Array.from(document.querySelectorAll<HTMLButtonElement>('.thread-row-context-menu [role="menuitem"]'));
    };

    const items = openMenu();
    expect(items).toHaveLength(2);
    expect(items.every((item) => !item.textContent?.includes(href))).toBe(true);
    act(() => items[0]?.click());
    expect(onOpenURL).not.toHaveBeenCalled();
    expect(openBrowserMock).toHaveBeenCalledWith(true);
    expect(openExternalMock).not.toHaveBeenCalled();

    const external = openMenu()[1];
    act(() => external?.click());
    expect(openExternalMock).toHaveBeenCalledWith(href);
    expect(document.querySelector(".thread-row-context-menu")).toBeNull();
  });

  it("does not turn inline code file names into file links", () => {
    render(<RichContent text={"Keep `README_zh.md` literal here."} cwd="/repo/wuu" />);

    expect(container.querySelector(".rich-file-link")).toBeNull();
    expect(container.querySelector("code")?.textContent).toBe("README_zh.md");
  });

  it("does not turn shell-error-format paths into file links", () => {
    // `program: path: error message` is the standard shell separator shape.
    // The path is sandwiched by `: ` (colon-space), which is not a
    // user-intentional file reference. Clicking the rendered link would
    // open a missing file and contradict the surrounding error.
    render(
      <RichContent
        text={"bash: scripts/desktop-dev.sh: No such file or directory"}
        cwd="/repo/wuu"
      />
    );

    expect(container.querySelector(".rich-file-link")).toBeNull();
  });

  it("keeps an oversized code block as one text node until highlight is requested", () => {
    const body = `const ${"value".repeat(2_000)} = 1;`;
    render(<RichContent text={"```javascript\n" + body + "\n```"} />);

    const code = container.querySelector(".rich-code code");
    expect(code?.querySelector("span")).toBeNull();
    expect(code?.textContent).toContain("const");
    const button = container.querySelector<HTMLButtonElement>(".rich-code-highlight");

    act(() => { button?.click(); });

    expect(container.querySelector(".rich-code code span")).not.toBeNull();
  });

  it("does not offer highlighting for a code block past the hard limit", () => {
    const body = "x".repeat(100_001);
    render(<RichContent text={"```\n" + body + "\n```"} />);

    expect(container.querySelector(".rich-code-highlight")).toBeNull();
    expect(container.querySelector(".rich-code code span")).toBeNull();
  });

  it("clicking the copy button writes the code text to the clipboard", async () => {
    render(<RichContent text={"```js\nconsole.log('hi');\n```"} />);

    const copyButton = container.querySelector(".rich-code-copy") as HTMLButtonElement | null;
    expect(copyButton).not.toBeNull();

    await act(async () => {
      copyButton?.click();
    });

    expect(writeTextMock).toHaveBeenCalledTimes(1);
    expect(writeTextMock).toHaveBeenCalledWith("console.log('hi');");
  });

});

describe("RichContent message semantics", () => {
  it("avoids collisions between repeated headings and explicit numeric suffixes", () => {
    render(<RichContent text={"# Topic\n\n# Topic\n\n# Topic-1\n\n# Topic"} />);
    expect(Array.from(container.querySelectorAll(".rich-heading"), heading => heading.id))
      .toEqual(["topic", "topic-1", "topic-1-1", "topic-2"]);
  });
});

describe("RichContent raw HTML and heading levels", () => {
  it("escapes inline HTML by default so chat output cannot inject DOM", () => {
    // Chat messages come from the model — without rehype-raw, an LLM that
    // emits <h1>title</h1> in a fenced answer should render as literal
    // text, not as an actual <h1> the user might mistake for a UI heading.
    render(<RichContent text={"<h1>injected</h1>"} />);

    expect(container.querySelector("h1")).toBeNull();
    expect(container.textContent).toContain("<h1>injected</h1>");
  });

  it("renders inline HTML as DOM elements when allowRawHtml is true", () => {
    // The workspace file preview enables this so README badges, centered
    // <div>s, and other GitHub-rendered HTML show up the same way locally
    // as they do on github.com. The h1-h6 components still override the
    // tag to a styled <p>, but a <div> the user wrote passes through
    // untouched, which is what the README badge row relies on.
    render(<RichContent text={'<div class="badge">x</div>'} allowRawHtml />);

    const badge = container.querySelector(".rich-content .badge");
    expect(badge).not.toBeNull();
    expect(badge?.textContent).toBe("x");
  });

  describe("CJK autolink boundaries", () => {
    function singleWebLink(): HTMLAnchorElement {
      const links = container.querySelectorAll("a.rich-web-link");
      expect(links).toHaveLength(1);
      return links[0] as HTMLAnchorElement;
    }

    it("ends a bare URL before a fullwidth full stop", () => {
      render(<RichContent text={"链接 https://example.com/a。"} />);

      const link = singleWebLink();
      expect(link.getAttribute("href")).toBe("https://example.com/a");
      expect(link.textContent).toBe("https://example.com/a");
      // The anchor sits inside a `display: contents` tooltip trigger, so
      // assert against the enclosing paragraph rather than the parent.
      expect(link.closest("p")?.textContent).toBe("链接 https://example.com/a。");
    });

    it("ends a bare URL before a fullwidth comma and keeps the following text out", () => {
      render(<RichContent text={"见 https://example.com/a?b=1，谢谢"} />);

      const link = singleWebLink();
      expect(link.getAttribute("href")).toBe("https://example.com/a?b=1");
      expect(link.textContent).toBe("https://example.com/a?b=1");
      expect(link.closest("p")?.textContent).toBe("见 https://example.com/a?b=1，谢谢");
    });

    it("keeps paired fullwidth parentheses inside the URL", () => {
      render(<RichContent text={"https://example.com/a（注释）"} />);

      const link = singleWebLink();
      // jsdom reflects `href` through URL serialization, which percent-encodes
      // non-ASCII; compare against the serialized form.
      expect(link.getAttribute("href")).toBe(new URL("https://example.com/a（注释）").href);
      expect(link.textContent).toBe("https://example.com/a（注释）");
    });

    it("excludes an unbalanced trailing fullwidth open paren and everything after it", () => {
      render(<RichContent text={"PR：https://github.com/blueberrycongee/wuu/pull/45（分支 → main…）"} />);

      const link = singleWebLink();
      expect(link.getAttribute("href")).toBe("https://github.com/blueberrycongee/wuu/pull/45");
      expect(link.textContent).toBe("https://github.com/blueberrycongee/wuu/pull/45");
    });

    it("excludes an unmatched fullwidth close paren", () => {
      render(<RichContent text={"（见 https://example.com/a）"} />);

      const link = singleWebLink();
      expect(link.getAttribute("href")).toBe("https://example.com/a");
      expect(link.textContent).toBe("https://example.com/a");
    });

    it("keeps the GFM ASCII trailing-punctuation behavior", () => {
      render(<RichContent text={"https://example.com/a, next"} />);

      const link = singleWebLink();
      expect(link.getAttribute("href")).toBe("https://example.com/a");
      expect(link.textContent).toBe("https://example.com/a");
    });

    it("does not trim explicit markdown links", () => {
      render(<RichContent text={"[文档](https://example.com/a。)"} />);

      const link = singleWebLink();
      expect(link.getAttribute("href")).toBe(new URL("https://example.com/a。").href);
      expect(link.textContent).toBe("文档");
    });

    it("opens the trimmed URL on click", () => {
      render(<RichContent text={"链接 https://example.com/a。"} />);

      act(() => singleWebLink().click());
      expect(openBrowserMock).toHaveBeenCalledWith(true);
    });

    it("applies the same boundary in the streaming markdown path", () => {
      render(
        <StreamingMarkdown
          streamKey="cjk-autolink-test"
          initialText={"见 https://example.com/a。"}
          isLive={false}
          phase="final_answer"
        />,
      );

      const link = singleWebLink();
      expect(link.getAttribute("href")).toBe("https://example.com/a");
      expect(link.textContent).toBe("https://example.com/a");
    });

    it("keeps a bold label bold when a bare URL is written directly against its closing markers", () => {
      render(<RichContent text={"**入口:**http://localhost:5174/**"} />);

      const paragraph = container.querySelector(".rich-paragraph");
      // The middle `**` must not be consumed as the URL's emphasis opener:
      // the label stays bold and the link stays a plain clickable anchor.
      expect(paragraph?.textContent).toBe("入口:http://localhost:5174/");
      expect(paragraph?.querySelector("strong")?.textContent).toBe("入口:");
      const link = paragraph?.querySelector("a.rich-web-link");
      expect(link?.getAttribute("href")).toBe("http://localhost:5174/");
      expect(link?.textContent).toBe("http://localhost:5174/");
      expect(paragraph?.querySelectorAll("a.rich-web-link")).toHaveLength(1);
      expect(paragraph?.textContent).not.toContain("**");
    });
  });
});

it("external-only plugin content cannot resolve workspace paths or load unverified embedded images", () => {
  render(<MarkdownContent linkPolicy="external-only" text={'[artifact](wuu-artifact://workspace/private.txt) [local](file:///tmp/private.txt) [web](https://example.test) ![remote](https://example.test/image.png) ![artifact image](wuu-artifact://workspace/image.png)'} />);
  expect(Array.from(container.querySelectorAll("a")).map(link => link.getAttribute("href"))).toEqual(["https://example.test/"]);
  expect(container.querySelector("img")).toBeNull(); expect(resolveWorkspaceFileReferenceMock).not.toHaveBeenCalled();
});

it("uses public file navigation and host media URLs without legacy callbacks", () => {
  const openFile = vi.fn(); const legacy = vi.fn();
  const service = new ApplicationPreviewContent(window.wuu, { openFile, encodedFileURL: encoded => `https://remote.example/file/${encoded}` });
  renderWithImagePreview(<PreviewContentProvider service={service}><RichContent cwd="/repo" onOpenFile={legacy}
    text="[source](src/a.ts#L2) ![image](./image.png)" /></PreviewContentProvider>);
  act(() => container.querySelector<HTMLButtonElement>(".rich-file-link")!.click());
  expect(openFile).toHaveBeenCalledExactlyOnceWith({ root: "/repo", path: "src/a.ts#L2" });
  expect(legacy).not.toHaveBeenCalled();
  expect(container.querySelector("img.rich-image")?.getAttribute("src")).toMatch(/^https:\/\/remote.example\/file\//);
});

it("reports a missing browser capability without consulting preload", async () => {
  root = createRoot(container);
  act(() => root!.render(<RichContent text="[link](https://example.com)" />));
  await act(async () => container.querySelector<HTMLAnchorElement>(".rich-web-link")!.click());
  expect(openExternalMock).not.toHaveBeenCalled();
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
});
