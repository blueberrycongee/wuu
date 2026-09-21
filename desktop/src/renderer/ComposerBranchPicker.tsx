import { ChevronDown, GitBranch, Plus } from "lucide-react";
import { useRef, useState } from "react";
import type { GitStatusResult } from "../shared/protocol";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import {
  ComposerPickerCard,
  ComposerPickerList,
  ComposerPickerRow,
  ComposerPickerSearch
} from "./ComposerPickerCard";
import { hostSupports } from "./HostCapabilities";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

export function ComposerBranchPicker({
  gitStatus, disabled, open, onToggle, onSelect, onCreate,
}: {
  gitStatus: GitStatusResult;
  disabled: boolean;
  open: boolean;
  onToggle: () => void;
  onSelect: (branch: string) => void | Promise<void>;
  onCreate?: (branch: string) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const anchorRef = useRef<HTMLDivElement>(null);
  const branch = gitStatus.branch || "HEAD";

  // One dismissal for both Escape paths: the card itself (React bubbles the
  // key from the portaled card back through this component) and the trigger
  // while the card is open.
  function dismiss(): void {
    onToggle();
    anchorRef.current?.querySelector("button")?.focus();
  }

  return (
    <div className="composer-branch-control" ref={anchorRef} onKeyDown={(event) => {
      if (event.key === "Escape" && open) {
        event.stopPropagation();
        dismiss();
      }
    }}>
      <button type="button" className="hero-project-pill" aria-haspopup="menu"
        aria-expanded={open && !disabled} aria-label={t("composer.switchBranch", { branch })}
        title={branch}
        disabled={disabled} onClick={onToggle}>
        <GitBranch className="hero-project-pill-icon" />
        <span className="hero-project-pill-text">{branch}</span>
        <ChevronDown className="hero-project-pill-chevron" />
      </button>
      {open && !disabled ? (
        <FloatingMenuPortal anchorRef={anchorRef} owner="composer-runtime" placement="above" align="left" width={280}
          mobileSheet={{ label: t("git.branch"), onClose: onToggle }}>
          <ComposerBranchMenu gitStatus={gitStatus} onSelect={onSelect} onCreate={onCreate} onDismiss={dismiss} />
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

function ComposerBranchMenu({ gitStatus, onSelect, onCreate, onDismiss }: {
  gitStatus: GitStatusResult;
  onSelect: (branch: string) => void | Promise<void>;
  onCreate?: (branch: string) => Promise<void>;
  onDismiss: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const inFlight = useRef(false);
  const branches = [...new Set([...(gitStatus.branch ? [gitStatus.branch] : []), ...(gitStatus.branches ?? [])])]
    .filter((branch) => branch.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));

  async function run(action: () => void | Promise<void>): Promise<void> {
    if (inFlight.current) return;
    inFlight.current = true;
    setPending(true);
    try {
      await action();
    } catch (error) {
      showErrorToast(error, t("git.checkoutFailed"));
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  }

  return (
    <ComposerPickerCard label={t("git.branch")} busy={pending} onDismiss={onDismiss}>
      <ComposerPickerSearch label={t("environment.searchBranches")} value={query} onChange={setQuery} />
      <ComposerPickerList empty={branches.length === 0} emptyMessage={t("environment.noMatchingBranches")}>
        {branches.map((branch) => {
          const selected = branch === gitStatus.branch;
          return (
            <ComposerPickerRow key={branch} icon={<GitBranch />} label={branch} title={branch} selected={selected}
              disabled={selected || pending || !hostSupports("checkoutGitBranch")}
              onSelect={() => void run(() => onSelect(branch))} />
          );
        })}
      </ComposerPickerList>
      {onCreate && hostSupports("createCheckoutGitBranch") ? <>
        <div className="project-picker-divider" />
        {creating ? (
          <form className="composer-picker-create" onSubmit={(event) => {
            event.preventDefault();
            if (name.trim()) void run(() => onCreate(name.trim()));
          }}>
            <input autoFocus aria-label={t("environment.newBranchName")} placeholder={t("environment.newBranchName")}
              value={name} disabled={pending} onChange={(event) => setName(event.target.value)} />
            <button type="submit" aria-label={t("common.create")} disabled={pending || !name.trim()}><Plus /></button>
          </form>
        ) : (
          <ComposerPickerRow icon={<Plus />} label={t("composer.createBranch")} disabled={pending}
            onSelect={() => { setName(query.trim()); setCreating(true); }} />
        )}
      </> : null}
    </ComposerPickerCard>
  );
}
