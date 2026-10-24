import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ViewHostAPI, PresentationHost } from "../../shared/workbench";
import { ApplicationController } from "./ApplicationController";
import { ApplicationPreviewContent } from "./ApplicationPreviewContent";
import { LegacyUIContributions } from "./LegacyUIContributions";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";

let root: Root | undefined;
let element: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); root = undefined; element?.remove(); vi.restoreAllMocks(); });
async function fixture(semanticSlot: string, context: Readonly<Record<string, unknown>> = {}) {
  const invoke = vi.fn(async () => undefined);
  const application = Object.assign(new ApplicationController(), {
    subscribe: () => () => {}, invoke: invoke as ApplicationServiceV1["invoke"],
  });
  const createViewHost = vi.fn((): ViewHostAPI => ({ getStorage: async () => null, setStorage: async () => {},
    getSetting: async () => undefined, executeCommand: async () => undefined, openView: async () => {}, closeView: async () => {} }));
  const host = new PluginHost({ react: React, application, createLegacyViewHost: createViewHost });
  // Only the semantic slot is declared; no adapter contribution is registered.
  await host.activateGeneration({ pluginId: "custom-shell", generation: "one", register(api) {
    api.registerUISlot({ id: semanticSlot, cardinality: "one" });
    api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name={`custom-shell/${semanticSlot}`} context={context} fallback={<span>Native content</span>} /> });
  } });
  const mount = () => { element = document.createElement("div"); document.body.append(element); root = createRoot(element);
    act(() => root!.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "custom-shell", generation: "one", id: "shell" }} />)); };
  const register = (register: (api: PluginGenerationApi) => void, contributions?: Parameters<PluginHost["activateGeneration"]>[0]["contributions"]) =>
    act(async () => { await host.activateGeneration({ pluginId: "extension", generation: "same", register, contributions }); });
  return { host, application, invoke, mount, register };
}

// Failure cases: disconnected registrations, same-code reload retaining state,
// wrong document/workspace source, stale actions and stale plugin view selection.
describe("legacy contributions in declared public slots", () => {
  it.each(["conversation.message.before", "workspace.header"] as const)("renders translated %s slots and releases effects before generation cleanup on unload", async slot => {
    const { host, register, mount } = await fixture(slot, { threadId: "thread", turnId: "turn", itemId: "item" });
    const events: string[] = [];
    function Content() { React.useEffect(() => () => { events.push("effect"); }, []); return <span>Old extension</span>; }
    await register(api => {
      api.registerLocale({ id: "language", locale: "en", entries: { hello: "Translated" } });
      api.registerCleanup(() => events.push("generation"));
      api.registerSlot(slot, { id: "before", render: ctx => <><Content />{(ctx.translate as (key: string) => string)("hello")}:{String(ctx.itemId)}</> });
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

  it("selects the conversation header tab with its public conversation action", async () => {
    const { register, mount, invoke } = await fixture("header.workspace", { snapshot: {
      contractVersion: 1, scope: "workspace", tabs: [{ id: "conversation", title: "Conversation", kind: "conversation" }],
    } });
    let api!: PresentationHost;
    await register(plugin => { plugin.registerPresenter({ id: "header", target: "header.workspace", render: props => { api = props.host; return props.fallback; } }); });
    mount(); await api.invoke("header.select-tab", { tabId: "conversation" });
    expect(invoke).toHaveBeenCalledExactlyOnceWith("preview.show-conversation", {}, { signal: expect.any(AbortSignal) });
    await api.invoke("header.close-tab", { tabId: "conversation" });
    expect(invoke).toHaveBeenCalledOnce();
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

  it.each(["conversation.message", "conversation.message.actions"])("resolves %s from explicit public source IDs", async semanticSlot => {
    const source = { threadId: "secondary", turnId: "turn", itemId: "message" };
    const { application, register, mount, invoke } = await fixture(semanticSlot, source);
    const baseline = application.getSnapshot();
    const snapshot: ApplicationSnapshotV1 = { ...baseline, conversationWorkspace: { ...baseline.conversationWorkspace, threads: [
      { id: "secondary", preview: "", model_provider: "test", model: "test", cwd: "/repo", status: "idle", created_at: "", updated_at: "", turns: [{ id: "turn", status: "completed", items_view: "full", items: [{ id: "message", type: "user_message", status: "completed", text: "Selected source" }] }] },
    ] } };
    application.getSnapshot = () => snapshot;
    let api!: PresentationHost;
    const command = vi.fn();
    await register(plugin => { plugin.registerCommand({ id: "inspect", title: "Inspect source", placements: ["conversation.message.actions"], execute: command }); plugin.registerPresenter({ id: "item", target: "conversation.item", key: "user-message", render: props => {
      api = props.host; return <span>{(props.snapshot as { text: string }).text}</span>;
    } }); });
    mount();
    if (semanticSlot === "conversation.message") {
      expect(element.textContent).toBe("Selected source");
      await api.invoke("conversation.item.edit");
      expect(invoke).toHaveBeenCalledWith("history.begin-edit", source, { signal: expect.any(AbortSignal) });
    } else {
      await act(async () => { element.querySelector<HTMLButtonElement>('button[aria-label="Inspect source"]')!.click(); });
      expect(command).toHaveBeenCalledWith(expect.objectContaining({ threadId: "secondary", turnId: "turn", item: expect.objectContaining({ id: "message", text: "Selected source" }) }));
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
      api.registerUIContribution({ id: "layer-owner", target: "custom-shell/test-layer", render: ({ application, context }) =>
        <LegacyUIContributions host={host} semanticSlot="workbench.layers" application={application} context={context}
          createViewHost={() => { throw new Error("No view requested"); }} renderLegacyWorkbench={input => {
            captured = input.application;
            expect(input.context).toEqual({ visible: true });
            return <Layer />;
          }} /> });
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
