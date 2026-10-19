import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ComposerContextMeter } from "./ComposerContextMeter";
import type { TurnContextUsage } from "./AppState";

let container: HTMLDivElement;
let root: Root | null = null;

function usageWith(
  overrides: Partial<TurnContextUsage> = {},
): TurnContextUsage {
  return {
    turnID: "turn-1",
    used: 45_000,
    window: 200_000,
    inputTokens: 30_000,
    cacheCreationTokens: 3_000,
    cacheReadTokens: 12_000,
    ...overrides,
  };
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  document
    .querySelectorAll(".floating-menu-layer")
    .forEach((node) => node.remove());
  container.remove();
});

function renderMeter(usage: TurnContextUsage | undefined): HTMLDivElement {
  act(() => {
    root = createRoot(container);
    root.render(<ComposerContextMeter usage={usage} />);
  });
  return container;
}

describe("ComposerContextMeter", () => {
  it("hides entirely when no usage snapshot is provided", () => {
    renderMeter(undefined);
    expect(container.querySelector(".composer-context-meter")).toBeNull();
  });

  it("hides when the context ceiling is zero (unknown / unsupported)", () => {
    renderMeter(usageWith({ window: 0 }));
    expect(container.querySelector(".composer-context-meter")).toBeNull();
  });

  it("hides when the context ceiling is negative", () => {
    renderMeter(usageWith({ window: -1 }));
    expect(container.querySelector(".composer-context-meter")).toBeNull();
  });

  it("renders the ring track and progress stroke", () => {
    renderMeter(usageWith());
    expect(
      container.querySelector(".context-usage-ring-track"),
    ).not.toBeNull();
    expect(
      container.querySelector(".context-usage-ring-progress"),
    ).not.toBeNull();
  });

  it("renders the latest used / limit value inline", () => {
    renderMeter(usageWith({ used: 45_000, window: 200_000 }));
    expect(
      container.querySelector(".composer-context-meter-label")?.textContent,
    ).toBe("45k / 200k");
  });

  it("scales to retained context, never to raw provider input", () => {
    // MiniMax reports input_tokens inclusive of cache_read (~132.6k here);
    // the ring must read the retained-context estimate (usage.used), not
    // the raw input, or it would grossly over-state occupancy. usage.used
    // already carries the server-side retained estimate (res.ContextTokens),
    // so the raw input/cache figures below must not leak into the readout.
    renderMeter(
      usageWith({
        used: 45_000,
        window: 200_000,
        inputTokens: 132_600,
        cacheReadTokens: 113_000,
      }),
    );
    expect(
      container.querySelector(".composer-context-meter-label")?.textContent,
    ).toBe("45k / 200k");
    const aria =
      container
        .querySelector(".composer-context-meter")
        ?.getAttribute("aria-label") ?? "";
    expect(aria).toContain("已保留 45k");
    expect(aria).toContain("23%"); // 45k / 200k, not 132.6k / 200k (66%)
    expect(aria).not.toContain("66%");
    expect(aria).not.toContain("133k");
  });

  it("does not render any center text — the ring is the readout", () => {
    // Per the user's spec: "圆环 + hover 显明细". A small 20×20 ring
    // does not have room for legible center text, and the percentage
    // belongs to the hover tooltip and aria-label, not the visible ring.
    renderMeter(usageWith({ used: 50_000, window: 200_000 }));
    expect(container.querySelector("text")).toBeNull();
  });

  it("renders an empty ring (no fill) when used is zero", () => {
    // The fallback case: context ceiling is known (window > 0) but no
    // turn has run yet, so used is 0. The progress stroke should be
    // at its full circumference offset — nothing visible.
    renderMeter(
      usageWith({
        used: 0,
        inputTokens: 0,
        cacheCreationTokens: 0,
        cacheReadTokens: 0,
      }),
    );
    const progress = container.querySelector(
      ".context-usage-ring-progress",
    );
    expect(progress).not.toBeNull();
    const offset = Number(progress?.getAttribute("stroke-dashoffset") ?? "0");
    // Full circumference means the ring is fully hidden.
    expect(offset).toBeGreaterThan(0);
  });

  it("encodes used / limit / percent in the aria label", () => {
    renderMeter(usageWith({ used: 45_000, window: 200_000 }));
    const aria =
      container
        .querySelector(".composer-context-meter")
        ?.getAttribute("aria-label") ?? "";
    expect(aria).toContain("已保留 45k");
    expect(aria).toContain("200k");
    expect(aria).toContain("23%");
  });

  it("mounts a compact tooltip in a portal on focus", () => {
    renderMeter(usageWith());
    const meter = container.querySelector<HTMLElement>(
      ".composer-context-meter",
    );
    expect(
      container.querySelector(".composer-context-meter-tooltip"),
    ).toBeNull();
    act(() => {
      meter?.focus();
    });
    const tooltip = document.body.querySelector(
      ".composer-context-meter-tooltip",
    );
    expect(tooltip).not.toBeNull();
    expect(meter?.getAttribute("aria-describedby")).toBe(tooltip?.id);
    // Headline: simple percentage, with raw used/limit as the only detail.
    expect(
      tooltip?.querySelector(
        ".composer-context-meter-tooltip-headline",
      )?.textContent ?? "",
    ).toContain("23%");
    const text = tooltip?.textContent ?? "";
    expect(text).toContain("保留历史");
    expect(text).toContain("45k / 200k");
    expect(text).not.toContain("输入");
    expect(text).not.toContain("缓存读取");
    expect(text).not.toContain("新建缓存");
  });

  it("adds request-shape diagnostics when the backend emits them", () => {
    renderMeter(
      usageWith({
        requestContext: {
          stepIndex: 0,
          messageCount: 8,
          stablePrefix: 5,
          turnPrefix: 6,
          transientMessages: 1,
          hiddenMessages: 1,
          toolCount: 14,
          stablePrefixBytes: 3200,
          turnPrefixBytes: 4100,
          messageBytes: 9800,
          dynamicBytes: 1200,
          toolSchemaBytes: 22000,
          promptCacheKey: "thread-1",
          stablePrefixHash: "stable",
          turnPrefixHash: "turn",
          toolSurfaceHash: "tools",
        },
      }),
    );
    const meter = container.querySelector<HTMLElement>(
      ".composer-context-meter",
    );
    act(() => {
      meter?.focus();
    });
    const text =
      document.body.querySelector(".composer-context-meter-tooltip")
        ?.textContent ?? "";
    expect(text).toContain("稳定前缀");
    expect(text).toContain("5 / 8 条");
    expect(text).toContain("本轮前缀");
    expect(text).toContain("6 / 8 条");
    expect(text).toContain("临时上下文");
    expect(text).toContain("1 条 · 1.2kB");
    expect(text).toContain("工具面");
    expect(text).toContain("14 个 · 22kB");
  });

  it("is focusable so keyboard users can reach the tooltip", () => {
    renderMeter(usageWith());
    const root = container.querySelector(".composer-context-meter");
    expect(root?.getAttribute("tabindex")).toBe("0");
    expect(root?.getAttribute("role")).toBe("status");
  });

  it("shrinks the progress stroke-dashoffset as fill grows", () => {
    // Same window, two fill levels → larger fill → smaller strokeDashoffset
    // (the progress arc reveals more).
    const a = document.createElement("div");
    document.body.appendChild(a);
    let aRoot: Root | null = createRoot(a);
    act(() => {
      aRoot!.render(
        <ComposerContextMeter usage={usageWith({ used: 40_000 })} />,
      );
    });
    const offsetSmall = Number(
      a
        .querySelector(".context-usage-ring-progress")
        ?.getAttribute("stroke-dashoffset") ?? "0",
    );
    act(() => {
      aRoot!.unmount();
      aRoot = createRoot(a);
      aRoot!.render(
        <ComposerContextMeter usage={usageWith({ used: 160_000 })} />,
      );
    });
    const offsetLarge = Number(
      a
        .querySelector(".context-usage-ring-progress")
        ?.getAttribute("stroke-dashoffset") ?? "0",
    );
    act(() => {
      aRoot?.unmount();
      aRoot = null;
      a.remove();
    });
    expect(offsetLarge).toBeLessThan(offsetSmall);
  });

  it("keeps the visible round-cap gap proportional near a full ring", () => {
    renderMeter(usageWith({ used: 96_000, window: 100_000 }));
    const progress = container.querySelector(
      ".context-usage-ring-progress",
    );
    const circumference = Number(
      progress?.getAttribute("stroke-dasharray") ?? "0",
    );
    const dashOffset = Number(
      progress?.getAttribute("stroke-dashoffset") ?? "0",
    );
    const strokeWidth = Number(progress?.getAttribute("stroke-width") ?? "0");

    // The two round caps consume one stroke width of the geometric gap.
    // Compensating the dash leaves a visible 4% gap for a 96% reading.
    expect(dashOffset - strokeWidth).toBeCloseTo(circumference * 0.04, 8);
  });

  it("drives the SVG data-fill and inline color from the gauge color tiers", () => {
    // ratio = 0 → idle gray (matches the token-speed gauge when stopped).
    renderMeter(
      usageWith({
        used: 0,
        inputTokens: 0,
        cacheCreationTokens: 0,
        cacheReadTokens: 0,
      }),
    );
    let svg = container.querySelector(
      ".composer-context-meter-svg",
    ) as HTMLElement | null;
    expect(svg?.getAttribute("data-fill")).toBe("idle");
    expect(svg?.style.color).toBe("var(--token-gauge-idle)");

    // ratio = 0.5 → mid amber (matches the token-speed gauge while running).
    act(() => {
      root?.unmount();
      root = null;
    });
    renderMeter(usageWith({ used: 100_000, window: 200_000 }));
    svg = container.querySelector(
      ".composer-context-meter-svg",
    ) as HTMLElement | null;
    expect(svg?.getAttribute("data-fill")).toBe("mid");
    expect(svg?.style.color).toBe("var(--token-gauge-mid)");

    // ratio ≥ 0.7 → high warm (matches the token-speed gauge past its 70%
    // threshold, so both meters step into "high" at the same fraction).
    act(() => {
      root?.unmount();
      root = null;
    });
    renderMeter(usageWith({ used: 170_000, window: 200_000 }));
    svg = container.querySelector(
      ".composer-context-meter-svg",
    ) as HTMLElement | null;
    expect(svg?.getAttribute("data-fill")).toBe("high");
    expect(svg?.style.color).toBe("var(--token-gauge-high)");
  });
});
