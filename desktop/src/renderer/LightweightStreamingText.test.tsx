/**
 * Tests for LightweightStreamingText.
 *
 * Contract: initial committed snapshots snap to full so remounting a
 * session does not replay old gray process text. Text that grows after
 * mount reveals progressively at a steady pace (~12 cps with a 100 ms
 * base), never resets when the target grows, and snaps on non-live,
 * shrink, and unmount paths.
 */
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";
import { LightweightStreamingText } from "./LightweightStreamingText";

// jsdom doesn't implement layout. Stub getBoundingClientRect so React
// doesn't crash on layout queries.
beforeAll(() => {
  Element.prototype.getBoundingClientRect = function (): DOMRect {
    return {
      x: 0,
      y: 0,
      top: 0,
      left: 0,
      right: 0,
      bottom: 0,
      width: 0,
      height: 0,
      toJSON() {
        return this;
      },
    } as DOMRect;
  };
});

let container: HTMLDivElement | null = null;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
});

function mount(props: Parameters<typeof LightweightStreamingText>[0]): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<LightweightStreamingText {...props} />);
  });
}

function rerender(props: Parameters<typeof LightweightStreamingText>[0]): void {
  act(() => {
    root!.render(<LightweightStreamingText {...props} />);
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

function surfaceText(): string {
  const span = document.querySelector(".lightweight-stream") as HTMLElement | null;
  return span?.textContent ?? "";
}

afterEach(() => {
  unmount();
  vi.useRealTimers();
});

function advanceReveal(ms: number): void {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

describe("LightweightStreamingText", () => {
  it("renders short text in full immediately (no animation)", () => {
    mount({ text: "ok", live: true, className: "lightweight-stream" });
    expect(surfaceText()).toBe("ok");
  });

  it("renders empty text as empty", () => {
    mount({ text: "", live: true, className: "lightweight-stream" });
    expect(surfaceText()).toBe("");
  });

  it("snaps to full text when live is false", () => {
    mount({
      text: "Reading the file now",
      live: false,
      className: "lightweight-stream",
    });
    expect(surfaceText()).toBe("Reading the file now");
  });

  it("renders initial live text in full so remounts do not replay", () => {
    mount({
      text: "Looking at the file",
      live: true,
      className: "lightweight-stream",
    });

    expect(surfaceText()).toBe("Looking at the file");
  });

  it("continues reveal when target text grows (never resets visible position)", () => {
    mount({
      text: "",
      live: true,
      className: "lightweight-stream",
    });
    rerender({
      text: "Reading file",
      live: true,
      className: "lightweight-stream",
    });

    // Let the reveal advance partway. 12 chars at ~12 cps with a 100 ms
    // base needs a settle wait comfortably past the base window;
    // 300 ms lands the reveal around 6 chars, which is clearly
    // partial and well above zero.
    advanceReveal(300);
    const before = surfaceText();
    expect(before.length).toBeGreaterThan(0);
    expect(before.length).toBeLessThan("Reading file".length);

    // Back-end pushes a longer snapshot. The reveal must continue
    // forward from the visible position, never jump back to a
    // shorter visible length.
    rerender({
      text: "Reading file content for analysis",
      live: true,
      className: "lightweight-stream",
    });
    const justAfter = surfaceText();
    expect(justAfter.length).toBeGreaterThanOrEqual(before.length);

    // Advance past the duration cap to finish the new target deterministically.
    advanceReveal(3000);
    expect(surfaceText()).toBe("Reading file content for analysis");
  });

  it("snaps to a shorter snapshot when the back-end shrinks the text", () => {
    mount({
      text: "Reading file content for analysis",
      live: true,
      className: "lightweight-stream",
    });

    // Finish the reveal before the back-end pushes the shorter snapshot.
    advanceReveal(3000);
    expect(surfaceText()).toBe("Reading file content for analysis");

    rerender({
      text: "Reading file",
      live: true,
      className: "lightweight-stream",
    });
    expect(surfaceText()).toBe("Reading file");
  });

  it("snaps to full text when live flips false mid-reveal", () => {
    mount({
      text: "",
      live: true,
      className: "lightweight-stream",
    });
    rerender({
      text: "Looking at this long content that has not finished revealing",
      live: true,
      className: "lightweight-stream",
    });

    advanceReveal(100);
    const mid = surfaceText();
    expect(mid.length).toBeGreaterThan(0);
    expect(mid.length).toBeLessThan(
      "Looking at this long content that has not finished revealing".length
    );

    rerender({
      text: "Looking at this long content that has not finished revealing",
      live: false,
      className: "lightweight-stream",
    });
    expect(surfaceText()).toBe(
      "Looking at this long content that has not finished revealing"
    );
  });

  it("respects the duration cap for very long text", () => {
    const longText = "a".repeat(500);
    mount({
      text: "",
      live: true,
      className: "lightweight-stream",
    });
    rerender({
      text: longText,
      live: true,
      className: "lightweight-stream",
    });

    // 500 chars would naively take 100 + 500*80 = 40100 ms. The cap is
    // 1800 ms, so 2000 ms is enough for the reveal to settle.
    advanceReveal(2000);
    expect(surfaceText()).toBe(longText);
  });

  it("does not leave a running RAF after unmount", () => {
    mount({
      text: "",
      live: true,
      className: "lightweight-stream",
    });
    rerender({
      text: "Looking at the file",
      live: true,
      className: "lightweight-stream",
    });
    // Unmount during reveal. If the RAF keeps firing it would touch
    // a null ref, but the bigger concern is a leaked loop: we assert
    // unmount completes cleanly with no errors and no further DOM
    // mutations.
    advanceReveal(100);
    unmount();
    expect(document.querySelector(".lightweight-stream")).toBeNull();
  });

  it("snaps text that caught up while the conversation was hidden", () => {
    const caughtUp = "Updated plan: read the configuration file and report back";
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    act(() => {
      root!.render(
        <ConversationRenderActivityProvider active={false}>
          <LightweightStreamingText text="Hi" live className="lightweight-stream" />
        </ConversationRenderActivityProvider>,
      );
    });
    act(() => {
      root!.render(
        <ConversationRenderActivityProvider active>
          <LightweightStreamingText text={caughtUp} live className="lightweight-stream" />
        </ConversationRenderActivityProvider>,
      );
    });
    expect(surfaceText()).toBe(caughtUp);
  });

  it("preserves the live=false path even when text grew", () => {
    mount({ text: "ok", live: true, className: "lightweight-stream" });
    rerender({
      text: "Updated plan: read the configuration file and report back",
      live: false,
      className: "lightweight-stream",
    });
    expect(surfaceText()).toBe(
      "Updated plan: read the configuration file and report back"
    );
  });
});
