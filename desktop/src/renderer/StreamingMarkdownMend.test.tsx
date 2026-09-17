import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MarkdownContent } from "./RichContent";
import { mendStreamingMarkdown } from "./StreamingMarkdownMend";

function render(text: string, live = true): HTMLElement {
  const container = document.createElement("div");
  container.innerHTML = renderToStaticMarkup(<MarkdownContent text={live ? mendStreamingMarkdown(text) : text} renderMermaid={false} />);
  return container;
}

describe("streaming display repair", () => {
  it("keeps incomplete emphasis styled until completion restores the actual source", () => {
    expect(render("**你好 ").querySelector("strong")?.textContent).toBe("你好");
    expect(render("**你好**").querySelector("strong")?.textContent).toBe("你好");
    expect(render("**你好", false).textContent).toBe("**你好");
    expect(render("**a *b").querySelector("strong em")?.textContent).toBe("b");
  });
  it("hides partial URLs without creating navigable links or image requests", () => {
    for (const input of ["[docs](https://exa", "[docs", "![docs](https://exa"]) {
      const node = render(input);
      expect(node.textContent).toBe("docs");
      expect(node.querySelector("a, img")).toBeNull();
    }
  });
  it("does not interpret code, escapes or intraword underscores as prose delimiters", () => {
    expect(render("`**x").querySelector("code")?.textContent).toBe("**x");
    expect(render("foo_bar").textContent).toBe("foo_bar");
    expect(render("\\*literal").textContent).toBe("*literal");
    expect(mendStreamingMarkdown("```ts\n**x")).toBe("```ts\n**x");
  });
  it("defers an ambiguous setext heading until the next chunk decides", () => {
    expect(render("hello\n-").querySelector(".rich-heading")).toBeNull();
    expect(render("hello\n--", false).querySelector(".rich-heading")?.textContent).toBe("hello");
  });
});
