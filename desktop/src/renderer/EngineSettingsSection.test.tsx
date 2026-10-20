import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineListResult, EngineUpdateParams } from "../shared/protocol";
import { readDraftEngineMemory, writeDraftEngineMemory } from "./DraftEngineMemory";
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
  localStorage.clear();
  window.wuu = { ...window.wuu, listEngines: vi.fn().mockResolvedValue(inventory) };
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

// The parent owns list/detail navigation while both settings share inventory.
function Harness({ result, onUpdate, onRefresh }: {
  result?: EngineListResult;
  onUpdate: (params: EngineUpdateParams) => Promise<EngineListResult>;
  onRefresh: () => Promise<EngineListResult | undefined>;
}) {
  const [selectedEngineID, setSelectedEngineID] = useState<string>();
  return <>
    <DefaultEngineRow result={result} onUpdate={onUpdate} />
    <EngineSettingsSection result={result} onRefresh={onRefresh} onUpdate={onUpdate}
      selectedEngineID={selectedEngineID} onSelectEngine={setSelectedEngineID}
      onCloseEngine={() => setSelectedEngineID(undefined)} />
  </>;
}

function render(result: EngineListResult | undefined, onUpdate = vi.fn(), onRefresh = vi.fn()) {
  act(() => root.render(<Harness result={result} onUpdate={onUpdate} onRefresh={onRefresh} />));
  return { onUpdate, onRefresh };
}

