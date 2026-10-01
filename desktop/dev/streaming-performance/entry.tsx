import { createRoot, type Root } from "react-dom/client";
import { flushSync } from "react-dom";
import { StreamingMarkdown } from "../../src/renderer/StreamingMarkdown";
import { streamTextKey, streamTextStore } from "../../src/renderer/StreamText";
import "../../src/renderer/styles.css";

// Measure committed tail snapshots independently of provider/network cadence.
// The real stream store and production Markdown tree are used without inference.
let root: Root;
let container: HTMLDivElement;
let firstBlock: Element | null;
let updates = 0;
const key = streamTextKey("performance", "answer", "text");

function render(): void {
  flushSync(() => root.render(
    <StreamingMarkdown streamKey={key} isLive={true} phase="final_answer" />,
  ));
}

const fixture = {
  mount(blocks: number): number {
    container = document.createElement("div");
    container.className = "conversation-width";
    document.body.appendChild(container);
    root = createRoot(container);
    updates = 0;
    const text = Array.from({ length: blocks }, (_, index) =>
      `Paragraph ${index}: synthetic **completed** content with an inline \`code\` span and [reference](https://example.com). The existing answer remains readable while another paragraph arrives.\n\n`,
    ).join("") + "Tail";
    streamTextStore.seed(key, text);
    render();
    firstBlock = container.querySelector(".streaming-markdown-block");
    return text.length;
  },
  commit(count: number): number[] {
    const samples: number[] = [];
    for (let index = 0; index < count; index++) {
      streamTextStore.append(key, " streamed");
      const started = performance.now();
      render();
      samples.push(performance.now() - started);
      updates++;
    }
    return samples;
  },
  finish(): { sameFirstBlock: boolean; completeTail: boolean } {
    const result = {
      sameFirstBlock: container.querySelector(".streaming-markdown-block") === firstBlock,
      completeTail: container.textContent?.endsWith("Tail" + " streamed".repeat(updates)) ?? false,
    };
    flushSync(() => root.unmount());
    container.remove();
    streamTextStore.clearTurn("performance");
    return result;
  },
};

(window as typeof window & { streamingPerformance: typeof fixture }).streamingPerformance = fixture;
