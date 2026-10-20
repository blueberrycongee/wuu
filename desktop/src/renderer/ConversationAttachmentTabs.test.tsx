import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { useWorkspaceToolState } from "./WorkspaceToolState";
import { useConversationAttachmentTabs } from "./ConversationAttachmentTabs";

it.each([false, true])("keeps Coding tabs, active-file and activation history unchanged with saved open=%s", async savedOpen => {
  const writes = vi.fn();
  let coding!: ReturnType<typeof useWorkspaceToolState>, twin!: ReturnType<typeof useConversationAttachmentTabs>;
  function Harness() {
    const [open, setOpen] = useState(savedOpen);
    coding = useWorkspaceToolState({ rightPanelOpen: open, rightPanelGlobalized: false, setRightPanelOpenWithMotion: value => { writes(value); setOpen(value); } });
    twin = useConversationAttachmentTabs();
    return null;
  }
  const container = document.createElement("div"), root = createRoot(container);
  try {
    await act(async () => root.render(<Harness />));
    await act(async () => coding.openWorkspaceFileTab({ context: { kind: "no_project", cwd: "/coding" }, path: "a.txt" }));
    const first = coding.workspaceActiveViewTabID!;
    await act(async () => coding.openWorkspaceFileTab({ context: { kind: "no_project", cwd: "/coding" }, path: "b.txt" }));
    const second = coding.workspaceActiveViewTabID!;
    await act(async () => coding.openWorkspaceDiffTab({ threadID: "coding", path: "c.txt", selection: { path: "c.txt", additions: 1, deletions: 0, newFile: true } }));
    const diff = coding.workspaceActiveViewTabID!;
    // Revisit the first file, creating a non-adjacent close-history target.
    await act(async () => coding.focusWorkspaceViewTab(first));
    if (!savedOpen) await act(async () => coding.toggleRightPanel());
    const snapshot = { tabs: coding.workspaceViewTabs, active: coding.workspaceActiveViewTabID, file: coding.workspaceActiveFileTabID };
    writes.mockClear();
    const attachment = { resourceId: "twin:1", name: "a.txt", mediaType: "text/plain", size: 1, read: async () => new Blob(["x"]) };
    await act(async () => twin.openAttachment(attachment));
    await act(async () => twin.openAttachment({ ...attachment, resourceId: "twin:2" }));
    await act(async () => twin.focusTab(twin.state.tabs[0].id));
    await act(async () => twin.setManualGlobalized(true));
    await act(async () => twin.focusConversation());
    await act(async () => twin.setOpen(false));
    await act(async () => twin.openAttachment(attachment));
    await act(async () => twin.closeTab(twin.state.tabs[0].id));
    await act(async () => twin.closeTab(twin.state.tabs[0].id));
    expect(twin.open).toBe(false);
    expect(writes).not.toHaveBeenCalled();
    expect(coding.workspaceViewTabs).toBe(snapshot.tabs);
    expect(coding.workspaceActiveViewTabID).toBe(snapshot.active);
    expect(coding.workspaceActiveFileTabID).toBe(snapshot.file);
    await act(async () => coding.closeWorkspaceViewTab(first));
    expect(coding.workspaceActiveViewTabID).toBe(diff);
    await act(async () => coding.closeWorkspaceViewTab(diff));
    expect(coding.workspaceActiveViewTabID).toBe(second);
  } finally { act(() => root.unmount()); }
});
