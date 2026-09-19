import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult, Thread } from "../shared/protocol";
import { initialState, type AppState } from "./AppState";
import type { CodexModelLoadState, CodexRuntimeMenu } from "./ComposerTypes";
import { readDraftApproveForMeMemory, readDraftPermissionMemory, readDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { createRuntimeSettingsActions } from "./RuntimeSettingsActions";

const originalWuu = (window as unknown as { wuu?: unknown }).wuu;

function restoreWuu(): void {
  if (originalWuu === undefined) {
    delete (window as unknown as { wuu?: unknown }).wuu;
    return;
  }
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: originalWuu,
  });
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  restoreWuu();
  window.localStorage.clear();
});

function initialized(overrides: Partial<InitializeResult> = {}): InitializeResult {
  return {
    protocol_version: "1",
    provider: "codex",
    model: "gpt-5",
    effort: "medium",
    variant: "medium",
    workspace_root: "/tmp/project-1",
    permissions: { mode: "standard" },
    providers: [
      {
        name: "codex",
        type: "chatgpt-codex",
        model: "gpt-5",
        base_url: "https://old.example.test",
      },
    ],
    advanced_settings: {
      max_steps: 10,
      max_context_tokens: 100000,
      temperature: 0,
      disable_auto_compact: false,
    },
    general_settings: {
      mcp_server_enabled: {},
    },
    ...overrides,
  };
}

function thread(id = "thread-1"): Thread {
  return {
    id,
    title: id,
    preview: id,
    model_provider: "codex",
    model: "gpt-5",
    cwd: "/tmp/project-1",
    status: "in_progress",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
}

function installWuuApi(): {
  updateRuntimeSettings: ReturnType<typeof vi.fn>;
  updateAdvancedSettings: ReturnType<typeof vi.fn>;
  updateGeneralSettings: ReturnType<typeof vi.fn>;
  removeProvider: ReturnType<typeof vi.fn>;
  loadCodexModels: ReturnType<typeof vi.fn>;
  interruptTurn: ReturnType<typeof vi.fn>;
} {
  const updateRuntimeSettings = vi.fn().mockResolvedValue({
    provider: "codex",
    model: "gpt-5.1",
    effort: "high",
    variant: "high",
    permissions: { mode: "read_only" },
    providers: [
      {
        name: "codex",
        type: "chatgpt-codex",
        model: "gpt-5.1",
        base_url: "https://new.example.test",
      },
    ],
  });
  const updateAdvancedSettings = vi.fn().mockResolvedValue({
    advanced_settings: {
      max_steps: 20,
      max_context_tokens: 120000,
      temperature: 0.2,
      disable_auto_compact: true,
    },
  });
  const updateGeneralSettings = vi.fn().mockResolvedValue({
    general_settings: {
      git_attribution_enabled: false,
      mcp_server_enabled: { local: false },
    },
  });
  const removeProvider = vi.fn().mockResolvedValue({
    provider: "codex",
    model: "gpt-5",
    effort: "medium",
    variant: "medium",
  });
  const loadCodexModels = vi.fn().mockResolvedValue({
    provider: "codex",
    model: "gpt-5.1",
    effort: "high",
    variant: "high",
    models: [{ slug: "gpt-5.1", supported_in_api: true }],
  });
  const interruptTurn = vi.fn().mockResolvedValue({});
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      updateRuntimeSettings,
      updateAdvancedSettings,
      updateGeneralSettings,
      removeProvider,
      loadCodexModels,
      interruptTurn,
    },
  });
  return {
    updateRuntimeSettings,
    updateAdvancedSettings,
    updateGeneralSettings,
    removeProvider,
    loadCodexModels,
    interruptTurn,
  };
}

