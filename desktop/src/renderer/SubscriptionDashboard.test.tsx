import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineListResult, ProviderSummary, SubscriptionQuota } from "../shared/protocol";
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
      capabilities: ["account-quota"],
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

function quota(overrides: Partial<SubscriptionQuota> = {}): SubscriptionQuota {
  return {
    status: "available",
    kind: "subscription",
    checked_at: new Date().toISOString(),
    observed_at: new Date().toISOString(),
    account: { id: "opaque-account-id", label: "Example account", source: "Codex CLI" },
    windows: [{ id: "weekly", label: "Weekly", used_percent: 25, window_minutes: 10080 }],
    ...overrides,
  };
}

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

async function render(onSelectBuiltinModel = vi.fn(), parentInventory = inventory, parentProviders: ProviderSummary[] | null = providers) {
  await act(async () => {
    root.render(
      <SubscriptionDashboard
        inventory={parentInventory}
        providers={parentProviders ?? undefined}
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
    expect(window.wuu.listEngines).toHaveBeenCalledWith({ include_quota: true });
    expect(container.textContent).not.toContain("99999");

    // A manual refresh keeps the loaded allowance on screen.
    vi.mocked(window.wuu.listEngines).mockReturnValueOnce(new Promise(() => {}));
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    expect(container.querySelectorAll("meter")).toHaveLength(2);
  });

  it("keeps refreshed provider quota when parent inventory and provider models rerender", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({
      engines: inventory.engines,
      subscription_providers: [{ ...providers[1], quota: quota({ account: { id: "opaque-key-account", label: "Work account", source: "API key" } }) }],
    });
    await render();
    const account = container.querySelector('[data-testid="subscription-builtin-xai-subscription"]')!;
    expect(account.textContent).toContain("Work account");
    expect(account.textContent).toContain("API key");
    expect(account.querySelector("meter")?.value).toBe(75);

    await act(async () => root.render(<SubscriptionDashboard
      inventory={inventory}
      providers={[{ ...providers[1], model: "grok-4.2", models: [{ id: "grok-4.2" }] }, providers[0]]}
      onSelectBuiltinModel={vi.fn()}
    />));
    const rerendered = container.querySelector('[data-testid="subscription-builtin-xai-subscription"]')!;
    expect(rerendered.textContent).toContain("Work account");
    expect(rerendered.querySelector<HTMLMeterElement>("meter")?.value).toBe(75);
    expect(rerendered.textContent).toContain("grok-4.2");
    await act(async () => root.render(<SubscriptionDashboard inventory={inventory}
      providers={[providers[0]]} onSelectBuiltinModel={vi.fn()} />));
    expect(container.querySelector('[data-testid="subscription-builtin-xai-subscription"]')).toBeNull();
  });

  it("distinguishes unknown, zero, unlimited, balances and currencies without combining accounts", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [], subscription_providers: [
      { name: "unknown-key", type: "openai", model: "m", api_key_configured: true, quota: quota({
        account: { id: "account-unknown", label: "Unknown account", source: "API key" },
        windows: [{ id: "missing-percent", label: "Requests", display: "250 calls used" }],
      }) },
      { name: "empty-plan", type: "anthropic", model: "m", api_key_configured: true, quota: quota({
        kind: "plan", account: { id: "account-zero", label: "Zero account", source: "API key" },
        windows: [{ id: "zero", label: "Included usage", used_percent: 100 }],
      }) },
      { name: "unlimited-key", type: "google", model: "m", api_key_configured: true, quota: quota({
        kind: "balance", account: { id: "account-unlimited", label: "Unlimited account", source: "API key" },
        windows: [{ id: "unlimited", label: "Context", unlimited: true }],
        balances: [{ currency: "USD", amount: "9007199254740993.123456789" }, { currency: "EUR", amount: "12.50" }],
      }) },
    ] });
    await render(undefined, inventory, null);
    const unknown = container.querySelector('[data-testid="subscription-builtin-unknown-key"]')!;
    const zero = container.querySelector('[data-testid="subscription-builtin-empty-plan"]')!;
    const unlimited = container.querySelector('[data-testid="subscription-builtin-unlimited-key"]')!;
    expect(unknown.querySelector("meter")).toBeNull();
    expect(unknown.querySelector('button[aria-haspopup="menu"]')).toBeNull();
    expect(unknown.textContent).toContain("Unknown account");
    expect(unknown.textContent).toContain("250 calls used");
    expect(zero.querySelector("meter")?.value).toBe(0);
    expect(unlimited.querySelector("meter")).toBeNull();
    expect(unlimited.textContent).toContain("9007199254740993.123456789");
    expect(unlimited.textContent).toContain("USD");
    expect(unlimited.textContent).toContain("EUR");
    expect(container.textContent).not.toContain("opaque-account-id");
  });

  it("retains stale snapshots as stale, never draws expired meters, and translates safe failure states", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [], subscription_providers: [
      { name: "stale-key", type: "openai", model: "m", api_key_configured: true, quota: quota({
        status: "stale", checked_at: new Date().toISOString(), observed_at: new Date(Date.now() - 86_400_000).toISOString(),
        expires_at: new Date(Date.now() - 1).toISOString(),
        windows: [{ id: "last-known", label: "Monthly", used_percent: 45, window_minutes: 43200, resets_at: new Date(Date.now() - 1).toISOString() }],
      }) },
      { name: "sign-in-key", type: "openai", model: "m", quota: quota({ status: "sign_in", account: { id: "unverified", label: "Unverified account", source: "provider" }, windows: undefined }) },
      { name: "unsupported-key", type: "openai", model: "m", quota: quota({ status: "unsupported", windows: undefined }) },
      { name: "failed-key", type: "openai", model: "m", quota: quota({ status: "unavailable", error_code: "network", windows: undefined }) },
    ] });
    await render(undefined, inventory, null);
    const stale = container.querySelector('[data-testid="subscription-builtin-stale-key"]')!;
    expect(stale.querySelector("meter")).toBeNull();
    expect(stale.textContent).toContain("55");
    expect(stale.textContent).toContain("Example account");
    const signIn = container.querySelector('[data-testid="subscription-builtin-sign-in-key"]')!;
    expect(signIn.textContent).toBeTruthy();
    expect(signIn.textContent).not.toContain("Unverified account");
    const unsupported = container.querySelector('[data-testid="subscription-builtin-unsupported-key"]')!;
    expect(unsupported.textContent).toBeTruthy();
    expect(unsupported.textContent).not.toContain("Example account");
    const failed = container.querySelector('[data-testid="subscription-builtin-failed-key"]')!;
    expect(failed.textContent).toBeTruthy();
    expect(failed.textContent).not.toContain("network");
    expect(failed.textContent).not.toContain("Example account");
  });

  it("keeps the exact reset instant accessible while the visible countdown ages without requests", async () => {
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    try {
      const observed = Date.UTC(2026, 8, 30, 12, 0);
      vi.setSystemTime(observed);
      const reset = new Date(observed + 90_000).toISOString();
      vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{ ...inventory.engines[0], quota: quota({
        windows: [{ id: "short", used_percent: 20, resets_at: reset }],
      }) }] });
      await render();
      const resetTime = container.querySelector<HTMLTimeElement>("time[datetime]")!;
      expect(resetTime).not.toBeNull();
      expect(resetTime.dateTime).toBe(reset);
      expect(resetTime.getAttribute("aria-label")).toBe(resetTime.title);
      expect(resetTime.title).toContain("2026");
      const original = resetTime.textContent;
      await act(async () => vi.advanceTimersByTime(60_000));
      expect(resetTime.textContent).not.toBe(original);
      expect(window.wuu.listEngines).toHaveBeenCalledTimes(1);
      await act(async () => vi.advanceTimersByTime(60_000));
      expect(container.querySelector("meter")).toBeNull();
      expect(resetTime.dateTime).toBe(reset);
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not replace a known snapshot with empty data while manual refresh is pending or fails", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{
      ...inventory.engines[0], quota: quota({ windows: [{ id: "known", used_percent: 20 }] }),
    }] });
    await render();
    const request = vi.mocked(window.wuu.listEngines);
    let fail!: (error: Error) => void;
    request.mockReturnValueOnce(new Promise((_, reject) => { fail = reject; }));
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    expect(container.querySelector<HTMLMeterElement>('[data-testid="subscription-engine-grok"] meter')?.value).toBe(80);
    await act(async () => fail(new Error("offline")));
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
  });

  it("does not present a reset or old snapshot as current remaining allowance", async () => {
    vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{ ...inventory.engines[0], quota: {
      status: "available", checked_at: new Date(Date.now() - 600_000).toISOString(),
      expires_at: new Date(Date.now() - 1).toISOString(),
      windows: [{ id: "old", used_percent: 30 }],
    } }] });
    await render();
    expect(container.querySelector("meter")).toBeNull();
  });

  it("reserves quota only for engines that report it and recovers from refresh failure", async () => {
    const engines = inventory.engines.map((engine) => engine.id === "codex" ? { ...engine, enabled: true } : engine);
    let fail!: (error: Error) => void;
    vi.mocked(window.wuu.listEngines).mockReturnValueOnce(new Promise((_, reject) => { fail = reject; }));
    await render(vi.fn(), { engines });
    const codex = container.querySelector('[data-testid="subscription-engine-codex"]')!;
    const placeholder = ':scope > [aria-hidden="true"]';
    expect(codex.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')?.disabled).toBe(false);
    expect(codex.querySelector(placeholder)).not.toBeNull();
    // Runnable engines without an allowance and built-in subscriptions never
    // receive quota, so they reserve nothing that would later collapse.
    expect(container.querySelector('[data-testid="subscription-engine-grok"]')!.querySelector(placeholder)).toBeNull();
    expect(container.querySelector('[data-testid="subscription-builtin-xai-subscription"]')!.querySelector(placeholder)).toBeNull();

    await act(async () => fail(new Error("offline")));
    expect(container.querySelector('[role="alert"]')?.textContent).toBeTruthy();
    expect(container.textContent).not.toContain("offline");
    expect(codex.querySelector(placeholder)).toBeNull();
    expect(codex.querySelector('button[aria-haspopup="menu"]')).not.toBeNull();

    vi.mocked(window.wuu.listEngines).mockResolvedValueOnce({ engines: engines.map((engine) => engine.id === "codex" ? {
      ...engine, quota: { status: "available", checked_at: new Date().toISOString(), windows: [{ id: "short", used_percent: 40 }] },
    } : engine) });
    await act(async () => { container.querySelector<HTMLButtonElement>("header button")!.click(); });
    expect(container.querySelector('[role="alert"]')).toBeNull();
    expect(codex.querySelector("meter")?.value).toBe(60);
    expect(codex.querySelector(placeholder)).toBeNull();
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

it("switches the Codex credential source without changing the model selection and checks authentication", async () => {
 const provider: ProviderSummary = { name: "codex-login", type: "openai-codex", model: "test-model", codex_credential_source: "wuu-auth-store" };
 const updated = { ...provider, reuse_codex_credentials: true, codex_credential_source: "codex-cli" };
 const useLogin = vi.fn().mockResolvedValue({ providers: [updated] });
 const check = vi.fn().mockRejectedValue(new Error("Authentication transport failed: secret-fixture-token /private-fixture/account.json"));
 window.wuu.useCodexCredentials = useLogin;
 window.wuu.loadCodexModels = check;
 const select = vi.fn();
 await act(async () => root.render(<SubscriptionDashboard providers={[provider]} inventory={inventory} onSelectBuiltinModel={select} />));
 const source = container.querySelector('[data-testid="subscription-builtin-codex-login"]')!;
 const openActions = async () => act(async () => source.querySelector<HTMLButtonElement>('[data-testid="subscription-account-actions"]')!.click());
 await openActions();
 expect(useLogin).not.toHaveBeenCalled();
 expect(check).not.toHaveBeenCalled();
 await act(async () => document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')[0]!.click());
 expect(useLogin).toHaveBeenCalledWith("codex-login");
 expect(select).not.toHaveBeenCalled();
 expect(check).toHaveBeenCalledWith("codex-login");
 expect(container.querySelector('[role="alert"]')?.textContent).toBeTruthy();
 expect(container.textContent).not.toContain("secret-fixture-token");
 expect(container.textContent).not.toContain("/private-fixture/account.json");
 check.mockResolvedValue({ providers: [updated], models: [{ id: "test-model" }] });
 await openActions();
 await act(async () => document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')[1]!.click());
 expect(check).toHaveBeenCalledTimes(2);
 expect(useLogin).toHaveBeenCalledTimes(1);
 expect(container.querySelector('[role="alert"]')).toBeNull();
 expect(window.wuu.listEngines).toHaveBeenCalledTimes(3);
});
