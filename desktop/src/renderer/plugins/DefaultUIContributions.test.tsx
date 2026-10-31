import * as Toast from "../Toast";
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ViewHostAPI, PresentationHost, InspectorSnapshotV1 } from "../../shared/workbench";
import { ApplicationController } from "./ApplicationController.test-support";
import { ApplicationPreviewContent } from "./ApplicationPreviewContent";
import { registerConversationContributions, registerPreviewContributions, registerShellContributions } from "./DefaultUIContributions";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";

let root: Root | undefined;
let element: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); root = undefined; element?.remove(); vi.restoreAllMocks(); });
async function fixture(semanticSlot: string, context: Readonly<Record<string, unknown>> = {}, cardinality: "one" | "many" = "one") {
  const invoke = vi.fn(async () => undefined);
  const application = Object.assign(new ApplicationController(), {
    subscribe: () => () => {}, invoke: invoke as ApplicationServiceV1["invoke"],
  });
  const createViewHost = vi.fn((): ViewHostAPI => ({ getStorage: async () => null, setStorage: async () => {},
    getSetting: async () => undefined, executeCommand: async () => undefined, openView: vi.fn(async () => {}), closeView: async () => {} }));
  const workbench = Object.create(application.workbench);
  workbench.createViewHostAPI = createViewHost;
  Object.defineProperty(application, "workbench", { value: workbench, configurable: true });
  const host = new PluginHost({ react: React, application });
  // The surface explicitly owns its behavior; declaring a slot alone is inert.
  await host.activateGeneration({ pluginId: "custom-shell", generation: "one", register(api) {
    api.registerUISlot({ id: semanticSlot, cardinality });
    if (semanticSlot.startsWith("conversation.")) registerConversationContributions(api);
    else if (["workspace.tools", "workspace.view", "content.preview", "header.workspace"].includes(semanticSlot)) registerPreviewContributions(api);
    api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name={`custom-shell/${semanticSlot}`} context={context} fallback={<span>Native content</span>} /> });
  } });
  const mount = () => { element = document.createElement("div"); document.body.append(element); root = createRoot(element);
    act(() => root!.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "custom-shell", generation: "one", id: "shell" }} />)); };
  const register = (register: (api: PluginGenerationApi) => void, contributions?: Parameters<PluginHost["activateGeneration"]>[0]["contributions"]) =>
    act(async () => { await host.activateGeneration({ pluginId: "extension", generation: "same", register, contributions }); });
  return { host, application, invoke, mount, register, createViewHost };
}