function buildActions({
  initial = {
    ...initialState,
    initialized: initialized(),
    thread: thread(),
    status: "loading",
  },
  viewContextSwitchPending = false,
  codexModels = { loading: false, error: "", models: [] },
}: {
  initial?: AppState;
  viewContextSwitchPending?: boolean;
  codexModels?: CodexModelLoadState;
} = {}) {
  let appState = initial;
  let modelState = codexModels;
  let runtimeMenuOpen = true;
  let accessMenuOpen = true;
  let branchMenuOpen = true;
  let codexRuntimeMenu: CodexRuntimeMenu = null;
  const clearThreadPendingComposerMessages = vi.fn();
  const variantByModel = new Map<string, string>();
  const actions = createRuntimeSettingsActions({
    getAppState: () => appState,
    setAppState: (update) => {
      appState = typeof update === "function" ? update(appState) : update;
    },
    getViewContextSwitchPending: () => viewContextSwitchPending,
    getCodexModels: () => modelState,
    setCodexModels: (update) => {
      modelState = typeof update === "function" ? update(modelState) : update;
    },
    setRuntimeMenuOpen: (open) => {
      runtimeMenuOpen = open;
    },
    setAccessMenuOpen: (open) => {
      accessMenuOpen = open;
    },
    setBranchMenuOpen: (open) => {
      branchMenuOpen = open;
    },
    setCodexRuntimeMenu: (update) => {
      codexRuntimeMenu =
        typeof update === "function" ? update(codexRuntimeMenu) : update;
    },
    clearThreadPendingComposerMessages,
    variantByModel,
  });
  return {
    actions,
    getAppState: () => appState,
    getCodexModels: () => modelState,
    getRuntimeMenus: () => ({
      runtimeMenuOpen,
      accessMenuOpen,
      branchMenuOpen,
      codexRuntimeMenu,
    }),
    clearThreadPendingComposerMessages,
  };
}

