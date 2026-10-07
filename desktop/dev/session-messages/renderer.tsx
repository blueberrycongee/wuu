import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import type { Thread, Turn } from "../../src/shared/protocol";
import { emptyComposerDraft } from "../../src/renderer/AppState";
import { ConversationSplitPane } from "../../src/renderer/ConversationSplitPane";
import { setOpenThreadInSplitHandler } from "../../src/renderer/ConversationSplitBridge";
import { I18nProvider } from "../../src/renderer/i18n";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { applyMeasuredScrollbarWidth } from "../../src/renderer/ScrollbarMetrics";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
applyMessageFlowFontSize(Number(params.get("size")) || 14);
startFocusModality();
applyMeasuredScrollbarWidth();
const noAction = (): void => {};
const sourceName = params.has("long") ? "Reviewer with a deliberately long conversation name and another project" : "Fusion Sidekick";
const notification = (index: number): Turn => ({
  id: `notice-${index}`, status: "completed", items_view: "full", items: [{
    id: `message-${index}`, type: "user_message", origin: "host", name: sourceName,
    presentation_kind: "session_message", related_session_id: params.has("missing") ? undefined : "sidekick",
    read_only: true, text: `Private dispatch fusion:long-id-${index}. Complete internal report.`,
  }],
});
const base: Thread = {
  id: "lead", preview: "Lead", title: "Lead", model_provider: "fixture", model: "fixture-lead",
  cwd: "/synthetic/project", status: "idle", created_at: "", updated_at: "", read_only: true, turns: [],
};
const primary: Thread = { ...base, turns: [
  { id: "question", status: "completed", items_view: "full", items: [
    { id: "user", type: "user_message", text: "Review the implementation and verify the result." },
  ] },
  ...Array.from({ length: 5 }, (_, index) => notification(index)),
  { id: "answer", status: "completed", items_view: "full", items: [
    { id: "lead-answer", type: "agent_message", status: "completed", terminal: true, text: "I reviewed the changes and verified the result." },
  ] },
  notification(5), notification(6),
] };
const secondary: Thread = { ...base, id: "sidekick", title: params.has("long") ? sourceName : "Sidekick", preview: "Sidekick", turns: params.has("empty") ? [] : [
  { id: "side-turn", status: "completed", items_view: "full", items: [
    { id: "side-task", type: "user_message", origin: params.has("plain") ? undefined : "host", cause: "fusion",
      presentation_kind: "session_message", related_session_id: "lead", name: "",
      text: "Implement the requested change and run verification." },
    { id: "verification", type: "tool_call", name: "shell", status: "completed", arguments: JSON.stringify({ command: "node verify.cjs" }), result: "All checks passed." },
    { id: "side-answer", type: "agent_message", status: "completed", terminal: true,
      text: "Sidekick conversation: implemented the change. All checks passed.\n\n" + Array.from({ length: 12 }, (_, index) => `Verification step ${index + 1}: checked the implementation, reviewed the result, and confirmed the expected behavior.`).join("\n\n") },
  ] },
] };

function Fixture(): JSX.Element {
  const [opened, setOpened] = useState(false);
  useEffect(() => {
    setOpenThreadInSplitHandler(id => { if (id === secondary.id) setOpened(true); });
    return () => setOpenThreadInSplitHandler(undefined);
  }, []);
  const pane = (thread: Thread, position: "primary" | "secondary"): JSX.Element => <ConversationSplitPane
    key={thread.id} pane={position} thread={thread} active activeContextCwd={thread.cwd}
    appStatus="" draft={emptyComposerDraft()} viewSwitchPending={false} queryHistory={[]}
    onActivate={noAction} onClose={() => setOpened(false)} onBodyRef={noAction} onScroll={noAction}
    onSetPrompt={noAction} onPasteAttachmentFiles={noAction} onRemoveFile={noAction} onRemoveImage={noAction}
    onSend={noAction} onInterrupt={noAction} onForkMessage={noAction} onStreamFrame={noAction} />;
  return <main className="conversation-pane" style={{ height: "100dvh", padding: 16, display: "block" }}>
    <div style={{ display: "grid", gridTemplateColumns: opened ? "minmax(0, 1fr) minmax(0, 1fr)" : "minmax(0, 1fr)", height: "100%", gap: 16 }}>
      {pane(primary, "primary")}{opened ? pane(secondary, "secondary") : null}
    </div>
  </main>;
}

const root = document.getElementById("root");
if (root) createRoot(root).render(<I18nProvider preferenceStore={{
  get: () => params.get("lang") === "zh" ? "zh-CN" : "en-US", set: async () => {}, subscribe: () => noAction,
}}><WuuUIRoot><ImagePreviewProvider><Fixture /></ImagePreviewProvider></WuuUIRoot></I18nProvider>);
