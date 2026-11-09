import { describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
import { createComposerControls } from "./ComposerControls";

function setup() {
  let snapshot = {
    tabs: { activeTabId: "first", items: [{ id: "first", threadId: "thread" }] },
    drafts: { activeTabId: "first", activePane: "primary", split: true,
      primary: { ownerTabId: "first", text: "hello", imageIds: ["image"], fileIds: ["file"], selections: [] },
      secondary: { ownerTabId: "second", text: "other", imageIds: [], fileIds: [], selections: [] }, attachments: [] },
    submission: { operations: [], pendingCreations: [], stopRequests: [], failedDraftTabIds: [] },
  } as unknown as ApplicationSnapshotV1;
  const invoke = vi.fn().mockResolvedValue(undefined);
  const application = { getSnapshot: () => snapshot, invoke } as unknown as ApplicationServiceV1;
  const controls = createComposerControls(application, { ownerTabId: "first", pane: "primary" });
  return { controls, invoke, snapshot: () => snapshot, replace: (value: ApplicationSnapshotV1) => { snapshot = value; } };
}

describe("composer public source controls", () => {
  it("addresses text and runtime choices to the captured owner", async () => {
    const { controls, invoke } = setup();
    await controls.setText("new"); await controls.chooseRuntime({ kind: "native-model", model: "", effort: "" });
    expect(invoke).toHaveBeenNthCalledWith(1, "composer.set-draft", { ownerTabId: "first", text: "new" });
    expect(invoke).toHaveBeenNthCalledWith(2, "composer.choose-runtime", { ownerTabId: "first", selection: { kind: "native-model", model: "", effort: "" } });
  });
  it("removes references atomically before releasing only unreferenced bytes", async () => {
    const { controls, invoke } = setup();
    let release!: () => void;
    invoke.mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve; }));
    const removal = controls.removeAttachment("file");
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(invoke.mock.calls[0]).toEqual(["composer.checkpoint", expect.objectContaining({ activeTabId: "first", primary: expect.objectContaining({ fileIds: [], imageIds: ["image"] }), secondary: expect.objectContaining({ text: "other" }) })]);
    release(); await removal;
    expect(invoke).toHaveBeenLastCalledWith("composer.release-attachments", { attachmentIds: ["file"], ifUnreferenced: true });
  });
  it("preserves successful siblings when a pasted file fails admission", async () => {
    const { controls, invoke } = setup(); const good = new File(["a"], "a"); const bad = new File(["b"], "b");
    invoke.mockImplementation((_action, input) => input.files[0] === bad ? Promise.reject(new Error("Too large")) : Promise.resolve({ attachments: [] }));
    const results = await controls.attach([good, bad]);
    expect(results.map(result => result.status)).toEqual(["fulfilled", "rejected"]);
    expect(invoke.mock.calls.map(([, input]) => input.ownerTabId)).toEqual(["first", "first"]);
  });
  it("rejects a stale source before submitting or mutating another tab", async () => {
    const { controls, invoke, snapshot, replace } = setup();
    replace({ ...snapshot(), drafts: { ...snapshot().drafts, primary: { ...snapshot().drafts.primary, ownerTabId: "replacement" } } });
    await expect(controls.submit("queue")).rejects.toThrow("no longer");
    await expect(controls.setText("bad")).rejects.toThrow("no longer"); expect(invoke).not.toHaveBeenCalled();
  });
  it("cancels the source pending creation rather than interrupting another thread", async () => {
    const { controls, invoke, snapshot, replace } = setup();
    replace({ ...snapshot(), submission: { ...snapshot().submission, pendingCreations: [{ ownerTabId: "first", operationID: "creating" } as never] } });
    await controls.stop(); expect(invoke).toHaveBeenCalledWith("composer.cancel-submission", { operationID: "creating" });
  });
  it("keeps send/queue/steer payload and captured content unchanged", async () => {
    const { controls, invoke } = setup();
    await controls.submit("steer", { text: "next", consumedOfferID: "offer", activeDocument: { path: "readme.md" } });
    expect(invoke).toHaveBeenCalledWith("composer.submit", { ownerTabId: "first", mode: "steer", text: "next", consumedOfferID: "offer", activeDocument: { path: "readme.md" } });
  });
});
