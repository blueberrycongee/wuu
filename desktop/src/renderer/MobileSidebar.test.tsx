import { act, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MobileSidebar } from "./MobileSidebar";
import { initialState, SCRATCH_PSEUDO_PROJECT_ID, type ThreadSummary } from "./AppState";
import { translateCurrent } from "./i18n";

let container: HTMLDivElement;
let root: Root;
let props: ComponentProps<typeof MobileSidebar>;
const date = "2026-01-01T00:00:00Z";
function thread(id: string): ThreadSummary {
  return { id, title: id, preview: id, cwd: `/repo/${id}`, model_provider: "openai",
    model: "test", status: "idle", created_at: date, updated_at: date, turns: [], turn_count: 0 };
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  props = {
    visible: true,
    state: { ...initialState, activeProjectId: "one", activeContext: { kind: "project", project_id: "one", cwd: "/repo/one" } },
    sidebarProjects: [SCRATCH_PSEUDO_PROJECT_ID, "one", "two"].map((id) => ({ id, name: id, path: id === SCRATCH_PSEUDO_PROJECT_ID ? "" : `/repo/${id}`, created_at: date, updated_at: date })),
    activeThreadID: "first",
    projectThreadsByProjectID: { one: [thread("first")], two: [thread("second")], [SCRATCH_PSEUDO_PROJECT_ID]: [] },
    expandedSidebarSectionIDs: new Set(["one"]),
    onToggleSidebarSectionCollapsed: vi.fn(),
    onStartNewThreadForProject: vi.fn(), onSelectProjectThread: vi.fn(),
    onTogglePinned: vi.fn(), onArchiveThread: vi.fn(), onRenameThread: vi.fn(), onDeleteThread: vi.fn(),
    onRemoveProject: vi.fn(), onRelocateProject: vi.fn(), onCreateProject: vi.fn(), onOpenProjectFolder: vi.fn(),
    commands: [],
  };
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});
function render() { act(() => root.render(<MobileSidebar {...props} />)); }
function click(label: string) {
  const button = [...container.querySelectorAll("button")].find((item) => item.getAttribute("aria-label") === label || item.textContent === label);
  expect(button, label).toBeDefined();
  act(() => button!.click());
}
function chooseProject(id: string) {
  click(translateCurrent("sidebar.switchProject"));
  const button = [...container.querySelectorAll("button")].find((item) => item.querySelector("strong")?.textContent === id);
  expect(button).toBeDefined();
  act(() => button!.click());
}

it("browses projects without navigating away, then creates and selects in that project", () => {
  render();
  chooseProject("two");
  expect(props.onSelectProjectThread).not.toHaveBeenCalled();
  expect(props.onToggleSidebarSectionCollapsed).toHaveBeenCalledWith("two");
  click(translateCurrent("sidebar.newConversation"));
  expect(props.onStartNewThreadForProject).toHaveBeenCalledWith("two");
  click("second");
  expect(props.onSelectProjectThread).toHaveBeenCalledWith("two", "second");
});

it("reopens at the active conversation rather than the last browsed project", () => {
  render();
  chooseProject("two");
  props = { ...props, visible: false };
  render();
  props = { ...props, visible: true };
  render();
  expect(container.textContent).toContain("first");
  expect(container.textContent).not.toContain("second");
});

it("lets native Back leave project selection before closing the drawer", () => {
  render();
  click(translateCurrent("sidebar.switchProject"));
  const closeDrawer = vi.fn();
  window.addEventListener("wuu:workbench-back", closeDrawer);
  try {
    const back = new Event("wuu:workbench-back", { cancelable: true });
    act(() => window.dispatchEvent(back));
    expect(back.defaultPrevented).toBe(true);
    expect(closeDrawer).not.toHaveBeenCalled();
    act(() => window.dispatchEvent(new Event("wuu:workbench-back", { cancelable: true })));
    expect(closeDrawer).toHaveBeenCalledOnce();
  } finally {
    window.removeEventListener("wuu:workbench-back", closeDrawer);
  }
});

it("keeps empty loading and missing projects distinct and prevents creating in a missing folder", () => {
  props = { ...props, projectThreadsByProjectID: {}, loadingProjectThreadIDs: new Set(["one"]) };
  render();
  expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
  expect(container.textContent).not.toContain(translateCurrent("sidebar.noConversations"));
  props = { ...props, loadingProjectThreadIDs: new Set(), sidebarProjects: props.sidebarProjects.map((item) => ({ ...item, missing: item.id === "one" })) };
  render();
  click(translateCurrent("sidebar.newConversation"));
  expect(props.onStartNewThreadForProject).not.toHaveBeenCalled();
  expect(container.textContent).toContain(translateCurrent("threadSidebar.missingWorkspace"));
});

it("exposes touch actions without selecting a conversation and confirms permanent deletion", () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  render();
  click(translateCurrent("sidebar.conversationActions", { title: "first" }));
  click(translateCurrent("threadSidebar.delete"));
  expect(props.onDeleteThread).not.toHaveBeenCalled();
  expect(props.onSelectProjectThread).not.toHaveBeenCalled();
  confirm.mockReturnValue(true);
  click(translateCurrent("threadSidebar.delete"));
  expect(props.onDeleteThread).toHaveBeenCalledWith(expect.objectContaining({ id: "first" }));
});

it("dismisses the drawer when opening an extension view from More", () => {
  const openView = vi.fn();
  const closeDrawer = vi.fn();
  props = { ...props, onNavigateAway: closeDrawer, commands: [
    { id: "plugin:example:notes", kind: "command", label: "Notes", onActivate: openView },
  ] };
  render();
  click(translateCurrent("sidebar.more"));
  click("Notes");
  expect(closeDrawer).toHaveBeenCalledOnce();
  expect(openView).toHaveBeenCalledOnce();
});
