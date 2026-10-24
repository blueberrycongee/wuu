import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ViewHostAPI, PresentationHost } from "../../shared/workbench";
import { ApplicationController } from "./ApplicationController";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { LegacyUIContributions } from "./LegacyUIContributions";

let root: Root | undefined;
let element: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); root = undefined; element?.remove(); });
async function fixture(semanticSlot: string, context: Readonly<Record<string, unknown>> = {}) {
  const controller = new ApplicationController();
  const invoke = vi.fn(async () => undefined);
  const application = { getSnapshot: controller.getSnapshot, subscribe: () => () => {}, invoke } as unknown as ApplicationServiceV1;
  const host = new PluginHost({ react: React, application: application as never });
  const createViewHost = vi.fn((): ViewHostAPI => ({ getStorage: async () => null, setStorage: async () => {},
    getSetting: async () => undefined, executeCommand: async () => undefined, openView: async () => {}, closeView: async () => {} }));
  // Exercise the real host and outlet while assembly wiring is owned separately.
  await host.activateGeneration({ pluginId: "custom-shell", generation: "one", register(api) {
    api.registerUISlot({ id: semanticSlot, cardinality: "one" });
    api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name={`custom-shell/${semanticSlot}`} /> });
    api.registerUIContribution({ id: "legacy-assembly", target: `custom-shell/${semanticSlot}`, render: () =>
      <LegacyUIContributions host={host} semanticSlot={semanticSlot} application={application} context={context}
        createViewHost={createViewHost} fallback={<span>Native content</span>} /> });
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
    expect(invoke).toHaveBeenCalledWith("preview.focus-tab", { tabId: "original" });
    const old = captured;
    await register(api => { api.registerPresenter({ id: "replacement", target: "header.workspace", render: () => <span>New</span> }); });
    expect(element.textContent).toBe("New");
    await expect(old.invoke("header.select-tab", { tabId: "original" })).rejects.toThrow();
    await act(async () => { host.unload("extension"); });
    expect(element.textContent).toBe("Native content");
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
    const openFile = vi.fn(async () => {}); const revealItem = vi.fn(async () => {});
    Object.assign(application, { previewContent: { openFile, revealItem, capabilities: { revealItem: true } } });
    let captured!: PresentationHost;
    await register(api => { api.registerPresenter({ id: "markdown", target: "content.preview", key: "text/markdown", render: props => { captured = props.host; return <span>Markdown replacement</span>; } }); });
    mount(); expect(element.textContent).toBe("Markdown replacement");
    await captured.invoke("file-preview.open");
    expect(openFile).toHaveBeenCalledWith({ root: "/other-worktree", path: "docs/guide.md" });
    await expect(captured.invoke("file-preview.open", { path: "../secret" })).rejects.toThrow();
  });

  it("resolves message presenters and commands from explicit public source IDs", async () => {
    const source = { threadId: "secondary", turnId: "turn", itemId: "message" };
    const { application, register, mount, invoke } = await fixture("conversation.message", source);
    const baseline = application.getSnapshot();
    const snapshot = { ...baseline, conversationWorkspace: { ...baseline.conversationWorkspace, threads: [
      { id: "secondary", turns: [{ id: "turn", status: "completed", items: [{ id: "message", type: "user_message", status: "completed", text: "Selected source" }] }] },
    ] } };
    application.getSnapshot = () => snapshot as ReturnType<ApplicationServiceV1["getSnapshot"]>;
    let api!: PresentationHost;
    await register(plugin => { plugin.registerPresenter({ id: "item", target: "conversation.item", key: "user-message", render: props => {
      api = props.host; return <span>{(props.snapshot as { text: string }).text}</span>;
    } }); });
    mount(); expect(element.textContent).toBe("Selected source");
    await api.invoke("conversation.item.edit");
    expect(invoke).toHaveBeenCalledWith("history.begin-edit", source);
  });

  it("opens declared workspace tools through the public generation-specific action", async () => {
    const { register, mount, invoke } = await fixture("workspace.tools");
    await register(api => { api.registerViewType({ id: "notes", title: "Notes", render: () => <span>Notes view</span> }); },
      { workspace_tools: [{ id: "notes-entry", view: "notes", title: "Notes" }] });
    mount(); await act(async () => { element.querySelector<HTMLButtonElement>("button")!.click(); });
    expect(invoke).toHaveBeenCalledWith("preview.open-plugin", expect.objectContaining({ entryId: "notes-entry", pluginId: "extension", generation: "same", viewTypeId: "notes" }));
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