describe("createRuntimeSettingsActions", () => {
  it("saves provider defaults without targeting or patching running sessions", async () => {
    const api = installWuuApi();
    const primary = thread();
    const secondary = thread("thread-2");
    const harness = buildActions({ initial: {
      ...initialState, initialized: initialized(), thread: primary,
      secondaryThread: secondary, threads: [primary, secondary], running: true, status: "running",
    } });
    await harness.actions.updateProviderSettings("codex", "gpt-5.1");
    expect(api.updateRuntimeSettings).toHaveBeenCalledWith("codex", "gpt-5.1", undefined, undefined, undefined, undefined, undefined);
    expect(harness.getAppState().thread).toBe(primary);
    expect(harness.getAppState().secondaryThread).toBe(secondary);
    expect(harness.getAppState().running).toBe(true);
    expect(harness.getAppState().status).toBe("running");
    expect(harness.getAppState().initialized?.model).toBe("gpt-5.1");
  });

  it("leaves session status alone when saving provider settings fails", async () => {
    const api = installWuuApi();
    api.updateRuntimeSettings.mockRejectedValue(new Error("save failed"));
    const harness = buildActions({ initial: {
      ...initialState, initialized: initialized(), thread: thread(), running: true, status: "running",
    } });
    await expect(harness.actions.updateProviderSettings("codex", "gpt-5.1")).rejects.toThrow("save failed");
    expect(harness.getAppState().status).toBe("running");
    expect(harness.getAppState().running).toBe(true);
  });

  it("saves trimmed runtime settings and patches initialized runtime state", async () => {
    const api = installWuuApi();
    const primary = thread("thread-1");
    const secondary = thread("thread-2");
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized(),
        thread: primary,
        secondaryThread: secondary,
        threads: [primary, secondary],
        status: "loading",
      },
    });

    await harness.actions.updateRuntimeSettings(
      " codex ",
      " gpt-5.1 ",
      " high ",
      {
        base_url: " https://new.example.test ",
        api_key: " key ",
        auth_token: " token ",
        type: "openai-compatible",
        create_provider: true,
      },
      " high ",
      " read_only ",
    );

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      "codex",
      "gpt-5.1",
      "high",
      {
        base_url: "https://new.example.test",
        api_key: "key",
        auth_token: "token",
        type: "openai-compatible",
        create_provider: true,
      },
      "high",
      "read_only",
      "thread-1",
    );
    expect(harness.getAppState().initialized?.model).toBe("gpt-5.1");
    expect(harness.getAppState().thread?.model).toBe("gpt-5.1");
    expect(harness.getAppState().thread?.model_variant).toBe("high");
    expect(harness.getAppState().secondaryThread?.model).toBe("gpt-5");
    expect(harness.getAppState().threads.map((item) => item.model)).toEqual([
      "gpt-5.1",
      "gpt-5",
    ]);
    expect(harness.getAppState().initialized?.permissions?.mode).toBe(
      "read_only",
    );
    expect(harness.getAppState().status).toBe("ready");
  });

  it("skips a runtime save when nothing changed", async () => {
    const api = installWuuApi();
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized(),
        status: "ready",
      },
    });

    await harness.actions.updateRuntimeSettings(
      "codex",
      "gpt-5",
      "medium",
      undefined,
      "medium",
      "standard",
    );

    expect(api.updateRuntimeSettings).not.toHaveBeenCalled();
  });

  it("does not treat a global variant match as a thread variant no-op", async () => {
    const api = installWuuApi();
    const primary = { ...thread("thread-1"), model_variant: "low" };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ variant: "medium" }),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.updateRuntimeSettings(
      "codex",
      "gpt-5",
      "medium",
      undefined,
      "medium",
      "standard",
    );

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      "codex",
      "gpt-5",
      "medium",
      undefined,
      "medium",
      "standard",
      "thread-1",
    );
  });

  it("preserves the target thread permission when a model-only update returns global permissions", async () => {
    installWuuApi();
    const primary = {
      ...thread("thread-1"),
      permission_mode: "unconfined",
    };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ permissions: { mode: "standard" } }),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.updateRuntimeSettings(
      "codex",
      "gpt-5.1",
      undefined,
      undefined,
      "high",
    );

    expect(harness.getAppState().initialized?.permissions?.mode).toBe(
      "read_only",
    );
    expect(harness.getAppState().thread?.permission_mode).toBe("unconfined");
  });

  it("does not synthesize a thread when runtime settings change without an active thread", async () => {
    installWuuApi();
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized(),
        status: "ready",
      },
    });

    await harness.actions.updateRuntimeSettings(
      "codex",
      "gpt-5.1",
      undefined,
      undefined,
      "high",
    );

    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().secondaryThread).toBeUndefined();
  });

  it("changes effort for the active thread's provider and model", async () => {
    const api = installWuuApi();
    const primary = thread("thread-1");
    const secondary = {
      ...thread("thread-2"),
      model_provider: "anthropic",
      model: "claude-sonnet-4-6",
      model_variant: "medium",
    };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ provider: "codex", model: "gpt-5" }),
        thread: primary,
        secondaryThread: secondary,
        activePane: "secondary",
        threads: [primary, secondary],
        status: "ready",
      },
    });

    await harness.actions.selectRuntimeEffort("high");

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      undefined,
      undefined,
      undefined,
      undefined,
      "high",
      undefined,
      "thread-2",
    );
    // Only the effort click is stamped on the thread; the workspace-effective
    // result must not overwrite the thread's pinned provider/model.
    expect(harness.getAppState().secondaryThread?.model_provider).toBe(
      "anthropic",
    );
    expect(harness.getAppState().secondaryThread?.model).toBe(
      "claude-sonnet-4-6",
    );
    expect(harness.getAppState().secondaryThread?.model_variant).toBe("high");
    expect(harness.getAppState().initialized?.model).toBe("gpt-5.1");
  });

  it("restores each model's own effort after switching away and back", async () => {
    const api = installWuuApi();
    api.updateRuntimeSettings
      .mockResolvedValueOnce({
        provider: "codex",
        model: "model-a",
        effort: "max",
        variant: "max",
      })
      .mockResolvedValueOnce({
        provider: "codex",
        model: "model-b",
        effort: "medium",
        variant: "medium",
      });
    const primary = {
      ...thread("thread-1"),
      model: "model-b",
      model_variant: "medium",
      model_effort: "medium",
    };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ model: "model-b", variant: "medium", providers: [
          { name: "codex", type: "openai-codex", model: "model-b", models: [
            { id: "model-a", supported_efforts: ["medium", "max"] },
            { id: "model-b", supported_efforts: ["medium", "max"] },
          ] },
        ] }),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.selectRuntimeModel("codex", "model-a", "max");
    await harness.actions.selectRuntimeModel("codex", "model-b", "max");

    expect(api.updateRuntimeSettings).toHaveBeenNthCalledWith(
      1,
      "codex",
      "model-a",
      undefined,
      undefined,
      "max",
      undefined,
      "thread-1",
    );
    expect(api.updateRuntimeSettings).toHaveBeenNthCalledWith(
      2,
      "codex",
      "model-b",
      undefined,
      undefined,
      "medium",
      undefined,
      "thread-1",
    );
    expect(harness.getAppState().thread?.model).toBe("model-b");
    expect(harness.getAppState().thread?.model_variant).toBe("medium");
  });

  it("sends an explicit empty variant when resetting effort to the model default", async () => {
    const api = installWuuApi();
    // The thread-scoped reset leaves the workspace selection unchanged, so
    // the workspace-effective result echoes the current defaults.
    api.updateRuntimeSettings.mockResolvedValue({
      provider: "codex",
      model: "gpt-5",
      effort: "medium",
      variant: "medium",
    });
    const primary = {
      ...thread("thread-1"),
      model_variant: "high",
      model_effort: "high",
    };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized(),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.selectRuntimeEffort("");

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      undefined,
      undefined,
      undefined,
      undefined,
      "",
      undefined,
      "thread-1",
    );
    expect(harness.getAppState().thread?.model_variant).toBe("");
    expect(harness.getAppState().thread?.model_effort).toBe("");
    expect(harness.getAppState().initialized?.variant).toBe("medium");
  });

  it("skips the reset when the thread effort is already the model default", async () => {
    const api = installWuuApi();
    const primary = { ...thread("thread-1"), model_variant: "" };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized(),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.selectRuntimeEffort("");

    expect(api.updateRuntimeSettings).not.toHaveBeenCalled();
  });

  it("updates advanced and general settings unless a view switch is pending", async () => {
    const api = installWuuApi();
    const harness = buildActions();

    await harness.actions.updateAdvancedSettings({
      max_steps: 20,
      disable_auto_compact: true,
    });
    await harness.actions.updateGeneralSettings({
      git_attribution_enabled: false,
    });

    expect(api.updateAdvancedSettings).toHaveBeenCalledWith({
      max_steps: 20,
      disable_auto_compact: true,
    });
    expect(api.updateGeneralSettings).toHaveBeenCalledWith({
      git_attribution_enabled: false,
    });
    expect(harness.getAppState().initialized?.advanced_settings?.max_steps).toBe(
      20,
    );
    expect(harness.getAppState().initialized?.general_settings?.git_attribution_enabled).toBe(false);

    const blocked = buildActions({ viewContextSwitchPending: true });
    await blocked.actions.updateAdvancedSettings({ max_steps: 30 });
    expect(api.updateAdvancedSettings).toHaveBeenCalledTimes(1);
  });

  it("loads Codex models once without rewriting the active selection", async () => {
    const api = installWuuApi();
    const harness = buildActions();
    const providers = [{ name: "codex", type: "openai-codex", model: "gpt-5", models: [
      { id: "gpt-6-discovered", supported_efforts: ["medium", "high"] },
    ] }];
    api.loadCodexModels.mockResolvedValue({ provider: "codex", models: [
      { slug: "gpt-5.1", supported_in_api: true },
    ], providers });

    await harness.actions.loadCodexModelsForProvider("codex");
    await harness.actions.loadCodexModelsForProvider("codex");

    expect(api.loadCodexModels).toHaveBeenCalledTimes(1);
    expect(harness.getCodexModels().models).toEqual([
      { slug: "gpt-5.1", supported_in_api: true },
    ]);
    expect(harness.getAppState().initialized?.model).toBe("gpt-5");
    expect(harness.getAppState().initialized?.providers).toEqual(providers);
  });

  it("keeps a pre-conversation model choice local instead of saving provider defaults", async () => {
    const api = installWuuApi();
    const harness = buildActions({ initial: { ...initialState, initialized: initialized(), thread: undefined } });
    const providers = harness.getAppState().initialized?.providers;
    await harness.actions.selectRuntimeModel("codex", "draft-model", "max");
    expect(api.updateRuntimeSettings).not.toHaveBeenCalled();
    expect(harness.getAppState().initialized?.model).toBe("draft-model");
    expect(harness.getAppState().initialized?.variant).toBe("");
    expect(harness.getAppState().initialized?.providers).toEqual(providers);
    expect(readDraftRuntimeMemory()).toEqual({
      provider: "codex",
      model: "draft-model",
      effort: "",
    });
  });

  it("remembers the last composer provider, model, and effort for the next conversation", async () => {
    const api = installWuuApi();
    const harness = buildActions({
      initial: { ...initialState, initialized: initialized(), thread: undefined },
    });

    await harness.actions.selectRuntimeModel("codex", "gpt-5.1", "high");
    await harness.actions.selectRuntimeEffort("max");

    expect(api.updateRuntimeSettings).not.toHaveBeenCalled();
    expect(readDraftRuntimeMemory()).toEqual({
      provider: "codex",
      model: "gpt-5.1",
      effort: "max",
    });
  });

  it("starts the next conversation from the provider default saved in Settings", async () => {
    const api = installWuuApi();
    const harness = buildActions();

    await harness.actions.updateProviderSettings("codex", "gpt-5.1", undefined, {
      base_url: "https://new.example.test",
    });

    expect(api.updateRuntimeSettings).toHaveBeenCalled();
    expect(readDraftRuntimeMemory()).toEqual({
      provider: "codex",
      model: "gpt-5.1",
      effort: "high",
    });
  });

  it("toggles the Codex runtime menu and closes sibling menus", () => {
    const api = installWuuApi();
    const harness = buildActions();

    harness.actions.toggleCodexRuntimeMenu("model");

    expect(harness.getRuntimeMenus()).toEqual({
      runtimeMenuOpen: false,
      accessMenuOpen: false,
      branchMenuOpen: false,
      codexRuntimeMenu: "model",
    });
    expect(api.loadCodexModels).toHaveBeenCalledWith("codex");
  });

  it("keeps the model panel open while a model update is still queued", async () => {
    const api = installWuuApi();
    let resolveUpdate!: (value: {
      provider: string;
      model: string;
      effort: string;
      variant: string;
    }) => void;
    api.updateRuntimeSettings.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveUpdate = resolve;
      }),
    );
    const harness = buildActions();
    harness.actions.toggleCodexRuntimeMenu("model");

    const selection = harness.actions.selectRuntimeModel("codex", "gpt-5.1", "high");

    expect(api.updateRuntimeSettings).toHaveBeenCalledTimes(1);
    // The panel stays open so the user can chain "switch model → tune effort"
    // in one visit; the picker highlights the selection optimistically.
    expect(harness.getRuntimeMenus().codexRuntimeMenu).toBe("model");

    resolveUpdate({
      provider: "codex",
      model: "gpt-5.1",
      effort: "high",
      variant: "high",
    });
    await selection;
  });

  it("loads models for the active session instead of the workspace default", () => {
    const api = installWuuApi();
    const primary = thread();
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({
          provider: "anthropic",
          model: "claude-sonnet",
          providers: [
            { name: "anthropic", type: "anthropic", model: "claude-sonnet" },
            { name: "codex", type: "chatgpt-codex", model: "gpt-5" },
          ],
        }),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    harness.actions.toggleCodexRuntimeMenu("model");

    expect(api.loadCodexModels).toHaveBeenCalledWith("codex");
  });

  it("persists permission mode for the active thread and future threads", async () => {
    const api = installWuuApi();
    const harness = buildActions();

    await harness.actions.selectPermissionMode("read_only");

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      "read_only",
      "thread-1",
    );
    expect(harness.getAppState().thread?.permission_mode).toBe("read_only");
    expect(harness.getRuntimeMenus().accessMenuOpen).toBe(false);
    // The pick is remembered so the next new conversation seeds from it
    // instead of the workspace default.
    expect(readDraftPermissionMemory()).toBe("read_only");
  });

  it("keeps thread and workspace efforts intact on a permission-only click", async () => {
    const api = installWuuApi();
    // Permission-only updates leave the workspace selection untouched, so the
    // workspace-effective result echoes the current defaults.
    api.updateRuntimeSettings.mockResolvedValue({
      provider: "codex",
      model: "gpt-5",
      effort: "medium",
      variant: "medium",
      permissions: { mode: "read_only" },
    });
    const primary = {
      ...thread("thread-1"),
      model_provider: "anthropic",
      model: "claude-sonnet-4-6",
      model_variant: "low",
      model_effort: "low",
    };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized(),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.selectPermissionMode("read_only");

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      "read_only",
      "thread-1",
    );
    expect(harness.getAppState().thread?.model_provider).toBe("anthropic");
    expect(harness.getAppState().thread?.model).toBe("claude-sonnet-4-6");
    expect(harness.getAppState().thread?.model_variant).toBe("low");
    expect(harness.getAppState().thread?.model_effort).toBe("low");
    expect(harness.getAppState().thread?.permission_mode).toBe("read_only");
    expect(harness.getAppState().initialized?.variant).toBe("medium");
    expect(harness.getAppState().initialized?.effort).toBe("medium");
    expect(harness.getAppState().initialized?.permissions?.mode).toBe(
      "read_only",
    );
  });

  it("does not swallow a permission click that matches the workspace default on a diverged thread", async () => {
    const api = installWuuApi();
    const primary = { ...thread("thread-1"), permission_mode: "unconfined" };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ permissions: { mode: "standard" } }),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.selectPermissionMode("unconfined");
    expect(api.updateRuntimeSettings).not.toHaveBeenCalled();

    await harness.actions.selectPermissionMode("standard");
    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      "standard",
      "thread-1",
    );
  });

  it("surfaces permission update failures via status without rejecting", async () => {
    const api = installWuuApi();
    api.updateRuntimeSettings.mockRejectedValueOnce(
      new Error("cannot change the model while a turn is running"),
    );
    const harness = buildActions();

    await expect(
      harness.actions.selectPermissionMode("read_only"),
    ).resolves.toBeUndefined();

    expect(harness.getAppState().status).toBe(
      "cannot change the model while a turn is running",
    );
    expect(harness.getRuntimeMenus().accessMenuOpen).toBe(false);
    // A rejected change must not become the default for future conversations.
    expect(readDraftPermissionMemory()).toBeUndefined();
  });

  it("remembers a draft permission pick before the first thread starts", async () => {
    const api = installWuuApi();
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ permissions: { mode: "standard" } }),
        thread: undefined,
        threads: [],
        status: "ready",
      },
    });

    await harness.actions.selectPermissionMode("unconfined");

    expect(api.updateRuntimeSettings).not.toHaveBeenCalled();
    expect(harness.getAppState().initialized?.permissions?.mode).toBe("unconfined");
    expect(readDraftPermissionMemory()).toBe("unconfined");
  });

  it("interrupts active and pane-specific threads without clearing pending messages", async () => {
    const api = installWuuApi();
    const primary = thread("primary-thread");
    const secondary = thread("secondary-thread");
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized(),
        thread: primary,
        secondaryThread: secondary,
        activePane: "secondary",
      },
    });

    await harness.actions.interrupt();
    await harness.actions.interruptPane("primary");

    expect(api.interruptTurn).toHaveBeenNthCalledWith(1, "secondary-thread");
    expect(api.interruptTurn).toHaveBeenNthCalledWith(2, "primary-thread");
    expect(harness.clearThreadPendingComposerMessages).not.toHaveBeenCalled();
  });

  it("selects Approve for me as a permission choice and closes the menu", async () => {
    const api = installWuuApi();
    api.updateRuntimeSettings.mockResolvedValue({
      provider: "codex",
      model: "gpt-5",
      effort: "medium",
      variant: "medium",
      permissions: { mode: "standard", approve_for_me: true },
    });
    const primary = { ...thread("thread-1"), permission_mode: "read_only" };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ permissions: { mode: "read_only" } }),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.selectPermissionMode("standard", true);

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      undefined,
      undefined,
      undefined,
      { approve_for_me: true },
      undefined,
      "standard",
      "thread-1",
    );
    expect(harness.getAppState().thread?.permission_mode).toBe("standard");
    expect(harness.getAppState().thread?.approve_for_me).toBe(true);
    expect(harness.getRuntimeMenus().accessMenuOpen).toBe(false);
    expect(readDraftPermissionMemory()).toBe("standard");
    expect(readDraftApproveForMeMemory()).toBe(true);
  });

  it("persists Approve for me on the active thread and keeps the menu open", async () => {
    const api = installWuuApi();
    api.updateRuntimeSettings.mockResolvedValue({
      provider: "codex",
      model: "gpt-5",
      effort: "medium",
      variant: "medium",
      permissions: { mode: "standard", approve_for_me: true },
    });
    const primary = { ...thread("thread-1"), permission_mode: "standard" };
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ permissions: { mode: "standard" } }),
        thread: primary,
        threads: [primary],
        status: "ready",
      },
    });

    await harness.actions.setApproveForMe(true);

    expect(api.updateRuntimeSettings).toHaveBeenCalledWith(
      undefined,
      undefined,
      undefined,
      { approve_for_me: true },
      undefined,
      undefined,
      "thread-1",
    );
    expect(harness.getAppState().thread?.approve_for_me).toBe(true);
    expect(harness.getRuntimeMenus().accessMenuOpen).toBe(true);
    expect(readDraftApproveForMeMemory()).toBe(true);
  });

  it("remembers a draft Approve for me pick before the first thread starts", async () => {
    const api = installWuuApi();
    const harness = buildActions({
      initial: {
        ...initialState,
        initialized: initialized({ permissions: { mode: "standard" } }),
        thread: undefined,
        threads: [],
        status: "ready",
      },
    });

    await harness.actions.setApproveForMe(true);

    expect(api.updateRuntimeSettings).not.toHaveBeenCalled();
    expect(harness.getAppState().initialized?.permissions?.approve_for_me).toBe(true);
    expect(readDraftApproveForMeMemory()).toBe(true);
  });
});
