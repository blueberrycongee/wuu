import * as React from "react";
import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExtensionInventoryRecord, Thread } from "../shared/protocol";
import type { HeaderSnapshotV1, PresentationHost } from "../shared/workbench";
import { AppSidebar } from "./AppSidebar";
import { createThreadSessionTab, initialState } from "./AppState";
import { ConversationTitleContent } from "./ConversationShellRenderers";
import { translateCurrent as t } from "./i18n";
import { PluginHost } from "./plugins/PluginHost";
import { DesktopWorkbench, WorkbenchController } from "./plugins/Workbench";

const context = { kind: "project" as const, project_id: "project", cwd: "/repo" };
const thread: Thread = {
  id: "conversation", title: "Original conversation", preview: "", cwd: "/repo",
  model_provider: "test", model: "test", status: "idle", turns: [],
  created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z",
};
const draft = { ...createThreadSessionTab(thread, context), prompt: "Unsent draft" };
const state = {
  ...initialState, activeContext: context, thread, threads: [thread],
  sessionTabs: [draft], activeSessionTabID: draft.id,
};
const pluginId = "test:primary-views";
const inventory: ExtensionInventoryRecord[] = [{
  id: pluginId, name: "Views", kind: "plugin", enabled: true,
  state: "granted", approval_state: "granted",
  provenance: { kind: "plugin", source: "user", scope: "user" },
}];
let container: HTMLDivElement;
let root: Root;
let host: PluginHost;
let controller: WorkbenchController;
const selectThread = vi.fn();

beforeEach(() => {
  window.localStorage.clear();
  selectThread.mockClear();
  host = new PluginHost({ react: React });
  controller = new WorkbenchController(host);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  controller.dispose();
  container.remove();
  window.localStorage.clear();
});

async function register(generation = "one", title = "Automation") {
  await act(async () => host.activateGeneration({
    pluginId, generation,
    contributions: { navigation: [{ id: "catalog-entry", title, view: "catalog" }] },
    register(api) {
      api.registerViewType({ id: "catalog", title, persistence: "durable", render: () => <article>Catalog content</article> });
      api.registerViewType({ id: "detail", title: "Detail", persistence: "durable", render: () => <article>Detail content</article> });
    },
  }));
}

function renderShell({ inventory: available }: { inventory?: ExtensionInventoryRecord[] } = { inventory }) {
  act(() => root.render(<>
    <AppSidebar
      state={state}
      sidebarProjects={[]}
      pinnedThreads={[{ ...thread, turn_count: 0 }]}
      activeThreadID={thread.id}
      collapsedSidebarSectionIDs={new Set()}
      expandedSidebarSectionIDs={new Set()}
      projectThreadsByProjectID={{}}
      projectMenuOpen={false}
      projectMenuRef={createRef()}
      searchOpen={false}
      sectionOrder={[]}
      onStartNewThread={() => {}}
      onOpenSkillsTab={() => {}}
      onToggleConversationSearch={() => {}}
      onSelectThread={selectThread}
      onTogglePinned={() => {}}
      onArchiveThread={() => {}}
      onDeleteThread={() => {}}
      onRenameThread={() => {}}
      onToggleProjectMenu={() => {}}
      onCreateProject={() => {}}
      onOpenProjectFolder={() => {}}
      onToggleSidebarSectionCollapsed={() => {}}
      onStartNewThreadForProject={() => {}}
      onSelectProjectThread={() => {}}
      onRemoveProject={() => {}}
      onRelocateProject={() => {}}
      onOpenSettings={() => {}}
      onMarkThreadsViewed={() => {}}
      pluginHost={host}
      workbenchController={controller}
    />
    <main className="conversation-pane">
      <header>
        <ConversationTitleContent state={state} activeTitle="Original conversation"
          onStartNewThread={() => {}} pluginHost={host} workbenchController={controller} />
      </header>
    </main>
    <DesktopWorkbench host={host} controller={controller} inventory={available} />
  </>));
}

function button(label: string): HTMLButtonElement {
  const result = [...container.querySelectorAll<HTMLButtonElement>("button")]
    .find((item) => item.getAttribute("aria-label") === label || item.textContent === label);
  expect(result, `button ${label}`).toBeDefined();
  return result!;
}

async function click(label: string) {
  await act(async () => button(label).click());
}

function expectPage(title: string, content?: string) {
  expect(container.querySelector("main header h1")?.textContent).toBe(title);
  expect(container.querySelector("main header [role=tablist]")).toBeNull();
  expect(container.querySelector("main header [role=tab]")).toBeNull();
  const view = container.querySelector(".plugin-workbench-view-primary");
  if (content) expect(view?.textContent).toContain(content);
  else expect(view).toBeNull();
}

