// Production UserQuestionCard with synthetic requests; nothing is sent. It covers
// the real Composer as an offer, or sits in the reading flow with `inline`.
// Params: theme=dark, size=20, lang=en, width=560, inline, two (a second
// question, inline only), approval (an engine command approval), long, multi,
// expires (a 45 s offer countdown).
import { createRoot } from "react-dom/client";
import { Composer, type CodexModelLoadState } from "../../src/renderer/ComposerView";
import { useComposerDraftState } from "../../src/renderer/ComposerDraftState";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { I18nProvider } from "../../src/renderer/i18n";
import { ToastViewport } from "../../src/renderer/Toast";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { UserQuestionCard } from "../../src/renderer/UserQuestionCard";
import type { UserQuestion, UserQuestionRequest, WuuDesktopApi } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";
import "./fixture.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
// The language picker reads its initial preference from the desktop bridge.
window.wuu = { initialLanguagePreference: params.get("lang") === "en" ? "en-US" : "zh-CN" } as unknown as WuuDesktopApi;
startFocusModality();
applyMessageFlowFontSize(Number(params.get("size")) || 14.5);

const EMPTY_MODELS: CodexModelLoadState = { loading: false, error: "", models: [] };
const noop = () => {};
const inline = params.has("inline");
const long = params.has("long");

// The core builds engine approvals this way (internal/appserver/engine_approvals.go).
const approvalQuestion: UserQuestion = {
  id: "approval.command_execution",
  header: "codex approval",
  question: "Allow this command to run?",
  detail: "rm -rf build dist node_modules/.cache\n\nWorking directory: /workspace/projects/wuu-desktop\n\nReason: clean old build output before the release build",
  options: [{ label: "Allow once" }, { label: "Allow for this session" }, { label: "Deny" }],
};
const scopeQuestion: UserQuestion = {
  id: "scope",
  header: "范围",
  question: "这次要整理哪些文档？",
  detail: long
    ? "README 和 CHANGELOG 由发布流程维护；docs/ 下的中英文页面需要保持对齐，site.json 里没有登记的页面不会出现在文档站。整理时请保留现有链接，避免破坏外部引用。"
    : "README 和 CHANGELOG 由发布流程维护。",
  options: [
    { label: "只整理 docs/", description: "不动 README 和 CHANGELOG" },
    { label: "全部 Markdown 文件" },
    ...(long ? [{ label: "desktop/src/renderer/styles/conversation-shell-and-a-very-long-module-name-without-breaks.css", description: "一个没有空格的长路径" }] : []),
  ],
  allow_custom: true,
  multi_select: params.has("multi"),
};
const toneQuestion: UserQuestion = { id: "tone", question: "写作风格？", options: [{ label: "简洁" }, { label: "详细" }] };

const approval = params.has("approval");
const request: UserQuestionRequest = {
  request_id: "preview",
  plugin_id: approval ? "agent-engine-codex" : "ask-user",
  execution_id: "preview",
  thread_id: "preview",
  turn_id: "preview",
  mode: inline ? "ask" : "offer",
  created_at: new Date().toISOString(),
  expires_at: params.has("expires") && !approval ? new Date(Date.now() + 45_000).toISOString() : undefined,
  questions: approval ? [approvalQuestion] : inline && params.has("two") ? [scopeQuestion, toneQuestion] : [scopeQuestion],
};
const card = <UserQuestionCard request={request} onAnswer={async () => {}} onCancel={async () => {}} onHold={async () => {}} onCustom={noop} />;

function Fixture(): JSX.Element {
  const draft = useComposerDraftState();
  return (
    <div className="fixture-shell" style={{ ["--fixture-width" as string]: `${Number(params.get("width")) || 760}px` }}>
      <main className="fixture-pane">
        <div className="fixture-history">
          <p>帮我整理一下项目文档。</p>
          <p>开始之前想确认一下范围。</p>
          {inline ? <div className="conversation-width" style={{ padding: 0 }}>{card}</div> : null}
        </div>
        <Composer
          variant="dock"
          topAccessory={inline ? undefined : card}
          prompt={draft.prompt}
          promptRevision={draft.promptRevision}
          setPrompt={draft.setPromptFromInput}
          files={draft.composerFiles}
          images={draft.composerImages}
          queuedMessages={[]}
          guideMessages={[]}
          running={false}
          runtimeControlsDisabled
          tokensPerSecond={0}
          status=""
          statusLiveProgress={false}
          readOnly={false}
          projects={[]}
          codexModels={EMPTY_MODELS}
          codexRuntimeMenu={null}
          codexRuntimeRef={{ current: null }}
          menuOpen={false}
          accessMenuOpen={false}
          branchMenuOpen={false}
          menuRef={{ current: null }}
          accessMenuRef={{ current: null }}
          workspaceFilter=""
          setWorkspaceFilter={noop}
          onToggleMenu={noop}
          onToggleAccessMenu={noop}
          onToggleBranchMenu={noop}
          onToggleCodexRuntimeMenu={noop}
          onSelectRuntimeModel={noop}
          onSelectRuntimeEffort={noop}
          onSelectPermissionMode={noop}
          onOpenSettings={noop}
          onOpenSkillsCatalog={noop}
          onSelectWorkspace={noop}
          onSelectNoProject={noop}
          onSelectGitBranch={noop}
          onCreateWorkspace={noop}
          onOpenWorkspace={noop}
          onStartNewThread={noop}
          onOpenWorkspaceTool={noop}
          onOpenInstructions={noop}
          onPasteAttachmentFiles={(files) => void draft.attachComposerAttachmentFiles(files)}
          onRemoveFile={draft.removeComposerFile}
          onRemoveImage={draft.removeComposerImage}
          onRemoveQueuedMessage={noop}
          onRemoveGuideMessage={noop}
          onGuideQueuedMessage={noop}
          onEditQueuedMessage={noop}
          onEditGuideMessage={noop}
          onSend={() => { draft.setPrompt(""); draft.setComposerImages([]); draft.setComposerFiles([]); }}
          onInterrupt={noop}
        />
      </main>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <I18nProvider><WuuUIRoot><ImagePreviewProvider><Fixture /></ImagePreviewProvider><ToastViewport /></WuuUIRoot></I18nProvider>,
);
