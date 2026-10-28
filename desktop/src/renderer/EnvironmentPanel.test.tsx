import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EnvironmentPanel } from "./EnvironmentPanel";
import { PreviewContentProvider } from "./PreviewContentContext";
import type { ApplicationEnvironmentV1 } from "../../../packages/plugin-sdk/src/ui-environment";
import type { ApplicationPreviewContentV1, PreviewFileV1 } from "../../../packages/plugin-sdk/src/ui-preview-content";
let root: Root | undefined;
let container: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); root = undefined; container?.remove(); vi.restoreAllMocks(); });
function renderPreview(workspaceRoot: string | undefined, readFile: ApplicationPreviewContentV1["readFile"]) {
  const snapshot = { contextKey: "context", actionBusy: false, root: workspaceRoot, capabilities: { checkout: true, createBranch: true, commit: true, pullRequest: true } };
  const environment = { getSnapshot: () => snapshot, subscribe: () => () => {} } as unknown as ApplicationEnvironmentV1;
  const service = { readFile } as ApplicationPreviewContentV1;
  if (!root) { container = document.createElement("div"); document.body.appendChild(container); root = createRoot(container); }
  root.render(<PreviewContentProvider service={service}><EnvironmentPanel environment={environment} panelRef={createRef()} motionState="open"
    activeMenu="file" running={false} pullRequestDisabledReason="" rightPanelFilePath="note.txt"
    onSetActiveMenu={() => {}} onClose={() => {}} onOpenReview={() => {}} onOpenCommit={() => {}} onOpenPullRequest={() => {}} /></PreviewContentProvider>);
}
function file(text: string): PreviewFileV1 {
  return { root: "/two", path: "note.txt", absolute_path: "/two/note.txt", size_bytes: text.length, mtime_ms: 1, sha256: "digest", binary: false, truncated: false, text };
}
describe("EnvironmentPanel public file preview", () => {
  it("passes the explicit environment root and discards a late previous-workspace read", async () => {
    let finish!: (value: PreviewFileV1) => void;
    const readFile = vi.fn().mockImplementationOnce(() => new Promise<PreviewFileV1>(resolve => { finish = resolve; })).mockResolvedValue(file("current content"));
    await act(async () => { renderPreview("/one", readFile); });
    expect(readFile).toHaveBeenCalledWith({ path: "note.txt", root: "/one" }, expect.objectContaining({ signal: expect.any(AbortSignal) }));
    const signal = readFile.mock.calls[0][1].signal as AbortSignal;
    await act(async () => { renderPreview("/two", readFile); });
    expect(signal.aborted).toBe(true);
    expect(container.textContent).toContain("current content");
    await act(async () => { finish(file("stale content")); });
    expect(container.textContent).not.toContain("stale content");
  });
  it("does not read an implicit active workspace when the environment root is missing", async () => {
    const readFile = vi.fn().mockResolvedValue(file("unexpected"));
    await act(async () => { renderPreview(undefined, readFile); });
    expect(readFile).not.toHaveBeenCalled();
    expect(container.textContent).toContain("session working directory is unavailable");
  });
});