describe("EngineSettingsSection", () => {
  it("lets the parent navigate from the list into one inline detail and back", async () => {
    const onSelectEngine = vi.fn();
    const onCloseEngine = vi.fn();
    const props = { result: inventory, embedded: true, onUpdate: vi.fn(), onRefresh: vi.fn(), onSelectEngine, onCloseEngine };
    act(() => root.render(<EngineSettingsSection {...props} />));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-codex-status"]')!.click());
    expect(onSelectEngine).toHaveBeenCalledWith("codex");
    expect(container.querySelector('[data-testid="subscription-engine-codex"]')).toBeNull();

    await act(async () => root.render(<EngineSettingsSection {...props} selectedEngineID="codex" />));
    expect(container.querySelector('[data-testid="subscription-engine-codex"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="settings-engine-connections"]')).toBeNull();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-connection-back"]')!.click());
    expect(onCloseEngine).toHaveBeenCalledOnce();

    act(() => root.render(<EngineSettingsSection {...props} />));
    expect(container.querySelector('[data-testid="settings-engine-codex-status"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="subscription-engine-codex"]')).toBeNull();
  });

  it("keeps path edits explicit and blocks repeat saves or back navigation while saving", async () => {
    let resolveSave: ((value: EngineListResult) => void) | undefined;
    const onUpdate = vi.fn(() => new Promise<EngineListResult>(resolve => { resolveSave = resolve; }));
    const onCloseEngine = vi.fn();
    await act(async () => root.render(<EngineSettingsSection result={inventory} selectedEngineID="codex"
      onSelectEngine={vi.fn()} onCloseEngine={onCloseEngine} onRefresh={vi.fn()} onUpdate={onUpdate} />));
    const input = container.querySelector<HTMLInputElement>('[data-testid="settings-engine-codex-path"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "/custom/codex");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
      input.dispatchEvent(new FocusEvent("blur", { bubbles: true }));
    });
    expect(onUpdate).not.toHaveBeenCalled();
    const save = container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-codex-path-save"]')!;
    const back = container.querySelector<HTMLButtonElement>('[data-testid="settings-connection-back"]')!;
    await act(async () => save.click());
    expect(save.disabled).toBe(true);
    expect(input.disabled).toBe(true);
    expect(back.disabled).toBe(true);
    await act(async () => { save.click(); back.click(); });
    expect(onUpdate).toHaveBeenCalledOnce();
    expect(onCloseEngine).not.toHaveBeenCalled();
    await act(async () => resolveSave?.(inventory));
    expect(back.disabled).toBe(false);
  });
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
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-devin-status"]')!.click());
    expect(document.querySelector<HTMLInputElement>('[data-testid="settings-engine-devin-path"]')!.value).toBe("/opt/devin");
    await act(async () => document.querySelector<HTMLButtonElement>('[data-testid="settings-engine-devin-enabled"]')!.click());
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

  it("hides absent default-enabled catalog engines and keeps them out of the default picker", async () => {
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
    expect(container.querySelector('[data-testid="settings-engine-claude-status"]')).toBeNull();
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
      container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-claude-status"]')!.click();
    });
    expect(document.querySelector<HTMLInputElement>('[data-testid="settings-engine-claude-path"]')?.placeholder).toBe(path);
    expect(row.querySelector(".settings-engine-detail")).toBeNull();
  });

  it("keeps a configured missing engine recoverable and retains its detail page after disabling", async () => {
    let current: EngineListResult = {
      engines: [{ id: "codex", enabled: false, binary_ok: false }],
      settings: { codex: { enabled: true, binary_path: "/missing/codex" } },
    };
    const onUpdate = vi.fn(async () => {
      current = { ...current, settings: { codex: { enabled: false, binary_path: "/missing/codex" } } };
      render(current, onUpdate);
      return current;
    });
    render(current, onUpdate);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-codex-status"]')!.click());
    expect(window.wuu.listEngines).not.toHaveBeenCalled();
    await act(async () => document.querySelector<HTMLButtonElement>('[data-testid="settings-engine-codex-enabled"]')!.click());
    expect(container.querySelector('[data-testid="settings-engine-codex-status"]')).toBeNull();
    expect(container.querySelector('[data-testid="settings-connection-back"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="subscription-engine-codex"]')).not.toBeNull();
  });

  it("retains only the selected executable's account snapshot across navigation and disabling", async () => {
    const active: EngineListResult = {
      engines: [{ id: "codex", enabled: true, binary_ok: true, binary_path: "/first/codex" }],
      settings: { codex: { binary_path: "/first/codex" } },
    };
    const observedAt = Date.now();
    window.wuu.listEngines = vi.fn().mockResolvedValue({ engines: [
      { ...active.engines[0], quota: { status: "available", kind: "subscription",
        account: { id: "first-account", label: "First account" },
        observed_at: new Date(observedAt).toISOString(), checked_at: new Date(observedAt).toISOString(),
        windows: [{ id: "weekly", label: "Weekly", used_percent: 25, window_minutes: 10080 }] } },
      { id: "claude", enabled: true, binary_ok: true, quota: { status: "available", kind: "subscription",
        account: { id: "other-account", label: "Other account" } } },
    ] });
    const props = { embedded: true, showAll: true, onUpdate: vi.fn(), onRefresh: vi.fn(), onSelectEngine: vi.fn(), onCloseEngine: vi.fn() };
    await act(async () => root.render(<EngineSettingsSection {...props} result={active} selectedEngineID="codex" />));
    expect(container.textContent).toContain("First account");
    expect(container.textContent).not.toContain("Other account");
    const disabled: EngineListResult = { ...active,
      engines: [{ ...active.engines[0], enabled: false }],
      settings: { codex: { enabled: false, binary_path: "/first/codex" } },
    };
    act(() => root.render(<EngineSettingsSection {...props} result={disabled} />));
    expect(container.querySelector<HTMLMeterElement>('[data-testid="settings-engine-codex-status"] meter')?.value).toBe(75);
    act(() => root.render(<EngineSettingsSection {...props} result={disabled} quotaFailed />));
    expect(container.querySelector('[data-testid="settings-engine-codex-status"] meter')).toBeNull();
    expect(container.querySelector('[data-testid="settings-engine-codex-status"]')?.textContent).toContain("75");
    const overview: EngineListResult = { ...disabled, engines: [{ ...disabled.engines[0], quota: {
      status: "available", kind: "subscription", observed_at: new Date(observedAt - 60_000).toISOString(),
      checked_at: new Date(observedAt - 60_000).toISOString(),
      windows: [{ id: "weekly", label: "Weekly", used_percent: 10, window_minutes: 10080 }],
    } }] };
    act(() => root.render(<EngineSettingsSection {...props} result={overview} />));
    expect(container.querySelector<HTMLMeterElement>('[data-testid="settings-engine-codex-status"] meter')?.value).toBe(75);
    overview.engines[0].quota!.observed_at = undefined;
    overview.engines[0].quota!.checked_at = "";
    act(() => root.render(<EngineSettingsSection {...props} result={{ ...overview }} />));
    expect(container.querySelector<HTMLMeterElement>('[data-testid="settings-engine-codex-status"] meter')?.value).toBe(75);
    overview.engines[0].quota!.observed_at = new Date(observedAt + 60_000).toISOString();
    act(() => root.render(<EngineSettingsSection {...props} result={{ ...overview }} />));
    expect(container.querySelector<HTMLMeterElement>('[data-testid="settings-engine-codex-status"] meter')?.value).toBe(90);
    await act(async () => root.render(<EngineSettingsSection {...props} result={disabled} selectedEngineID="codex" />));
    expect(container.textContent).toContain("First account");
    expect(window.wuu.listEngines).toHaveBeenCalledOnce();
    const changed: EngineListResult = { ...disabled,
      engines: [{ ...disabled.engines[0], binary_path: "/second/codex" }],
      settings: { codex: { enabled: false, binary_path: "/second/codex" } },
    };
    act(() => root.render(<EngineSettingsSection {...props} result={changed} />));
    expect(container.querySelector('[data-testid="settings-engine-codex-status"] meter')).toBeNull();
    await act(async () => root.render(<EngineSettingsSection {...props} result={changed} selectedEngineID="codex" />));
    expect(container.textContent).not.toContain("First account");
    expect(window.wuu.listEngines).toHaveBeenCalledOnce();
  });

  it("opens a hidden catalog engine by selection and retains a failed path draft for retry", async () => {
    const missing: EngineListResult = { engines: [{ id: "codex", enabled: false, binary_ok: false }] };
    const onUpdate = vi.fn().mockRejectedValueOnce(new Error("Cannot save path")).mockResolvedValue(missing);
    await act(async () => root.render(<EngineSettingsSection result={missing} embedded selectedEngineID="codex" onSelectEngine={vi.fn()} onCloseEngine={vi.fn()} onUpdate={onUpdate} onRefresh={vi.fn()} />));
    expect(container.querySelector('[data-testid="settings-connection-back"]')).not.toBeNull();
    const input = document.querySelector<HTMLInputElement>('[data-testid="settings-engine-codex-path"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "/custom/codex");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(onUpdate).not.toHaveBeenCalled();
    const save = document.querySelector<HTMLButtonElement>('[data-testid="settings-engine-codex-path-save"]')!;
    await act(async () => save.click());
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("Cannot save path");
    expect(input.value).toBe("/custom/codex");
    expect(save.disabled).toBe(false);
    await act(async () => save.click());
    expect(onUpdate).toHaveBeenCalledTimes(2);
    expect(onUpdate).toHaveBeenLastCalledWith({ codex: { binary_path: "/custom/codex" } });
  });

});

