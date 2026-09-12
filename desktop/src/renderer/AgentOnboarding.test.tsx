import { act, useState, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult, NamedAgent } from "../shared/protocol";
import { AgentOnboarding, createAgentOnboardingDraft, type AgentOnboardingDraft } from "./AgentOnboarding";
import { agentAvatarConfig } from "./AgentAvatarMark";

vi.mock("blobatar/react", () => ({ Blobatar: () => null }));

const initialized: InitializeResult = {
  protocol_version: "1", workspace_root: "/workspace", provider: "primary", model: "reasoner", effort: "high",
  providers: [
    { name: "primary", type: "openai-compatible", model: "reasoner", api_key_configured: true, models: [
      { id: "reasoner", supported_efforts: ["low", "high"], default_effort: "low" },
      { id: "adaptive", variants: [{ id: "medium" }, { id: "max" }], supported_efforts: ["legacy"], default_variant: "max" },
      { id: "plain" },
      { id: "toggle", capabilities: { chat: true, tools: true, structured_output: false, streaming: true, system_role: true, reasoning: true } },
    ] },
    { name: "secondary", type: "anthropic", model: "writer", api_key_configured: true, models: [{ id: "writer", supported_efforts: ["medium", "high"] }] },
    { name: "not-connected", type: "openai-codex", model: "unused", connection_locked: true, codex_credential_source: "codex-cli" },
  ],
};
const agent: NamedAgent = { id: "created-agent", name: "Ada", avatar_key: "abstract-1", memory_dir: "/agents/ada", autostart: false, created_at: "2026-09-12T00:00:00Z" };

let container: HTMLDivElement;
let root: Root;
let currentDraft: AgentOnboardingDraft;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  vi.clearAllMocks();
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

async function mount(overrides: Partial<ComponentProps<typeof AgentOnboarding>> = {}): Promise<{ create: ReturnType<typeof vi.fn>; open: ReturnType<typeof vi.fn>; close: ReturnType<typeof vi.fn> }> {
  const create = vi.fn().mockResolvedValue(agent);
  const open = vi.fn().mockResolvedValue(undefined);
  const close = vi.fn();
  function Harness(): JSX.Element {
    const [draft, setDraft] = useState(overrides.draft ?? createAgentOnboardingDraft(overrides.initialized ?? initialized));
    currentDraft = draft;
    return <AgentOnboarding initialized={initialized} onCreate={create} onOpenConversation={open} onClose={close} {...overrides} draft={draft} onDraftChange={(next) => { setDraft(next); overrides.onDraftChange?.(next); }} />;
  }
  await act(async () => root.render(<Harness />));
  return { create, open, close };
}

