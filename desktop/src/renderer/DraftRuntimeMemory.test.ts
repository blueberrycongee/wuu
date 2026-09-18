import { beforeEach, describe, expect, it } from "vitest";
import type { InitializeResult } from "../shared/protocol";
import { initialState } from "./AppState";
import {
  applyDraftRuntimeMemory,
  clearDraftApproveForMeMemory,
  clearDraftPermissionMemory,
  clearDraftRuntimeMemory,
  lastEffortForRuntimeModel,
  readDraftApproveForMeMemory,
  readDraftPermissionMemory,
  readDraftRuntimeMemory,
  resolveDraftRuntimeMemory,
  seedDraftRuntimeFromMemory,
  writeDraftApproveForMeMemory,
  writeDraftPermissionMemory,
  writeDraftRuntimeMemory,
} from "./DraftRuntimeMemory";

const MEMORY_KEY = "wuu.desktop.lastDraftRuntime";
const PERMISSION_KEY = "wuu.desktop.lastDraftPermissionMode";
const APPROVE_FOR_ME_KEY = "wuu.desktop.lastDraftApproveForMe";

beforeEach(() => {
  window.localStorage.clear();
});

function initialized(overrides?: Partial<InitializeResult>): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "work",
    model: "claude-sonnet",
    variant: "medium",
    effort: "medium",
    workspace_root: "/tmp/project",
    providers: [
      {
        name: "work",
        type: "anthropic",
        model: "claude-sonnet",
        models: [
          {
            id: "claude-sonnet",
            display_name: "Claude Sonnet",
            supported_efforts: ["low", "medium", "high"],
            default_effort: "medium",
          },
        ],
      },
      {
        name: "tokenhub",
        type: "openai-compatible",
        model: "gpt-5.6-sol",
        models: [
          {
            id: "gpt-5.6-sol",
            display_name: "GPT-5.6 Sol",
            supported_efforts: ["low", "medium", "high"],
            default_effort: "medium",
          },
          {
            id: "gpt-5.6-terra",
            display_name: "GPT-5.6 Terra",
            supported_efforts: ["medium", "high"],
            default_effort: "medium",
          },
        ],
      },
    ],
    ...overrides,
  };
}

describe("draft runtime memory", () => {
  it("restores the provider, model and effort last picked in the composer", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });

    expect(resolveDraftRuntimeMemory(initialized())).toEqual({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
  });

  it("seeds a new conversation from the remembered Wuu selection", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });

    expect(applyDraftRuntimeMemory(initialized())).toMatchObject({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      variant: "high",
      effort: "high",
    });
  });

  it("waits for initialize before applying a remembered selection", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });

    expect(resolveDraftRuntimeMemory(undefined)).toBeUndefined();
  });

  it("keeps the stored preference when the provider is not currently offered", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
    const missing = initialized({
      providers: [
        {
          name: "work",
          type: "anthropic",
          model: "claude-sonnet",
          models: [{ id: "claude-sonnet" }],
        },
      ],
    });

    expect(resolveDraftRuntimeMemory(missing)).toBeUndefined();
    expect(readDraftRuntimeMemory()?.provider).toBe("tokenhub");
  });

  it("drops a model the provider no longer reports", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "retired-model",
      effort: "high",
    });

    expect(resolveDraftRuntimeMemory(initialized())).toBeUndefined();
  });

  it("falls back to the model default when the remembered effort is unsupported", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-terra",
      effort: "low",
    });

    expect(resolveDraftRuntimeMemory(initialized())).toEqual({
      provider: "tokenhub",
      model: "gpt-5.6-terra",
      effort: "medium",
    });
  });

  it("keeps the remembered runtime when the provider reports no catalog", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
    const noCatalog = initialized({
      providers: [{ name: "tokenhub", type: "openai-compatible", model: "gpt-5.6-sol" }],
    });

    expect(resolveDraftRuntimeMemory(noCatalog)).toEqual({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
  });

  it("treats corrupted storage as no remembered runtime", () => {
    window.localStorage.setItem(MEMORY_KEY, "not json");
    expect(readDraftRuntimeMemory()).toBeUndefined();

    window.localStorage.setItem(MEMORY_KEY, JSON.stringify({ provider: "   " }));
    expect(readDraftRuntimeMemory()).toBeUndefined();

    window.localStorage.setItem(MEMORY_KEY, JSON.stringify(["tokenhub"]));
    expect(readDraftRuntimeMemory()).toBeUndefined();
  });

  it("clears the memory so the workspace default takes over again", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
    clearDraftRuntimeMemory();

    expect(readDraftRuntimeMemory()).toBeUndefined();
    expect(resolveDraftRuntimeMemory(initialized())).toBeUndefined();
  });

  it("returns the last effort for a previously chosen model", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });

    expect(lastEffortForRuntimeModel("tokenhub", "gpt-5.6-sol")).toBe("high");
    expect(lastEffortForRuntimeModel("tokenhub", "gpt-5.6-terra")).toBeUndefined();
  });

  it("seeds a draft conversation without rewriting an open thread", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
    const draft = seedDraftRuntimeFromMemory({
      ...initialState,
      initialized: initialized(),
    });
    expect(draft.initialized).toMatchObject({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      variant: "high",
      effort: "high",
    });

    const open = seedDraftRuntimeFromMemory({
      ...initialState,
      initialized: initialized(),
      thread: {
        id: "thread-1",
        title: "open",
        preview: "open",
        model_provider: "work",
        model: "claude-sonnet",
        cwd: "/tmp/project",
        status: "idle",
        pinned: false,
        archived: false,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
        turns: [],
      },
    });
    expect(open.initialized?.provider).toBe("work");
    expect(open.initialized?.model).toBe("claude-sonnet");
  });

  it("clears the memory when written without a provider or model", () => {
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
    writeDraftRuntimeMemory({ provider: "", model: "", effort: "" });

    expect(window.localStorage.getItem(MEMORY_KEY)).toBeNull();
  });
});