// Failure cases: disconnected registrations, same-code reload retaining state,
// wrong document/workspace source, stale actions and stale plugin view selection.
describe("provider-owned contributions in declared public slots", () => {
  it.each(["conversation.message.before", "workspace.header"] as const)("renders translated %s slots and releases effects before generation cleanup on unload", async slot => {
    const { host, register, mount } = await fixture(slot, { threadId: "thread", turnId: "turn", itemId: "item" });
    const events: string[] = [];
    function Content() { React.useEffect(() => () => { events.push("effect"); }, []); return <span>Old extension</span>; }
    await register(api => {
      api.registerLocale({ id: "language", locale: "en", entries: { hello: "Translated" } });
      api.registerCleanup(() => events.push("generation"));
      api.registerUIContribution({ target: `custom-shell/${slot}`, id: "before", render: ({ context, translate }) => <><Content />{translate("hello")}:{String(context.itemId)}</> });
    });
    mount(); expect(element.textContent).toContain("Old extensionTranslated:item");
    await act(async () => { host.unload("extension"); });
    expect(element.textContent).toBe("Native content");
    expect(events).toEqual(["effect", "generation"]);
  });

  it("preserves presenter chains and fences captured actions after same-code replacement", async () => {
    const { host, register, mount, invoke } = await fixture("header.workspace", { snapshot: { contractVersion: 1, scope: "workspace", tabs: [{ id: "original", title: "Original" }] } });
    let captured!: PresentationHost;
    await register(api => {
      api.registerPresenter({ id: "replacement", target: "header.workspace", render: props => { captured = props.host; return <span>Replacement</span>; } });
      api.registerPresenter({ id: "wrapper", target: "header.workspace", mode: "wrap", render: props => <section>Wrapper:{props.next()}</section> });
    });
    mount(); expect(element.textContent).toBe("Wrapper:Replacement");
    await captured.invoke("header.select-tab", { tabId: "original" });
    expect(invoke).toHaveBeenCalledWith("preview.focus-tab", { tabId: "original" }, { signal: expect.any(AbortSignal) });
    const old = captured;
    await register(api => { api.registerPresenter({ id: "replacement", target: "header.workspace", render: () => <span>New</span> }); });
    expect(element.textContent).toBe("New");
    await expect(old.invoke("header.select-tab", { tabId: "original" })).rejects.toThrow();
    await act(async () => { host.unload("extension"); });
    expect(element.textContent).toBe("Native content");
  });

  it("selects and closes the conversation header tab through public actions", async () => {
    const { application, register, mount, invoke } = await fixture("header.workspace", { snapshot: {
      contractVersion: 1, scope: "workspace", tabs: [{ id: "thread:one", title: "Conversation", kind: "conversation", closable: true }],
    } });
    const snapshot = application.getSnapshot();
    application.getSnapshot = () => ({ ...snapshot, tabs: { ...snapshot.tabs, activeTabId: "thread:one" } });
    let api!: PresentationHost;
    await register(plugin => { plugin.registerPresenter({ id: "header", target: "header.workspace", render: props => { api = props.host; return props.fallback; } }); });
    mount(); await api.invoke("header.select-tab", { tabId: "thread:one" });
    expect(invoke).toHaveBeenCalledExactlyOnceWith("preview.show-conversation", {}, { signal: expect.any(AbortSignal) });
    await api.invoke("header.close-tab", { tabId: "thread:one" });
    expect(invoke).toHaveBeenLastCalledWith("preview.close-conversation", { tabId: "thread:one" }, { signal: expect.any(AbortSignal) });
    application.getSnapshot = () => ({ ...snapshot, tabs: { ...snapshot.tabs, activeTabId: "thread:two" } });
    await expect(api.invoke("header.select-tab", { tabId: "thread:one" })).rejects.toThrow("no longer active");
    expect(invoke).toHaveBeenCalledTimes(2);
  });

  it("does not send an unsupported conversation close to a legacy application provider", async () => {
    const { register, mount, invoke } = await fixture("header.workspace", { snapshot: {
      contractVersion: 1, scope: "workspace", tabs: [{ id: "thread:one", title: "Conversation", kind: "conversation", closable: false }],
    } });
    let api!: PresentationHost;
    await register(plugin => { plugin.registerPresenter({ id: "header", target: "header.workspace", render: props => { api = props.host; return props.fallback; } }); });
    mount();
    await api.invoke("header.close-tab", { tabId: "thread:one" });
    expect(invoke).not.toHaveBeenCalled();
  });

  it("routes a legacy header back action only while its captured view remains active", async () => {
    const { application, register, mount, invoke } = await fixture("header.workspace", { viewId: "source-view",
      snapshot: { contractVersion: 1, scope: "workspace", canNavigateBack: true } });
    const baseline = application.getSnapshot();
    let current: ApplicationSnapshotV1 = { ...baseline, navigationWorkspace: { ...baseline.navigationWorkspace,
      activePluginView: { id: "source-view", pluginId: "extension", title: "Source", webPage: false, closable: true } } };
    application.getSnapshot = () => current;
    let captured!: PresentationHost;
    await register(api => { api.registerPresenter({ id: "header", target: "header.workspace", render: props => {
      captured = props.host;
      return <button onClick={() => { void props.host.invoke("header.navigate-back"); }}>Go back</button>;
    } }); });
    mount();
    expect(captured.actions).toContain("header.navigate-back");
    await act(async () => { element.querySelector<HTMLButtonElement>("button")!.click(); });
    expect(invoke).toHaveBeenCalledExactlyOnceWith("navigation.show-native", {}, { signal: expect.any(AbortSignal) });
    current = { ...current, navigationWorkspace: { ...current.navigationWorkspace,
      activePluginView: { id: "successor-view", pluginId: "extension", title: "Successor", webPage: false, closable: true } } };
    await expect(captured.invoke("header.navigate-back")).rejects.toThrow("view");
    expect(invoke).toHaveBeenCalledOnce();
  });

  it.each([{ canNavigateBack: false, viewId: "source" }, { canNavigateBack: true, viewId: undefined }])(
    "does not advertise an unavailable header back action (%j)", async ({ canNavigateBack, viewId }) => {
      const { register, mount } = await fixture("header.workspace", { viewId,
        snapshot: { contractVersion: 1, scope: "workspace", canNavigateBack } });
      let captured!: PresentationHost;
      await register(api => { api.registerPresenter({ id: "header", target: "header.workspace", render: props => { captured = props.host; return props.fallback; } }); });
      mount(); expect(captured.actions).not.toContain("header.navigate-back");
    });

  it("restores the supplied fallback after a presenter fails and recovers on reactivation", async () => {
    const { host, register, mount } = await fixture("header.workspace", { snapshot: { contractVersion: 1, scope: "workspace", tabs: [] } });
    const silence = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      await register(api => { api.registerPresenter({ id: "broken", target: "header.workspace", render: () => { throw new Error("broken view"); } }); });
      mount(); expect(element.textContent).toBe("Native content");
      expect(host.getGenerationDiagnostics("extension", "same")).toEqual(expect.arrayContaining([expect.objectContaining({ contributionId: "broken" })]));
      await register(api => { api.registerPresenter({ id: "broken", target: "header.workspace", render: () => <span>Recovered</span> }); });
      expect(element.textContent).toBe("Recovered");
    } finally { silence.mockRestore(); }
  });

  it("shows only source-thread cards and removes dynamically dismissed cards", async () => {
    const { host, register, mount } = await fixture("conversation.cards", { threadId: "selected" });
    let api!: PluginGenerationApi;
    await register(value => { api = value; }); mount();
    await act(async () => {
      api.showConversationCard({ threadId: "other", title: "Other", render: () => <span>Wrong thread</span> });
      api.showConversationCard({ threadId: "selected", title: "Selected", render: props => <button onClick={props.dismiss}>Dismiss selected</button> });
    });
    expect(element.textContent).toContain("Dismiss selected"); expect(element.textContent).not.toContain("Wrong thread");
    await act(async () => { Array.from(element.querySelectorAll("button")).find(button => button.textContent === "Dismiss selected")!.click(); });
    expect(element.textContent).toBe("Native content");
    await act(async () => { host.unload("extension"); });
  });

  it("uses the explicit file resource root and restricts preview actions to that file", async () => {
    const { application, register, mount } = await fixture("content.preview", {
      resource: { root: "/other-worktree", path: "docs/guide.md" },
      snapshot: { contentType: "text/markdown", workspaceRelativePath: "docs/guide.md" },
    });
    const preview = new ApplicationPreviewContent({ platform: "linux",
      readWorkspaceFile: async () => { throw new Error("unused"); },
      listWorkspaceDirectory: async () => { throw new Error("unused"); },
      revealWorkspaceItem: async () => {}, showWorkspaceItemMenu: async () => ({ action: "none" }),
    }, { openFile: async () => {} });
    vi.spyOn(application, "previewContent", "get").mockReturnValue(preview);
    const openFile = vi.spyOn(preview, "openFile");
    let captured!: PresentationHost;
    await register(api => { api.registerPresenter({ id: "markdown", target: "content.preview", key: "text/markdown", render: props => { captured = props.host; return <span>Markdown replacement</span>; } }); });
    mount(); expect(element.textContent).toBe("Markdown replacement");
    await captured.invoke("file-preview.open");
    expect(openFile).toHaveBeenCalledWith({ root: "/other-worktree", path: "docs/guide.md" }, { signal: expect.any(AbortSignal) });
    await expect(captured.invoke("file-preview.open", { path: "../secret" })).rejects.toThrow();
  });

  it.each(["conversation.message", "conversation.message.actions"])("uses the leaf payload for %s without looking up another conversation", async semanticSlot => {
    const source = { threadId: "secondary", turnId: "turn", itemId: "message" };
    const item = Object.freeze({ contractVersion: 1, id: "message", kind: "user-message", status: "completed", text: "Selected source" });
    const { host, register, mount, invoke } = await fixture(semanticSlot, { ...source, snapshot: item });
    let captured!: ApplicationServiceV1;
    const command = vi.fn();
    await register(plugin => {
      plugin.registerCommand({ id: "inspect", title: "Inspect source", placements: ["conversation.message.actions"], execute: command });
      plugin.registerUIContribution({ id: "item", target: "custom-shell/conversation.message", render: ({ application, context }) => {
        captured = application; return <span>{(context.snapshot as { text: string }).text}</span>;
      } });
    });
    mount();
    if (semanticSlot === "conversation.message") {
      expect(element.textContent).toBe("Selected source");
      await captured.invoke("history.begin-edit", source);
      expect(invoke).toHaveBeenCalledWith("history.begin-edit", source, { signal: expect.any(AbortSignal) });
      const old = captured;
      await act(async () => { host.unload("extension"); });
      await expect(old.invoke("history.begin-edit", source)).rejects.toThrow();
      expect(element.textContent).toBe("Native content");
    } else {
      await act(async () => { element.querySelector<HTMLButtonElement>('button[aria-label="Inspect source"]')!.click(); });
      expect(command).toHaveBeenCalledWith(expect.objectContaining({ threadId: "secondary", turnId: "turn", item }));
    }
  });

  it("opens declared workspace tools through the public generation-specific action", async () => {
    const { register, mount, invoke } = await fixture("workspace.tools");
    await register(api => { api.registerViewType({ id: "notes", title: "Notes", render: () => <span>Notes view</span> }); },
      { workspace_tools: [{ id: "notes-entry", view: "notes", title: "Notes" }] });
    mount(); await act(async () => { element.querySelector<HTMLButtonElement>("button")!.click(); });
    expect(invoke).toHaveBeenCalledWith("preview.open-plugin", expect.objectContaining({ entryId: "notes-entry", pluginId: "extension", generation: "same", viewTypeId: "notes" }), { signal: expect.any(AbortSignal) });
  });

  it("unmounts host workbench layers and fences their captured application with their owner", async () => {
    const { host, register, mount, invoke } = await fixture("test-layer", { visible: true });
    const cleanup = vi.fn();
    let captured!: ApplicationServiceV1;
    function Layer() { React.useEffect(() => cleanup, []); return <span>Host workbench</span>; }
    await register(api => {
      api.registerUIContribution({ id: "layer-owner", target: "custom-shell/test-layer", render: ({ application, context }) => {
        captured = application;
        expect(context).toEqual({ visible: true });
        return <Layer />;
      } });
    });
    mount(); expect(element.textContent).toBe("Host workbench");
    await captured.invoke("preview.show-conversation", {});
    expect(invoke).toHaveBeenCalledOnce();
    await act(async () => { host.unload("extension"); });
    expect(cleanup).toHaveBeenCalledOnce();
    expect(element.textContent).toBe("Native content");
    await expect(captured.invoke("preview.show-conversation", {})).rejects.toThrow();
    expect(invoke).toHaveBeenCalledOnce();
  });

  it("does not create workbench behavior from a slot name and stops explicit provider layers on unload", async () => {
    const { host, application, mount } = await fixture("workbench.layers", { settingsOnly: true });
    const cleanup = vi.fn();
    function Layer() { React.useEffect(() => cleanup, []); return <span>Selected workbench layer</span>; }
    const renderLayers = vi.fn(() => <Layer />);
    Object.defineProperty(application, "workbench", { value: { ...application.workbench, renderLayers }, configurable: true });
    mount(); expect(element.textContent).toBe("Native content"); expect(renderLayers).not.toHaveBeenCalled();
    await act(async () => { await host.activateGeneration({ pluginId: "custom-shell", generation: "two", register: api => {
      api.registerUISlot({ id: "workbench.layers", cardinality: "many" });
      registerShellContributions(api);
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name="custom-shell/workbench.layers" context={{ settingsOnly: true }} /> });
    } }); root!.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "custom-shell", generation: "two", id: "shell" }} />); });
    expect(element.textContent).toBe("Selected workbench layer"); expect(renderLayers).toHaveBeenCalledWith({ settingsOnly: true });
    await act(async () => { host.unload("custom-shell"); });
    expect(cleanup).toHaveBeenCalledOnce(); expect(element.textContent).not.toContain("Selected workbench layer");
  });

  it("preserves normal presenter openView defaults, options and generation fences", async () => {
    const { register, mount, invoke, createViewHost } = await fixture("header.workspace", { snapshot: { contractVersion: 1, scope: "workspace" } });
    let captured!: PresentationHost;
    await register(api => {
      api.registerViewType({ id: "details", title: "Details", defaultRegion: "primary", render: () => null });
      api.registerPresenter({ id: "header", target: "header.workspace", render: props => { captured = props.host; return props.fallback; } });
    });
    mount(); const base = createViewHost.mock.results[0].value;
    await captured.openView("details");
    expect(base.openView).toHaveBeenCalledWith("details", undefined);
    const options = { region: "overlay" as const, context: { source: "header" }, persistence: "session" as const, reveal: false };
    await captured.openView("details", options);
    expect(base.openView).toHaveBeenLastCalledWith("details", options);
    expect(invoke).not.toHaveBeenCalled();
    const old = captured; await register(() => {});
    await expect(old.openView("details")).rejects.toThrow();
    expect(base.openView).toHaveBeenCalledTimes(2);
  });

  it("translates only preview-owned auxiliary targets without discarding view options", async () => {
    const { register, mount, invoke, createViewHost } = await fixture("workspace.view", {
      pluginId: "extension", generation: "same", viewTypeId: "source", tabId: "source-tab", region: "workspace",
    });
    let captured!: ViewHostAPI;
    await register(api => {
      api.registerViewType({ id: "source", title: "Source", render: props => { captured = props.host; return <span>Source</span>; } });
      api.registerViewType({ id: "details", title: "Details", defaultRegion: "auxiliary", render: () => null });
      api.registerViewType({ id: "primary", title: "Primary", defaultRegion: "primary", render: () => null });
    }, { workspace_tools: [{ id: "details-entry", view: "details", title: "Details" }] });
    mount(); const base = createViewHost.mock.results[0].value;
    await captured.openView("primary");
    expect(base.openView).toHaveBeenCalledWith("primary", undefined);
    await captured.openView("details");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("preview.open-plugin", expect.objectContaining({ viewTypeId: "details" }), { signal: expect.any(AbortSignal) });
    const options = { region: "auxiliary" as const, context: { document: "notes" }, persistence: "durable" as const, reveal: false };
    await captured.openView("details", options);
    expect(base.openView).toHaveBeenLastCalledWith("details", options);
    expect(invoke).toHaveBeenCalledOnce();
  });

  it("renders ordered inspector contributions with conditions, isolation and captured application ownership", async () => {
    const snapshot = Object.freeze<InspectorSnapshotV1>({ contractVersion: 1, session: { id: "source", status: "idle" } });
    const { host, register, mount } = await fixture("inspector.sections", { snapshot }, "many");
    let captured!: ApplicationServiceV1;
    const cleanup = vi.fn();
    function Content({ application }: { application: ApplicationServiceV1 }) {
      captured = application; React.useEffect(() => cleanup, []); return <span>Inspector content</span>;
    }
    const silence = vi.spyOn(console, "error").mockImplementation(() => {});
    const notice = vi.spyOn(Toast, "showErrorToast").mockReturnValue(undefined);
    try {
      await register(api => {
        const target = "custom-shell/inspector.sections";
        api.registerUIContribution({ id: "late", target, order: 20, render: () => <section><h2>Later</h2>Later content</section> });
        api.registerUIContribution({ id: "early", target, order: 1, render: ({ context, application }) =>
          (context.snapshot as InspectorSnapshotV1).session.id === "source" ? <section><h2>Earlier</h2><Content application={application} /></section> : null });
        api.registerUIContribution({ id: "hidden", target, order: 0, render: () => null });
        api.registerUIContribution({ id: "broken", target, order: 10, render: () => { throw new Error("inspector failed"); } });
      });
      mount();
      expect(Array.from(element.querySelectorAll("h2"), node => node.textContent)).toEqual(["Earlier", "Later"]);
      expect(element.textContent).toContain("Inspector content"); expect(element.textContent).not.toContain("Hidden content");
      expect(element.querySelector('[role="alert"]')).toBeNull();
      expect(element.textContent).not.toContain("inspector failed");
      expect(element.querySelector('[role="status"]')).not.toBeNull();
      expect(notice).not.toHaveBeenCalled();
      expect(host.getGenerationDiagnostics("extension", "same")).toEqual(expect.arrayContaining([expect.objectContaining({ kind: "render", message: expect.stringContaining("inspector failed") })]));
      await captured.invoke("settings.open", {});
      const old = captured;
      await register(api => { api.registerUIContribution({ id: "early", target: "custom-shell/inspector.sections", render: ({ application }) => <section><h2>Replaced</h2><Content application={application} /></section> }); });
      expect(element.textContent).toContain("Replaced"); expect(cleanup).toHaveBeenCalledOnce();
      await expect(old.invoke("settings.open", {})).rejects.toThrow();
      await act(async () => { host.unload("extension"); });
      expect(element.textContent).toBe("Native content"); expect(cleanup).toHaveBeenCalledTimes(2);
    } finally { silence.mockRestore(); }
  });

  it("keeps a failed active view visible and retries without substituting native content", async () => {
    const { host, register, mount } = await fixture("workspace.view", { pluginId: "extension", generation: "same", viewTypeId: "notes", tabId: "view-tab" });
    let broken = true;
    vi.spyOn(console, "error").mockImplementation(() => {});
    await register(api => api.registerViewType({ id: "notes", title: "Notes", render: () => {
      if (broken) throw new Error("private view details");
      return <span>Recovered notes</span>;
    } }));
    mount();
    expect(element.querySelector('[role="status"]')).not.toBeNull();
    expect(element.textContent).not.toContain("Native content");
    expect(element.textContent).not.toContain("private view details");
    broken = false;
    await act(async () => element.querySelector('button')!.click());
    expect(element.textContent).toBe("Recovered notes");
    expect(host.getGenerationDiagnostics("extension", "same").some(item => item.kind === "render")).toBe(true);
  });

  it("renders only the selected generation's workspace view and its title commands", async () => {
    const { host, register, mount } = await fixture("workspace.view", { pluginId: "extension", generation: "same", viewTypeId: "notes", tabId: "view-tab", region: "workspace" });
    const command = vi.fn();
    await register(api => {
      api.registerViewType({ id: "notes", title: "Notes", render: () => <span>Notes view</span> });
      api.registerCommand({ id: "refresh", title: "Refresh notes", placements: ["view.title"], execute: command });
    });
    mount(); expect(element.textContent).toContain("Notes view");
    await act(async () => { element.querySelector<HTMLButtonElement>('button[aria-label="Refresh notes"]')!.click(); });
    expect(command).toHaveBeenCalledWith(expect.objectContaining({ viewId: "view-tab", viewTypeId: "notes", viewPluginId: "extension" }));
    await act(async () => { await host.activateGeneration({ pluginId: "extension", generation: "new", register: api => { api.registerViewType({ id: "notes", title: "New", render: () => <span>Wrong successor</span> }); } }); });
    expect(element.textContent).not.toContain("Wrong successor");
  });
});