function query<T extends Element = HTMLElement>(selector: string): T {
  const element = document.querySelector<T>(selector);
  expect(element, selector).not.toBeNull();
  return element!;
}
async function click(selector: string): Promise<void> {
  await act(async () => query<HTMLElement>(selector).click());
}
async function type(name: string, value: string): Promise<void> {
  const input = query<HTMLInputElement | HTMLTextAreaElement>(`[name="${name}"]`);
  const prototype = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  await act(async () => {
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
async function choose(field: string, value: string): Promise<void> {
  await click(`[data-field="${field}"]`);
  const option = [...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]')].find((item) => item.dataset.value === value || item.dataset.value === `${currentDraft.provider}\u0000${value}`);
  expect(option).toBeTruthy();
  await act(async () => option!.click());
}
async function next(): Promise<void> {
  await type("agent-name", "Ada");
}
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (error: Error) => void } {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe("AgentOnboarding", () => {
  it("can start and edit a draft in a LAN web context without randomUUID", async () => {
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    const { create } = await mount();
    await next();
    await click('[data-action="submit"]');
    expect(create).toHaveBeenCalledOnce();
    expect(create.mock.calls[0][0].request_id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });

  it("shows the model selector in the conversation and creates the selected identity without a naming step", async () => {
    const { create, open, close } = await mount();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(query<HTMLTextAreaElement>(".channel-composer textarea").disabled).toBe(true);
    const identity = { ...currentDraft };
    expect(agentAvatarConfig(identity.avatarKey).accessory).toBe("none");
    await click('[data-action="submit"]');
    expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
      request_id: identity.requestId, name: expect.any(String),
      avatar_key: identity.avatarKey, engine_override: "wuu",
      provider_override: "primary", model_override: "reasoner", effort_override: "high",
    }));
    expect(create.mock.calls[0][0].name.length).toBeGreaterThan(0);
    expect(open).toHaveBeenCalledExactlyOnceWith(agent);
    expect(close).toHaveBeenCalledOnce();
  });

  it("switches to each model's supported effort and excludes unavailable providers", async () => {
    await mount();
    await next();
    await click('[data-field="agent-model"]');
    expect([...document.querySelectorAll('[role="menuitemradio"]')].some((item) => item.getAttribute("data-value")?.startsWith("not-connected"))).toBe(false);
    await act(async () => query('[role="menu"]').dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));

    await choose("agent-model", "adaptive");
    expect(currentDraft.effort).toBe("max");
    await click('[data-field="agent-effort"]');
    expect([...document.querySelectorAll('[role="menuitemradio"]')].map((item) => item.getAttribute("data-value"))).toEqual(["medium", "max"]);
    await click('[role="menuitemradio"][data-value="medium"]');
    await choose("agent-model", "plain");
    expect(currentDraft.effort).toBe("");
    expect(document.querySelector('[data-field="agent-effort"]')).toBeNull();
    await choose("agent-model", "toggle");
    await choose("agent-effort", "none");
    expect(currentDraft.effort).toBe("none");
    await choose("agent-model", "secondary\u0000writer");
    expect(currentDraft.model).toBe("writer");
    expect(currentDraft.effort).toBe("medium");
  });

  it("blocks repeated submission and dismissal while the creation request is pending", async () => {
    const result = deferred<NamedAgent>();
    const create = vi.fn().mockReturnValue(result.promise);
    const close = vi.fn();
    await mount({ onCreate: create, onClose: close });
    await next();
    await act(async () => {
      query("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      query("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      query<HTMLButtonElement>('[data-action="close"]').click();
    });
    expect(create).toHaveBeenCalledOnce();
    expect(close).not.toHaveBeenCalled();
    expect(query<HTMLButtonElement>('[data-action="close"]').disabled).toBe(true);
    await act(async () => result.resolve(agent));
    expect(close).toHaveBeenCalledOnce();
  });

  it("retries a failed creation with the same request identity until the draft content changes", async () => {
    const create = vi.fn().mockRejectedValue(new Error("provider temporarily unavailable"));
    await mount({ onCreate: create });
    await next();
    const requestID = currentDraft.requestId;
    await click('[data-action="submit"]');
    expect(query('[role="alert"]').textContent).toContain("provider temporarily unavailable");
    await click('[data-action="submit"]');
    expect(create.mock.calls[0][0].request_id).toBe(requestID);
    expect(create.mock.calls[1][0].request_id).toBe(requestID);
    await choose("agent-model", "reasoner");
    expect(currentDraft.requestId).toBe(requestID);
    await type("agent-name", "Grace");
    expect(currentDraft.requestId).not.toBe(requestID);
    await click('[data-action="submit"]');
    await click('[data-action="submit"]');
    expect(create.mock.calls[2][0].name).toBe("Grace");
    expect(create.mock.calls[2][0].request_id).toBe(currentDraft.requestId);
  });

  it("keeps a created identity when opening its conversation fails and retries only the opening", async () => {
    const open = vi.fn().mockRejectedValueOnce(new Error("connection lost")).mockResolvedValue(undefined);
    const { create, close } = await mount({ onOpenConversation: open });
    await next();
    await click('[data-action="submit"]');
    expect(currentDraft.createdAgent).toEqual(agent);
    expect(query('[role="alert"]').textContent).toContain("connection lost");
    expect(document.activeElement).toBe(query('[data-action="submit"]'));
    expect(query<HTMLButtonElement>('[data-field="agent-model"]').disabled).toBe(true);
    expect(close).not.toHaveBeenCalled();
    await click('[data-action="submit"]');
    expect(create).toHaveBeenCalledOnce();
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls.every(([value]) => value === agent)).toBe(true);
    expect(close).toHaveBeenCalledOnce();
  });

  it("resumes a draft after connecting a provider without discarding its identity", async () => {
    const disconnected = { ...initialized, providers: [] };
    const manage = vi.fn();
    const { create } = await mount({ initialized: disconnected, onManageProviders: manage });
    await type("agent-name", "Ada");
    expect(document.querySelector('[data-action="submit"]')).toBeNull();
    await act(async () => query("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(create).not.toHaveBeenCalled();
    await click('[data-action="manage-providers"]');
    expect(manage).toHaveBeenCalledOnce();
    const saved = currentDraft;
    await act(async () => root.unmount());
    root = createRoot(container);
    await mount({ initialized, draft: saved });
    expect(currentDraft.name).toBe(saved.name);
    expect(currentDraft.role).toBe(saved.role);
    expect(currentDraft.avatarKey).toBe(saved.avatarKey);
    expect(currentDraft.provider).toBe("primary");
    expect(query<HTMLButtonElement>('[data-action="submit"]').disabled).toBe(false);
  });

  it("lets the model menu consume Escape before dismissing onboarding", async () => {
    const { close } = await mount();
    await next();
    await click('[data-field="agent-model"]');
    await act(async () => query('[role="menu"]').dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(close).not.toHaveBeenCalled();
    await click('[data-action="close"]');
    expect(close).toHaveBeenCalledOnce();
  });

  it("does not take focus back from the newly opened conversation on completion", async () => {
    const previous = document.createElement("button");
    const composer = document.createElement("textarea");
    document.body.append(previous, composer);
    previous.focus();
    const originalDraft = { ...createAgentOnboardingDraft(initialized), name: "Ada", step: "model" as const };
    function Harness(): JSX.Element | null {
      const [draft, setDraft] = useState<AgentOnboardingDraft>(originalDraft);
      const [visible, setVisible] = useState(true);
      return visible ? <AgentOnboarding draft={draft} onDraftChange={setDraft} initialized={initialized} onCreate={async () => agent} onOpenConversation={async () => { composer.focus(); }} onClose={() => setVisible(false)} /> : null;
    }
    try {
      await act(async () => root.render(<Harness />));
      await click('[data-action="submit"]');
      expect(document.querySelector('[role="dialog"]')).toBeNull();
      expect(document.activeElement).toBe(composer);
    } finally {
      previous.remove();
      composer.remove();
    }
  });

});
