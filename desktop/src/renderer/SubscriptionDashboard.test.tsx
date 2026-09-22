import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineListResult, ProviderSummary } from "../shared/protocol";
import { clearDraftEngineMemory, readDraftEngineMemory } from "./DraftEngineMemory";
import { SubscriptionDashboard } from "./SubscriptionDashboard";

let container: HTMLDivElement;
let root: Root;

const inventory: EngineListResult = {
  engines: [
    {
      id: "grok",
      display_name: "Grok",
      protocol: "acp",
      enabled: true,
      binary_ok: true,
      models: [
        { id: "grok-4.5", display_name: "Grok 4.5" },
        { id: "grok-4.6", display_name: "Grok 4.6", is_default: true },
      ],
      latest_request: { status: "failed", model: "grok-4.5", error: "login expired" },
    },
    {
      id: "codex",
      display_name: "Codex",
      enabled: false,
      binary_ok: true,
    },
  ],
};

const providers: ProviderSummary[] = [
  {
    name: "openai",
    type: "openai",
    model: "gpt-4.1",
    api_key_configured: true,
  },
  {
    name: "xai-subscription",
    type: "xai-subscription",
    model: "grok-4",
    api_key_configured: true,
    models: [{ id: "grok-4", display_name: "Grok 4" }, { id: "grok-4-fast" }],
    latest_request: {
      status: "completed",
      model: "grok-4",
      usage_reported: true,
      input_tokens: 20,
      output_tokens: 5,
    },
  },
];

beforeEach(() => {
  window.wuu = { ...window.wuu, listEngines: vi.fn().mockResolvedValue(inventory) };
  clearDraftEngineMemory();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  clearDraftEngineMemory();
});

async function render(onSelectBuiltinModel = vi.fn()) {
  await act(async () => {
    root.render(
      <SubscriptionDashboard
        inventory={inventory}
        providers={providers}
        onSelectBuiltinModel={onSelectBuiltinModel}
      />,
    );
  });
  return onSelectBuiltinModel;
}

describe("SubscriptionDashboard", () => {
  it("lists external engines and built-in subscriptions without merging their credentials", async () => {
    await render();
    expect(container.querySelector('[data-testid="subscription-engine-grok"]')?.textContent).toContain("可用");
    expect(container.querySelector('[data-testid="subscription-engine-codex"]')?.textContent).toContain("不可用");
    expect(container.querySelector('[data-testid="subscription-builtin-xai-subscription"] button[aria-haspopup="menu"]')).not.toBeNull();
    expect(container.textContent).not.toContain("openai");
    const failed = container.querySelector('[data-testid="subscription-engine-grok"] details')!;
    expect(failed.textContent).toContain("login expired");
    expect(failed.textContent).toContain("grok-4.5");
    expect(failed.querySelector('[data-testid="engine-auth-discover"]')).not.toBeNull();
    const completed = container.querySelector('[data-testid="subscription-builtin-xai-subscription"] details')!;
    expect(completed.textContent).toContain("20");
    expect(completed.textContent).toContain("5");
  });

  it("switches an external model through draft memory and a built-in model through the provider save", async () => {
    const onSelect = await render(vi.fn().mockResolvedValue(undefined));
    const grok = container.querySelector<HTMLButtonElement>('[data-testid="subscription-engine-grok"] button[aria-haspopup="menu"]')!;
    await act(async () => { grok.click(); });
    await act(async () => { document.querySelector<HTMLButtonElement>('[role="menuitemradio"][data-value="grok-4.5"]')!.click(); });
    expect(readDraftEngineMemory()).toMatchObject({ engine: "grok", model: "grok-4.5" });

    const xai = container.querySelector<HTMLButtonElement>('[data-testid="subscription-builtin-xai-subscription"] button[aria-haspopup="menu"]')!;
    await act(async () => { xai.click(); });
    await act(async () => { document.querySelector<HTMLButtonElement>('[role="menuitemradio"][data-value="grok-4-fast"]')!.click(); });
    expect(onSelect).toHaveBeenCalledWith("xai-subscription", "grok-4-fast");
  });

  it("shows remaining allowance independently of local tokens and never draws unknown quota", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: inventory.engines.map((engine) => engine.id === "grok" ? {
      ...engine,
      local_usage: { input_tokens: 99999, output_tokens: 10, cache_creation_tokens: 0, cache_read_tokens: 0, reported_turns: 1 },
      quota: { status: "available", checked_at: new Date().toISOString(), windows: [
        { id: "short", used_percent: 30, window_minutes: 300 },
        { id: "long", used_percent: 120, window_minutes: 10080 },
      ] },
    } : engine) });
    await render();
    const meters = container.querySelectorAll("meter");
    expect([...meters].map((meter) => meter.value)).toEqual([70, 0]);
    expect(container.querySelector('[data-testid="subscription-engine-codex"] meter')).toBeNull();
    expect(window.wuu.listEngines).toHaveBeenCalledWith({ include_quota: true });
  });

  it("does not present a reset or old snapshot as current remaining allowance", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{ ...inventory.engines[0], quota: {
      status: "available", checked_at: new Date(Date.now() - 600_000).toISOString(),
      windows: [{ id: "old", used_percent: 30 }],
    } }] });
    await render();
    expect(container.querySelector("meter")).toBeNull();
  });

  it("recovers from refresh failure without losing model controls", async () => {
    vi.mocked(window.wuu.listEngines).mockRejectedValueOnce(new Error("offline"));
    await render();
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("offline");
    expect(container.querySelector('button[aria-haspopup="menu"]')).not.toBeNull();
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it("refreshes the accepted catalog and replaces a failed request without retaining its error", async () => {
    await render();
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{
      ...inventory.engines[0], models: [{ id: "new-model" }],
      latest_request: { status: "completed", model: "new-model" },
    }] });
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    const source = container.querySelector('[data-testid="subscription-engine-grok"]')!;
    expect(source.querySelector("details")?.textContent).toContain("new-model");
    expect(source.textContent).not.toContain("login expired");
    await act(async () => { source.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click(); });
    expect(document.querySelector('[role="menuitemradio"][data-value="new-model"]')).not.toBeNull();
    expect(document.querySelector('[role="menuitemradio"][data-value="grok-4.5"]')).toBeNull();
  });
});
