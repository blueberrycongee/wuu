import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { createScopedTestApplication } from "../test-support/ScopedApplication";
import { ApplicationController } from "../plugins/ApplicationController";
import type { ApplicationAttachmentV1 } from "../../../../packages/plugin-sdk/src/ui-composer";
import { awaitComposerFiles, revokeComposerImagePreview, type ComposerFile, type ComposerImage } from "../ComposerMessages";
import { createApplicationComposerAttachments } from "./ApplicationComposerAttachments";

const pending: ApplicationAttachmentV1 = { id: "file", kind: "file", name: "notes.txt", mediaType: "text/plain", status: "pending" };
const ready: ApplicationAttachmentV1 = { ...pending, status: "ready", data: "aGVsbG8=" };
const generationCleanups: Array<() => void> = [];
afterEach(() => generationCleanups.splice(0).forEach(dispose => dispose()));
async function service(invoke: ReturnType<typeof vi.fn>): Promise<ApplicationServiceV1> {
  const application = new ApplicationController();
  application.invoke = (action, input) => invoke(action, input);
  const scoped = await createScopedTestApplication(application);
  generationCleanups.push(scoped.dispose);
  return scoped.application;
}

// Failures: pending sends must wait for owned bytes; retry must update captured
// message snapshots; view disposal must not settle local UI or revoke shared URLs.
describe("application composer attachment consumers", () => {
  it("keeps a pending send attached to the application resource", async () => {
    let resolve!: (value: ApplicationAttachmentV1) => void;
    const invoke = vi.fn(() => new Promise<ApplicationAttachmentV1>(done => { resolve = done; }));
    const changed = vi.fn();
    const attachments = createApplicationComposerAttachments(await service(invoke), changed);
    const file = attachments.read(pending) as ComposerFile;
    const delivery = awaitComposerFiles([file]);
    resolve(ready);
    await expect(delivery).resolves.toMatchObject([{ id: "file", data: "aGVsbG8=" }]);
    expect(invoke).toHaveBeenCalledWith("composer.resolve-attachment", { attachmentId: "file" });
    expect(changed).toHaveBeenCalledWith("file", expect.objectContaining({ data: "aGVsbG8=" }));
  });

  it("retries failed resource bytes for already captured message snapshots", async () => {
    const invoke = vi.fn().mockRejectedValueOnce(new Error("Read failed")).mockResolvedValueOnce(ready);
    const attachments = createApplicationComposerAttachments(await service(invoke), vi.fn());
    const file = attachments.read(pending) as ComposerFile;
    const captured = { ...file };
    await expect(awaitComposerFiles([captured])).rejects.toThrow("Read failed");
    await captured.preparation!.retry!();
    await expect(awaitComposerFiles([captured])).resolves.toMatchObject([{ id: "file", data: "aGVsbG8=" }]);
    expect(invoke).toHaveBeenLastCalledWith("composer.retry-attachment", { attachmentId: "file" });
  });

  it("stops view updates on disposal while a submitted resource still resolves", async () => {
    let resolve!: (value: ApplicationAttachmentV1) => void;
    const changed = vi.fn();
    const attachments = createApplicationComposerAttachments(await service(vi.fn(() => new Promise<ApplicationAttachmentV1>(done => { resolve = done; }))), changed);
    const file = attachments.read(pending) as ComposerFile;
    const delivery = awaitComposerFiles([file]);
    attachments.dispose();
    resolve(ready);
    await expect(delivery).resolves.toMatchObject([{ id: "file", data: "aGVsbG8=" }]);
    expect(changed).not.toHaveBeenCalled();
  });

  it("leaves preview cleanup to the application resource owner", async () => {
    const revoke = vi.fn();
    const original = URL.revokeObjectURL;
    URL.revokeObjectURL = revoke;
    try {
      const attachments = createApplicationComposerAttachments(await service(vi.fn(() => new Promise(() => {}))), vi.fn());
      const image = attachments.read({ ...pending, kind: "image", mediaType: "image/png", previewUrl: "blob:owned" }) as ComposerImage;
      revokeComposerImagePreview(image);
      attachments.dispose();
      expect(revoke).not.toHaveBeenCalled();
    } finally { URL.revokeObjectURL = original; }
  });
});
