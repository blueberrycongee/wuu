import { useMemo, useRef } from "react";
import { buildSideThreadSlashCommands } from "./ComposerSlashCommands";
import {
  Composer,
  type CodexModelLoadState,
  type ComposerVariant,
} from "./ComposerView";
import { useI18n } from "./i18n";

const EMPTY_MODEL_STATE: CodexModelLoadState = {
  loading: false,
  error: "",
  models: [],
};

const noop = () => {};

export type SideThreadComposerProps = {
  variant?: ComposerVariant;
  placeholder?: string;
  draft: string;
  running: boolean;
  disabledReason?: string;
  queryHistorySessionID: string;
  queryHistory: string[];
  onChangeDraft: (draft: string) => void;
  onSend: (prompt: string) => void;
  onInterrupt: () => void;
  onReset?: () => void;
};

// Side chat is another conversation surface, not another editor. Keep the
// small amount of transport-specific glue here and render the same Composer
// used by the main conversation.
export function SideThreadComposer({
  variant = "dock",
  placeholder,
  draft,
  running,
  disabledReason,
  queryHistorySessionID,
  queryHistory,
  onChangeDraft,
  onSend,
  onInterrupt,
  onReset,
}: SideThreadComposerProps): JSX.Element {
  const { locale, t } = useI18n();
  const runtimeRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const accessMenuRef = useRef<HTMLDivElement>(null);
  const slashCommands = useMemo(() => buildSideThreadSlashCommands(), [locale]);
  const readOnly = Boolean(disabledReason);
  // Keep the draft editable while a side turn runs so the composer remains
  // focusable. Sending stays disabled until the current side turn settles.
  const visibleDraft = draft;

  return (
    <Composer
      variant={variant}
      hideRuntimeControls
      textOnly
      placeholder={placeholder ?? t("composer.sideThreadPlaceholder")}
      prompt={visibleDraft}
      setPrompt={onChangeDraft}
      files={[]}
      images={[]}
      queuedMessages={[]}
      guideMessages={[]}
      running={running}
      sendDisabled={running}
      forceStopWhileRunning
      runtimeControlsDisabled
      tokensPerSecond={0}
      status={disabledReason ?? ""}
      statusLiveProgress={false}
      readOnly={readOnly}
      projects={[]}
      codexModels={EMPTY_MODEL_STATE}
      codexRuntimeMenu={null}
      codexRuntimeRef={runtimeRef}
      menuOpen={false}
      accessMenuOpen={false}
      branchMenuOpen={false}
      menuRef={menuRef}
      accessMenuRef={accessMenuRef}
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
      onPasteAttachmentFiles={noop}
      onRemoveFile={noop}
      onRemoveImage={noop}
      onRemoveQueuedMessage={noop}
      onRemoveGuideMessage={noop}
      onGuideQueuedMessage={noop}
      onEditQueuedMessage={noop}
      onEditGuideMessage={noop}
      onSend={() => {
        const prompt = draft.trim();
        if (!readOnly && !running && prompt) {
          onSend(prompt);
        }
      }}
      onInterrupt={onInterrupt}
      slashCommandsOverride={onReset ? slashCommands : undefined}
      onResetSideThread={onReset}
      queryHistorySessionID={queryHistorySessionID}
      queryHistory={queryHistory}
    />
  );
}
