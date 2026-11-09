import { describe, expect, it, vi } from "vitest";
import { initialState, type AppState } from "../AppState";
import type { WuuDesktopApi } from "../../shared/protocol";
import { ApplicationController } from "./ApplicationController.test-support";

// A replacement owns the draft, but both generations may still hold reads of
// the same physical preparation. Retirement must stop publication, not bytes.
describe("draft preparation across application retirement", () => {
  it.each(["ready", "failed"] as const)("settles %s preparation without publishing from the retired owner", async outcome => {
    window.wuu = { onServerEvent: () => () => {}, listUserQuestions: async () => ({ questions: [] }) } as unknown as WuuDesktopApi;
    const context = { kind: "no_project" as const, cwd: "/fixture" };
    const initialized = { provider: "fixture", model: "fixture", providers: [] } as unknown as AppState["initialized"];
    const old = new ApplicationController({ ...initialState, initialized, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "original", images: [], files: [] },
    ] });
    const stopOld = old.start();
    let finish!: (bytes: ArrayBuffer) => void;
    let fail!: (error: Error) => void;
    const file = new File(["a"], "note.txt", { type: "text/plain" });
    Object.defineProperty(file, "arrayBuffer", { value: () => new Promise<ArrayBuffer>((resolve, reject) => { finish = resolve; fail = reject; }) });
    const { attachments: [attachment] } = await old.invoke("composer.attach", { files: [file] });
    const oldRead = old.invoke("composer.resolve-attachment", { attachmentId: attachment.id });
    const oldResult = oldRead.then(value => value, error => error);
    stopOld();
    const checkpoint = old.exportCheckpoint();
    const next = new ApplicationController(checkpoint.state);
    const stopNext = next.start();
    const oldPublished = vi.fn();
    const off = old.subscribe(oldPublished);
    try {
      await next.invoke("composer.checkpoint", { activeTabId: "draft:a", primary: { text: "newer draft", imageIds: [], fileIds: [attachment.id], selections: [] } });
      const nextRead = next.invoke("composer.resolve-attachment", { attachmentId: attachment.id });
      const nextResult = nextRead.then(value => value, error => error);
      const retiredSnapshot = old.getSnapshot();
      const retiredState = old.getState();
      if (outcome === "ready") finish(new Uint8Array([65]).buffer);
      else fail(new Error("attachment read failed"));
      const results = await Promise.all([oldResult, nextResult]);
      if (outcome === "ready") for (const result of results) expect(result).toMatchObject({ id: attachment.id, status: "ready", data: "QQ==" });
      else for (const result of results) expect(result).toMatchObject({ message: "attachment read failed" });
      expect(oldPublished).not.toHaveBeenCalled();
      expect(old.getSnapshot()).toBe(retiredSnapshot);
      expect(old.getState()).toBe(retiredState);
      expect(next.getSnapshot().drafts.primary.text).toBe("newer draft");
      expect(next.getSnapshot().drafts.primary.fileIds).toEqual([attachment.id]);
      expect(next.getSnapshot().drafts.attachments[0].status).toBe(outcome);
    } finally { off(); stopOld(); stopNext(); }
  });
});
