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
      models_error: "catalog unavailable",
    },
    {
      id: "codex",
      display_name: "Codex",
      enabled: false,
      binary_ok: true,
      models: [{ id: "gpt-5.4", display_name: "GPT-5.4" }],
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
    expect(container.querySelector<HTMLButtonElement>('[data-testid="subscription-engine-grok"] button[aria-haspopup="menu"]')?.disabled).toBe(false);
    expect(container.querySelector<HTMLButtonElement>('[data-testid="subscription-engine-codex"] button[aria-haspopup="menu"]')?.disabled).toBe(true);
    expect(container.querySelector('[data-testid="subscription-builtin-xai-subscription"] button[aria-haspopup="menu"]')).not.toBeNull();
    expect(container.textContent).not.toContain("openai");
    const failed = container.querySelector('[data-testid="subscription-engine-grok"]')!;
    expect(failed.querySelector('[data-testid="engine-auth-discover"]')).not.toBeNull();
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
    expect(container.querySelector('[data-testid="subscription-engine-grok"] > [aria-hidden="true"]')).toBeNull();
    expect(window.wuu.listEngines).toHaveBeenCalledWith({ include_quota: true });

    // A manual refresh keeps the loaded allowance instead of placeholders.
    vi.mocked(window.wuu.listEngines).mockReturnValueOnce(new Promise(() => {}));
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    expect(container.querySelectorAll("meter")).toHaveLength(2);
    expect(container.querySelector('[data-testid="subscription-engine-grok"] > [aria-hidden="true"]')).toBeNull();
  });

  it("does not present a reset or old snapshot as current remaining allowance", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{ ...inventory.engines[0], quota: {
      status: "available", checked_at: new Date(Date.now() - 600_000).toISOString(),
      windows: [{ id: "old", used_percent: 30 }],
    } }] });
    await render();
    expect(container.querySelector("meter")).toBeNull();
  });

  it("keeps rows usable while quota loads and recovers from refresh failure", async () => {
    let fail!: (error: Error) => void;
    vi.mocked(window.wuu.listEngines).mockReturnValueOnce(new Promise((_, reject) => { fail = reject; }));
    await render();
    const grok = container.querySelector('[data-testid="subscription-engine-grok"]')!;
    expect(grok.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')?.disabled).toBe(false);
    // Only a runnable engine reserves a quota placeholder; disabled engines
    // and built-in subscriptions never receive one.
    expect(grok.querySelector(':scope > [aria-hidden="true"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="subscription-engine-codex"] > [aria-hidden="true"]')).toBeNull();
    expect(container.querySelector('[data-testid="subscription-builtin-xai-subscription"] > [aria-hidden="true"]')).toBeNull();

    await act(async () => fail(new Error("offline")));
    expect(container.querySelector('[role="alert"]')?.textContent).toBeTruthy();
    expect(container.textContent).not.toContain("offline");
    expect(grok.querySelector(':scope > [aria-hidden="true"]')).toBeNull();
    expect(grok.querySelector('button[aria-haspopup="menu"]')).not.toBeNull();
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it("reports detection until the first inventory arrives and stops when it fails", async () => {
    let fail!: (error: Error) => void;
    vi.mocked(window.wuu.listEngines).mockReturnValueOnce(new Promise((_, reject) => { fail = reject; }));
    await act(async () => {
      root.render(<SubscriptionDashboard onSelectBuiltinModel={vi.fn()} />);
    });
    expect(container.querySelector('[role="status"][aria-busy="true"]')).not.toBeNull();

    await act(async () => fail(new Error("offline")));
    expect(container.querySelector('[role="status"]')).toBeNull();
    expect(container.querySelector('[role="alert"]')?.textContent).toBeTruthy();
  });

  it("refreshes the accepted catalog after a failed discovery", async () => {
    await render();
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{
      id: "grok", display_name: "Grok", protocol: "acp", enabled: true, binary_ok: true,
      models: [{ id: "new-model" }],
    }] });
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    const source = container.querySelector('[data-testid="subscription-engine-grok"]')!;
    expect(source.querySelector('[data-testid="engine-auth-discover"]')).toBeNull();
    await act(async () => { source.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click(); });
    expect(document.querySelector('[role="menuitemradio"][data-value="new-model"]')).not.toBeNull();
    expect(document.querySelector('[role="menuitemradio"][data-value="grok-4.5"]')).toBeNull();
  });

  it("summarizes engine diagnostics and recovers through explicit sign-in without exposing raw logs", async () => {
    const diagnostic = "session/new: engine RPC error -32603: Internal error\n" + "[WARNING] synthetic agent stderr /example/private/config\n".repeat(30);
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{
      ...inventory.engines[0], models: [], models_error: diagnostic,
    }] });
    window.wuu.listEngineAuthMethods = vi.fn().mockRejectedValueOnce(new Error(diagnostic))
      .mockResolvedValue({ methods: [{ id: "browser", name: "Browser" }], authenticated: false });
    window.wuu.authenticateEngine = vi.fn().mockResolvedValue({ methods: [], authenticated: true });
    await render();
    const source = container.querySelector('[data-testid="subscription-engine-grok"]')!;
    expect(source.innerHTML).not.toContain("synthetic agent stderr");
    expect(source.textContent).not.toContain("login expired");
    expect(window.wuu.listEngineAuthMethods).not.toHaveBeenCalled();
    await act(async () => source.querySelector<HTMLButtonElement>('[data-testid="engine-auth-discover"]')!.click());
    expect(source.querySelector('[role="status"]')?.textContent).toBeTruthy();
    expect(source.innerHTML).not.toContain("synthetic agent stderr");
    await act(async () => source.querySelector<HTMLButtonElement>('[data-testid="engine-auth-discover"]')!.click());
    expect(window.wuu.authenticateEngine).not.toHaveBeenCalled();
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{ ...inventory.engines[0], models_error: undefined }] });
    await act(async () => source.querySelector<HTMLButtonElement>('[data-testid="engine-auth-method"]')!.click());
    expect(window.wuu.authenticateEngine).toHaveBeenCalledWith("grok", "browser");
    expect(source.querySelector('[data-testid="engine-auth-discover"]')).toBeNull();
    expect(source.querySelector('button[aria-haspopup="menu"]')).not.toBeNull();
  });
});
