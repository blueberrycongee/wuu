import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineListResult } from "../shared/protocol";
import { DefaultEngineRow, EngineSettingsSection } from "./EngineSettingsSection";

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

function defaultPicker(): HTMLButtonElement {
  return container.querySelector<HTMLButtonElement>('[data-testid="settings-default-engine"]')!;
}

async function openDefaultPicker(): Promise<HTMLButtonElement[]> {
  await act(async () => defaultPicker().click());
  return [...document.querySelectorAll<HTMLButtonElement>(".select-menu-item")];
}

// The default picker lives on the General page and the agent list on the
// Agents page; both read and write the same inventory.
function render(result: EngineListResult | undefined, onUpdate = vi.fn(), onRefresh = vi.fn()) {
  act(() => {
    root.render(
      <>
        <DefaultEngineRow result={result} onUpdate={onUpdate} />
        <EngineSettingsSection
          result={result}
          onRefresh={onRefresh}
          onUpdate={onUpdate}
        />
      </>,
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
    const devin = (await openDefaultPicker()).find((item) => item.dataset.value === "devin")!;
    await act(async () => devin.click());
    expect(onUpdate).toHaveBeenLastCalledWith({ default_engine: "devin" });
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-devin-advanced-toggle"]')!.click());
    expect(container.querySelector<HTMLInputElement>('[data-testid="settings-engine-devin-path"]')!.value).toBe("/opt/devin");
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-devin-enabled"]')!.click());
    expect(onUpdate).toHaveBeenLastCalledWith({ devin: { enabled: false } });
  });

  it("renders a supplied session snapshot without starting detection on mount", () => {
    const onRefresh = vi.fn();
    render(inventory, vi.fn(), onRefresh);

    expect(defaultPicker().textContent).toContain("Wuu");
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
    expect(defaultPicker()).not.toBeNull();

    await act(async () => {
      resolveRefresh?.(inventory);
    });
    expect(refresh.disabled).toBe(false);
  });

  it("uses a stable skeleton only when no session snapshot exists", () => {
    render(undefined);

    expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
    expect(defaultPicker().disabled).toBe(true);
    expect(container.querySelector('[data-testid="settings-engine-refresh"]')).not.toBeNull();
  });

  it("keeps an undetected agent listed but unselectable, with the reason on the row", async () => {
    const missingClaude: EngineListResult = {
      engines: [
        { id: "wuu", enabled: true, binary_ok: true },
        { id: "codex", enabled: true, binary_ok: true },
        { id: "claude", enabled: true, binary_ok: false },
      ],
      settings: { default_engine: "wuu" },
    };
    render(missingClaude);

    const options = await openDefaultPicker();
    expect(options.map((item) => item.dataset.value)).toEqual(["wuu", "codex"]);
    expect(
      container.querySelector('[data-testid="settings-engine-claude-status"]')?.getAttribute("aria-label"),
    ).toContain("未安装");
  });

  it("lists a switched-off agent in the default picker without letting it be picked", async () => {
    const onUpdate = vi.fn();
    render({
      engines: [
        { id: "wuu", enabled: true, binary_ok: true },
        { id: "codex", enabled: false, binary_ok: true },
      ],
      settings: { default_engine: "wuu", codex: { enabled: false } },
    }, onUpdate);

    const codex = (await openDefaultPicker()).find((item) => item.dataset.value === "codex")!;
    expect(codex.disabled).toBe(true);
    expect(codex.textContent).toContain("已停用");
    await act(async () => codex.click());
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it("shows a detected binary path only in the override field", async () => {
    const path = "/opt/claude/bin/claude";
    render({
      engines: [{ id: "claude", enabled: true, binary_ok: true, binary_path: path }],
      settings: { default_engine: "wuu" },
    });

    const row = container.querySelector('[data-testid="settings-engine-claude-status"]')!;
    expect(row.getAttribute("aria-label")).toContain("已就绪");
    expect(row.textContent).not.toContain(path);
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-claude-advanced-toggle"]')!.click();
    });
    expect(container.querySelector<HTMLInputElement>('[data-testid="settings-engine-claude-path"]')?.placeholder).toBe(path);
    expect(row.querySelector(".settings-engine-detail")).toBeNull();
  });
});
