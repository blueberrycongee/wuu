import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { UsageOverviewResponse, WuuDesktopApi } from "../shared/protocol";
import { EmptyHomeOverview } from "./EmptyHomeOverview";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  container?.remove();
  container = undefined;
  delete (window as unknown as { wuu?: unknown }).wuu;
});

function installWuuStub(overrides: Partial<WuuDesktopApi>): void {
  (window as unknown as { wuu: WuuDesktopApi }).wuu = {
    ...overrides,
  } as WuuDesktopApi;
}

async function renderOverview(): Promise<HTMLDivElement> {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  await act(async () => {
    root?.render(<EmptyHomeOverview />);
    await Promise.resolve();
    await Promise.resolve();
  });
  return container;
}

function storeWithoutUsage(): UsageOverviewResponse {
  return {
    total_sessions: 0,
    metrics: {
      prompt_tokens: 0,
      context_tokens: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      cache_hit_rate: 0,
      turns: 0,
      agents: 0,
      date_range: ["", ""],
      active_days: 0,
    },
    days: [],
  };
}

describe("EmptyHomeOverview", () => {
  it("shows nothing instead of zero usage when the host cannot answer", async () => {
    installWuuStub({
      getUsageOverview: vi.fn().mockRejectedValue(new Error("unknown method usage/overview")),
    });

    const view = await renderOverview();

    expect(view.querySelector(".empty-home-overview")).toBeNull();
  });

  it("shows zero totals and an empty heatmap for a store without usage", async () => {
    const getUsageOverview = vi.fn().mockResolvedValue(storeWithoutUsage());
    installWuuStub({ getUsageOverview });

    const view = await renderOverview();

    // Days must line up with the calendar the renderer draws.
    expect(getUsageOverview).toHaveBeenCalledWith({
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    });
    const values = [...view.querySelectorAll(".empty-home-stats dd")].map((value) => value.textContent);
    expect(values).toEqual(["0", "0", "0"]);
    const cells = [...view.querySelectorAll(".empty-home-heatmap-week i")];
    expect(cells.length).toBeGreaterThan(0);
    expect(cells.every((cell) => cell.getAttribute("data-level") === "0")).toBe(true);
  });
});
