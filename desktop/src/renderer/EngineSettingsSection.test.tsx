import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineListResult } from "../shared/protocol";
import { EngineSettingsSection } from "./EngineSettingsSection";

const inventory: EngineListResult = {
  engines: [
    { id: "wuu", enabled: true, binary_ok: true },
    { id: "codex", enabled: true, binary_ok: true },
    { id: "claude", enabled: true, binary_ok: true },
  ],
  settings: { default_engine: "wuu" },
};

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(result: EngineListResult | undefined, onUpdate = vi.fn(), onRefresh = vi.fn()) {
  act(() => {
    root.render(
      <EngineSettingsSection
        result={result}
        onRefresh={onRefresh}
        onUpdate={onUpdate}
      />,
    );
  });
  return { onUpdate, onRefresh };
}

describe("EngineSettingsSection", () => {
  it("saves defaults and disables engines discovered outside the native pair", async () => {
    const live: EngineListResult = {
      engines: [{ id: "devin", display_name: "Devin", enabled: true, binary_ok: true }],
      settings: { devin: { binary_path: "/opt/devin" } },
    };
    const onUpdate = vi.fn().mockResolvedValue(live);
    render(live, onUpdate);
    await act(async () => container.querySelector<HTMLInputElement>('[data-testid="settings-engine-devin-radio"]')!.click());
    expect(onUpdate).toHaveBeenLastCalledWith({ default_engine: "devin" });
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-devin-advanced-toggle"]')!.click());
    expect(container.querySelector<HTMLInputElement>('[data-testid="settings-engine-devin-path"]')!.value).toBe("/opt/devin");
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-devin-enabled"]')!.click());
    expect(onUpdate).toHaveBeenLastCalledWith({ devin: { enabled: false } });
  });

  it("renders a supplied session snapshot without starting detection on mount", () => {
    const onRefresh = vi.fn();
    render(inventory, vi.fn(), onRefresh);

    expect(container.querySelector('[role="radiogroup"]')).not.toBeNull();
    expect(
      container.querySelector<HTMLInputElement>('[data-testid="settings-engine-wuu-radio"]')?.checked,
    ).toBe(true);
    expect(container.querySelector('[data-testid="settings-engine-codex-status"]')?.getAttribute("aria-label")).toContain("已就绪");
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it("keeps the cached snapshot visible while the user explicitly refreshes", async () => {
    let resolveRefresh: ((value: EngineListResult) => void) | undefined;
    const onRefresh = vi.fn(() => new Promise<EngineListResult>((resolve) => {
      resolveRefresh = resolve;
    }));
    render(inventory, vi.fn(), onRefresh);

    const refresh = container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-refresh"]')!;
    await act(async () => {
      refresh.click();
    });

    expect(onRefresh).toHaveBeenCalledOnce();
    expect(refresh.disabled).toBe(true);
    expect(container.querySelector('[data-testid="settings-engine-wuu-radio"]')).not.toBeNull();

    await act(async () => {
      resolveRefresh?.(inventory);
    });
    expect(refresh.disabled).toBe(false);
  });

  it("uses a stable skeleton only when no session snapshot exists", () => {
    render(undefined);

    expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
    expect(container.querySelector('[role="radiogroup"]')).toBeNull();
    expect(container.querySelector('[data-testid="settings-engine-refresh"]')).not.toBeNull();
  });

  it("keeps an undetected agent listed but unselectable, with the reason on the row", () => {
    const missingClaude: EngineListResult = {
      engines: [
        { id: "wuu", enabled: true, binary_ok: true },
        { id: "codex", enabled: true, binary_ok: true },
        { id: "claude", enabled: true, binary_ok: false },
      ],
      settings: { default_engine: "wuu" },
    };
    render(missingClaude);

    const claudeRadio = container.querySelector<HTMLInputElement>('[data-testid="settings-engine-claude-radio"]')!;
    expect(claudeRadio.disabled).toBe(true);
    expect(
      container.querySelector('[data-testid="settings-engine-claude-status"]')?.getAttribute("aria-label"),
    ).toContain("未安装");
  });
});