describe("primary plugin navigation without a tab strip", () => {
  it("opens, returns, switches API-opened instances, reloads and closes through visible controls", async () => {
    await register();
    renderShell();
    expectPage("Original conversation");
    await click("Automation");
    expectPage("Automation", "Catalog content");
    expect(button("Automation").getAttribute("aria-current")).toBe("page");
    const first = controller.getSnapshot().activeViewByRegion.primary;

    await click("Original conversation");
    expect(selectThread).toHaveBeenCalledWith(thread.id);
    expectPage("Original conversation");
    expect(button("Automation").hasAttribute("aria-current")).toBe(false);
    await click("Automation");
    expect(controller.getSnapshot().activeViewByRegion.primary).toBe(first);
    expectPage("Automation", "Catalog content");

    await act(async () => { await controller.openPluginView(pluginId, "detail"); });
    expectPage("Detail", "Detail content");
    await act(async () => { await controller.openPluginView(pluginId, "detail", { reveal: false }); });
    await click("Detail · 1");
    expect(button("Detail · 1").getAttribute("aria-current")).toBe("page");
    expect(button("Detail · 2").hasAttribute("aria-current")).toBe(false);
    await click(t("workspace.closeTab", { label: "Detail" }));
    expectPage("Detail", "Detail content");
    await click(t("workspace.closeTab", { label: "Detail" }));
    expectPage("Automation", "Catalog content");
    await register("two", "Updated catalog");
    expectPage("Updated catalog", "Catalog content");
    await click(t("common.back"));
    expectPage("Original conversation");
    await click("Updated catalog");
    await click(t("workspace.closeTab", { label: "Updated catalog" }));
    expectPage("Original conversation");
    expect(document.activeElement).toBe(container.querySelector("main header h1"));
    expect(controller.getSnapshot().views).toEqual([]);
    expect(state.sessionTabs[0].prompt).toBe("Unsent draft");

    await click("Updated catalog");
    act(() => host.unload(pluginId));
    expectPage("Original conversation");
    expect(container.querySelector('[data-wuu-component="plugin-navigation"]')).toBeNull();
    await register("three");
    expectPage("Original conversation");
  });

  it("restores old durable views after async registration and remembers returning to the conversation", async () => {
    await register();
    await controller.openPluginView(pluginId, "detail");
    controller.dispose();
    host = new PluginHost({ react: React });
    controller = new WorkbenchController(host);
    renderShell({});
    expectPage("Original conversation");
    renderShell();
    await register("two");
    expectPage("Detail", "Detail content");
    expect(button("Detail").getAttribute("aria-current")).toBe("page");
    await click(t("common.back"));
    expectPage("Original conversation");

    act(() => root.unmount());
    root = createRoot(container);
    controller.dispose();
    host = new PluginHost({ react: React });
    controller = new WorkbenchController(host);
    renderShell();
    await register("three");
    expectPage("Original conversation");
    await click("Detail");
    expectPage("Detail", "Detail content");
    renderShell({ inventory: [] });
    expectPage("Original conversation");
    expect(controller.getSnapshot().views).toEqual([]);
  });

  it("keeps different plugins with the same view type independently reachable", async () => {
    await register();
    const other = "test:other-views";
    await host.activateGeneration({ pluginId: other, generation: "one", register(api) {
      api.registerViewType({ id: "catalog", title: "Other catalog", render: () => <article>Other content</article> });
    } });
    renderShell({ inventory: [...inventory, { ...inventory[0], id: other }] });
    await click("Automation");
    await act(async () => { await controller.openPluginView(other, "catalog"); });
    expectPage("Other catalog", "Other content");
    expect(button("Other catalog").getAttribute("aria-current")).toBe("page");
    expect(button("Automation").hasAttribute("aria-current")).toBe(false);
    await click("Automation");
    expectPage("Automation", "Catalog content");
    await click("Other catalog");
    await click(t("workspace.closeTab", { label: "Other catalog" }));
    expectPage("Automation", "Catalog content");
    expect(button("Automation").getAttribute("aria-current")).toBe("page");
  });

  it("advertises back navigation, not tab actions, to header presenters and slots", async () => {
    await register();
    let snapshot: HeaderSnapshotV1 | undefined;
    let presentation: PresentationHost | undefined;
    let slotContext: unknown;
    await host.activateGeneration({ pluginId: "header", generation: "one", register(api) {
      api.registerPresenter({ id: "header", target: "header.workspace", render(props) {
        snapshot = props.snapshot as HeaderSnapshotV1;
        presentation = props.host;
        return <h1>{snapshot.title}</h1>;
      } });
      api.registerSlot("workspace.header", { id: "header-slot", render(props) {
        slotContext = props;
        return null;
      } });
    } });
    renderShell();
    await click("Automation");
    expectPage("Automation", "Catalog content");
    expect(snapshot?.tabs).toBeUndefined();
    expect(snapshot?.activeTabId).toBeUndefined();
    expect(snapshot?.canNavigateBack).toBe(true);
    expect(slotContext).toMatchObject({ hasSessionTabs: false, tabCount: 0 });
    expect(presentation?.actions).toEqual(["header.navigate-back"]);
    await expect(presentation!.invoke("header.select-tab", { tabId: draft.id })).rejects.toThrow();
    await act(async () => { await presentation!.invoke("header.navigate-back"); });
    expectPage("Original conversation");
    await click("Automation");
    await click(t("workspace.closeTab", { label: "Automation" }));
    expectPage("Original conversation");
  });
});
