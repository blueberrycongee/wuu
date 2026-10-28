import { File as NodeFile } from "node:buffer";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AppContextState, InitializeResult, WuuDesktopApi } from "../shared/protocol";
import { AppContextCaptureHost } from "./AppContextCapture";
import { createDraftSessionTab, initialState } from "./AppState";
import { createScopedTestApplication } from "./test-support/ScopedApplication";
import { ApplicationController } from "./plugins/ApplicationController.test-support";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

const generationCleanups: Array<() => void> = [];
let root: Root | undefined;
let captured: AppContextState;
let changed: (() => void) | undefined;
const stop = vi.fn();
const originalHost = window.wuu;

beforeEach(() => {
  vi.stubGlobal("File", NodeFile);
  captured = { available: true, settings: { enabled: true, shortcut: "", include_text: true }, shortcut_registered: true, phase: "idle" };
  changed = undefined;
  stop.mockClear();
});
afterEach(() => {
  act(() => root?.unmount()); root = undefined;
  generationCleanups.splice(0).forEach(dispose => dispose());
  document.body.innerHTML = "";
  window.wuu = originalHost;
  vi.unstubAllGlobals();
});

async function mount(discard: (id?: string) => Promise<AppContextState> = vi.fn(async () => ({ ...captured, phase: "idle" as const }))) {
  window.wuu = { platform: "darwin", getAppContextState: async () => captured,
    onAppContextChanged: (listener: () => void) => { changed = listener; return stop; },
    discardAppContextSnapshot: discard } as unknown as WuuDesktopApi;
  const context = { kind: "no_project" as const, cwd: "/capture-fixture" };
  const application = new ApplicationController({ ...initialState, activeContext: context,
    initialized: { extension_inventory: [] } as unknown as InitializeResult,
    activeSessionTabID: "a", sessionTabs: [createDraftSessionTab("a", context), createDraftSessionTab("b", context)] });
  await application.invoke("composer.set-draft", { text: "Keep this draft" });
  const container = document.createElement("div"); document.body.append(container);
  root = createRoot(container);
  const scoped = await createScopedTestApplication(application);
  generationCleanups.push(scoped.dispose);
  await act(async () => root!.render(<WuuUIRoot><AppContextCaptureHost application={scoped.application} /></WuuUIRoot>));
  return { application, discard };
}

async function deliver() {
  captured = { ...captured, phase: "ready", snapshot: { id: "capture-1", app_name: "Editor", bundle_id: "example.editor",
    captured_at: "2026-10-24T09:00:00Z", window_title: "Example", window_id: 1, width: 100, height: 100,
    image_base64: "aW1hZ2U=", available_text: "Observed content", text_status: "included" } };
  await act(async () => changed?.());
}

// Native consumption and draft attachment must stay with the original public
// owner; a switch or module disposal during consumption cannot reassign bytes.
describe("public application capture", () => {
  it("consumes a native snapshot once and atomically appends both attachments", async () => {
    const { application, discard } = await mount();
    await deliver();
    await vi.waitFor(() => expect(application.getSnapshot().drafts.attachments).toHaveLength(2));
    const snapshot = application.getSnapshot();
    expect(discard).toHaveBeenCalledExactlyOnceWith("capture-1");
    expect(snapshot.drafts.primary.text).toBe("Keep this draft");
    expect(snapshot.drafts.primary.imageIds).toHaveLength(1);
    expect(snapshot.drafts.primary.fileIds).toHaveLength(1);
    expect(Buffer.from(snapshot.drafts.attachments.find(item => item.kind === "file")!.data!, "base64").toString()).toContain("Observed content");
    await deliver();
    expect(discard).toHaveBeenCalledTimes(1);
  });

  it("does not attach a consumed snapshot to another draft", async () => {
    let finish!: (value: AppContextState) => void;
    const discard = vi.fn(() => new Promise<AppContextState>(resolve => { finish = resolve; }));
    const { application } = await mount(discard);
    await deliver();
    await vi.waitFor(() => expect(discard).toHaveBeenCalledOnce());
    await act(async () => { await application.invoke("tab.select", { tabId: "b" }); });
    await act(async () => { finish(captured); });
    expect(application.getSnapshot().drafts.attachments).toHaveLength(0);
    expect(document.body.textContent).toContain("conversation window changed");
  });

  it("stops attachment delivery and subscriptions when the module is removed", async () => {
    let finish!: (value: AppContextState) => void;
    const discard = vi.fn(() => new Promise<AppContextState>(resolve => { finish = resolve; }));
    const { application } = await mount(discard);
    await deliver();
    await vi.waitFor(() => expect(discard).toHaveBeenCalledOnce());
    act(() => root!.unmount()); root = undefined;
    await act(async () => { finish(captured); });
    expect(application.getSnapshot().drafts.attachments).toHaveLength(0);
    expect(stop).toHaveBeenCalledOnce();
  });
});
