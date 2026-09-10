import { isTouchWebShell } from "./ComposerFocus";
import { GitBranch, Laptop } from "lucide-react";
import { useState } from "react";
import { Modal } from "./Modal";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import { useI18n } from "./i18n";

export type ForkMode = "local" | "worktree";

type ForkOption = {
  mode: ForkMode;
  icon: typeof GitBranch;
  titleKey: "fork.localTitle" | "fork.worktreeTitle";
  descriptionKey: "fork.localDescription" | "fork.worktreeDescription";
};

const FORK_OPTIONS: ForkOption[] = [
  {
    mode: "local",
    icon: Laptop,
    titleKey: "fork.localTitle",
    descriptionKey: "fork.localDescription",
  },
  {
    mode: "worktree",
    icon: GitBranch,
    titleKey: "fork.worktreeTitle",
    descriptionKey: "fork.worktreeDescription",
  },
];

// Asks the user whether a fork from a non-latest message should land in
// the same working directory ("local") or in a freshly created git
// worktree ("worktree"). The shared `Modal` chrome handles backdrop,
// focus, Escape, and backdrop-click dismissal — this component only
// owns the option list, the busy-state spinner, and the error note.
//
// `onChoose` resolves when the caller has finished starting the fork;
// the dialog stays open until then so the active spinner / disabled
// state on the chosen option is the visible feedback. Cancelling is
// always available via the Modal's own close affordances (X, Esc,
// backdrop click) while not busy.
export function ConversationForkDialog({
  onCancel,
  onChoose,
  worktreeDisabledReason,
}: {
  onCancel: () => void;
  onChoose: (mode: ForkMode) => void | Promise<void>;
  worktreeDisabledReason?: string;
}): JSX.Element {
  const { t } = useI18n();
  // Tracks which option is mid-flight so only that button shows a spinner
  // and becomes non-interactive. We keep both options clickable even
  // while one is submitting — when the IPC returns, the caller closes
  // the dialog, which remounts this component.
  const [busyMode, setBusyMode] = useState<ForkMode | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | undefined>(undefined);
  const disabled = busyMode !== null;

  async function handleChoose(mode: ForkMode): Promise<void> {
    if (busyMode !== null) {
      return;
    }
    setErrorMessage(undefined);
    setBusyMode(mode);
    try {
      await onChoose(mode);
    } catch (error) {
      setErrorMessage(desktopApiErrorMessage(error, t("thread.forkFailed")));
    } finally {
      // The caller is expected to close the dialog on success, so the
      // setter is only meaningful if the promise resolves inside this
      // tick (rare). Resetting keeps the UI sane if `onChoose` throws
      // and the caller chooses not to dismiss.
      setBusyMode((current) => (current === mode ? null : current));
    }
  }

  return (
    <Modal
      ariaLabel={t("fork.dialogLabel")}
      onClose={onCancel}
      closeDisabled={disabled}
      showCloseButton={false}
      panelClassName="fork-dialog"
    >
      <div className="fork-dialog-options">
        {FORK_OPTIONS.map(({ mode, icon: Icon, titleKey, descriptionKey }) => {
          const isBusy = busyMode === mode;
          const title = t(titleKey);
          const description = t(descriptionKey);
          const disabledReason =
            mode === "worktree" ? worktreeDisabledReason : undefined;
          const optionDisabled = disabled || Boolean(disabledReason);
          return (
            <button
              key={mode}
              className={`fork-dialog-option${isBusy ? " is-busy" : ""}`}
              type="button"
              disabled={optionDisabled}
              aria-label={title}
              onClick={() => void handleChoose(mode)}
            >
              <span className="fork-dialog-option-icon">
                <Icon className="icon-lg" aria-hidden="true" />
              </span>
              <span className="fork-dialog-option-text">
                <strong>{title}</strong>
                {disabledReason || !isTouchWebShell() ? <span>{disabledReason ?? description}</span> : null}
              </span>
              {isBusy ? (
                <span className="fork-dialog-option-spinner" aria-hidden="true" />
              ) : null}
            </button>
          );
        })}
      </div>
      {errorMessage ? (
        <div className="environment-dialog-error" role="alert">
          {errorMessage}
        </div>
      ) : null}
    </Modal>
  );
}