describe("draft permission memory", () => {
  it("seeds a new conversation with the permission mode last picked in the composer", () => {
    writeDraftPermissionMemory("unconfined");

    expect(readDraftPermissionMemory()).toBe("unconfined");
    expect(
      applyDraftRuntimeMemory(initialized({ permissions: { mode: "standard" } })).permissions,
    ).toEqual({ mode: "unconfined" });
    expect(
      seedDraftRuntimeFromMemory({
        ...initialState,
        initialized: initialized({ permissions: { mode: "standard" } }),
      }).initialized?.permissions?.mode,
    ).toBe("unconfined");
  });

  it("applies the remembered mode even when no provider/model is remembered", () => {
    writeDraftPermissionMemory("read_only");

    const next = applyDraftRuntimeMemory(initialized({ permissions: { mode: "standard" } }));
    expect(next.provider).toBe("work");
    expect(next.model).toBe("claude-sonnet");
    expect(next.permissions?.mode).toBe("read_only");
  });

  it("leaves the workspace default alone when nothing is remembered", () => {
    const base = initialized({ permissions: { mode: "standard" } });
    expect(applyDraftRuntimeMemory(base)).toBe(base);
    expect(
      seedDraftRuntimeFromMemory({ ...initialState, initialized: base }).initialized,
    ).toBe(base);
  });

  it("does not override the permission mode of an open thread", () => {
    writeDraftPermissionMemory("unconfined");
    const open = seedDraftRuntimeFromMemory({
      ...initialState,
      initialized: initialized({ permissions: { mode: "standard" } }),
      thread: {
        id: "thread-1",
        title: "open",
        preview: "open",
        model_provider: "work",
        model: "claude-sonnet",
        permission_mode: "read_only",
        cwd: "/tmp/project",
        status: "idle",
        pinned: false,
        archived: false,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
        turns: [],
      },
    });
    expect(open.initialized?.permissions?.mode).toBe("standard");
  });

  it("ignores unknown or corrupted stored modes", () => {
    window.localStorage.setItem(PERMISSION_KEY, "yolo");
    expect(readDraftPermissionMemory()).toBeUndefined();

    writeDraftPermissionMemory("unconfined");
    writeDraftPermissionMemory("");
    expect(window.localStorage.getItem(PERMISSION_KEY)).toBeNull();
  });

  it("clears the memory so the workspace default takes over again", () => {
    writeDraftPermissionMemory("unconfined");
    clearDraftPermissionMemory();
    expect(readDraftPermissionMemory()).toBeUndefined();
  });
});

describe("draft Approve for me memory", () => {
  it("inherits an explicit off choice across subsequent new conversations", () => {
    writeDraftApproveForMeMemory(true);
    writeDraftApproveForMeMemory(false);
    expect(readDraftApproveForMeMemory()).toBe(false);
    for (let index = 0; index < 2; index++) {
      const next = seedDraftRuntimeFromMemory({
        ...initialState,
        initialized: initialized({ permissions: { mode: "standard", approve_for_me: true } }),
      });
      expect(next.initialized?.permissions).toEqual({ mode: "standard", approve_for_me: false });
    }
  });

  it("seeds a new conversation with the last Approve for me pick", () => {
    writeDraftApproveForMeMemory(true);

    expect(readDraftApproveForMeMemory()).toBe(true);
    expect(window.localStorage.getItem(APPROVE_FOR_ME_KEY)).toBe("1");
    expect(
      applyDraftRuntimeMemory(initialized({ permissions: { mode: "standard" } })).permissions,
    ).toEqual({ mode: "standard", approve_for_me: true });
  });

  it("clears the Approve for me memory so the workspace default takes over again", () => {
    writeDraftApproveForMeMemory(true);
    clearDraftApproveForMeMemory();
    expect(readDraftApproveForMeMemory()).toBeUndefined();
  });
});