it("opens an official update guide without changing engine configuration or starting authentication", async () => {
  const live: EngineListResult = { engines: [{ id: "claude", enabled: true, binary_ok: true,
    cli_version: "2.1.302", update_url: "https://code.claude.com/docs/en/setup" }] };
  const openExternal = vi.fn().mockResolvedValue(undefined);
  const onUpdate = vi.fn();
  window.wuu = { ...window.wuu, openExternal, listEngines: vi.fn().mockResolvedValue(live), authenticateEngine: vi.fn() };
  await act(async () => root.render(<EngineSettingsSection result={live} selectedEngineID="claude"
    onSelectEngine={vi.fn()} onCloseEngine={vi.fn()} onRefresh={vi.fn()} onUpdate={onUpdate} />));
  expect(container.textContent).toContain("2.1.302");
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="engine-update-guide"]')!.click());
  expect(openExternal).toHaveBeenCalledExactlyOnceWith(live.engines[0].update_url);
  expect(onUpdate).not.toHaveBeenCalled();
  expect(window.wuu.authenticateEngine).not.toHaveBeenCalled();
});

it("keeps disabled models recoverable and preserves model selection while reordering", async () => {
  const live: EngineListResult = { engines: [{ id: "claude", enabled: true, binary_ok: true, models: [
    { id: "sonnet", display_name: "Sonnet", is_default: true }, { id: "opus", display_name: "Opus" },
  ] }] };
  window.wuu.listEngines = vi.fn().mockResolvedValue(live);
  writeDraftEngineMemory({ engine: "claude", model: "sonnet", effort: "high" });
  const onUpdate = vi.fn();
  const props = { result: live, selectedEngineID: "claude", onSelectEngine: vi.fn(), onCloseEngine: vi.fn(), onRefresh: vi.fn(), onUpdate };
  await act(async () => root.render(<EngineSettingsSection {...props} />));
  await act(async () => container.querySelector<HTMLButtonElement>('[data-model-id="opus"] [role="switch"]')!.click());
  expect(container.querySelector('[data-model-id="opus"] [role="switch"]')?.getAttribute("aria-checked")).toBe("false");
  await act(async () => container.querySelector<HTMLButtonElement>('[data-model-id="opus"] .connection-model-order button')!.click());
  expect([...container.querySelectorAll('[data-model-id]')].map(row => row.getAttribute("data-model-id"))).toEqual(["opus", "sonnet"]);
  expect(readDraftEngineMemory()).toMatchObject({ engine: "claude", model: "sonnet", effort: "high" });
  expect(onUpdate).not.toHaveBeenCalled();
  await act(async () => root.render(<EngineSettingsSection {...props} selectedEngineID={undefined} />));
  await act(async () => root.render(<EngineSettingsSection {...props} />));
  expect(container.querySelector('[data-model-id="opus"] [role="switch"]')?.getAttribute("aria-checked")).toBe("false");
});
