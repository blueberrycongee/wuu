import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ContextCompositionCard, type ContextCompositionEntry } from "./ContextCompositionCard";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
});

function renderCard(entry: ContextCompositionEntry): void {
  act(() => {
    root = createRoot(container);
    root.render(<ContextCompositionCard entry={entry} />);
  });
}

describe("ContextCompositionCard", () => {
  it("uses retained history as the primary context occupancy", () => {
    renderCard({
      id: "entry-1",
      threadID: "thread-1",
      loading: false,
      result: {
        thread_id: "thread-1",
        available: true,
        provider: "custom-provider",
        model: "bring-your-own-model",
        prompt_tokens: 508_000,
        context_window_tokens: 512_000,
        input_limit_tokens: 512_000,
        compact_threshold_tokens: 384_000,
        retained_tokens: 103_000,
        categories: [
          {
            id: "turn_prefix",
            label: "本轮前缀",
            contributes: true,
            tokens: 491_000,
            tone: "turn",
          },
        ],
      },
    });

    const text = container.textContent ?? "";
    expect(text).toMatch(/103k\s*\/\s*512k/);
    expect(text).toContain("本轮前缀");
  });

  it("scales the composition bar to retained context occupancy", () => {
    renderCard({
      id: "entry-2",
      threadID: "thread-1",
      loading: false,
      result: {
        thread_id: "thread-1",
        available: true,
        prompt_tokens: 400_000,
        context_window_tokens: 500_000,
        retained_tokens: 100_000,
        categories: [
          {
            id: "system",
            label: "系统提示",
            contributes: true,
            tokens: 100_000,
            tone: "system",
          },
          {
            id: "stable_history",
            label: "稳定历史",
            contributes: true,
            tokens: 300_000,
            tone: "stable",
          },
        ],
      },
    });

    const segments = Array.from(container.querySelectorAll<HTMLElement>(".context-composition-segment"));

    expect(segments).toHaveLength(2);
    expect(Number.parseFloat(segments[0]?.style.width ?? "")).toBeCloseTo(5);
    expect(Number.parseFloat(segments[1]?.style.width ?? "")).toBeCloseTo(15);
  });

  it("keeps preserved cache_read as a hit and a de-inflated prompt after input normalization", () => {
    // After A1 normalizes an inclusive-input endpoint (MiniMax), prompt_tokens
    // is the true fresh+cached prompt (~133k) rather than a ~4x-inflated
    // figure, and cache_read is preserved (never zeroed) so the hit readout
    // still renders. The occupancy headline reads retained history, not the
    // prompt size.
    renderCard({
      id: "entry-minimax",
      threadID: "thread-1",
      loading: false,
      result: {
        thread_id: "thread-1",
        available: true,
        provider: "minimax",
        model: "minimax-m3",
        prompt_tokens: 133_000,
        context_window_tokens: 1_000_000,
        retained_tokens: 88_000,
        cache_read_tokens: 113_000,
        categories: [
          {
            id: "stable_history",
            label: "稳定历史",
            contributes: true,
            tokens: 133_000,
            tone: "stable",
          },
        ],
      },
    });

    const text = container.textContent ?? "";
    expect(text).toContain("缓存命中 113k"); // cache_read preserved, not zeroed
    expect(text).toMatch(/88\.0k\s*\/\s*1\.0M/); // occupancy = retained history
  });
});
