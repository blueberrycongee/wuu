import { Check, FileDiff, GitBranch, Plus, Search } from "./WuuIcons";
import { type CSSProperties, useRef, useState } from "react";
import type { GitStatusResult } from "../shared/protocol";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { COMPOSER_PROJECT_MENU_WIDTH } from "./ComposerTypes";
import { hostSupports } from "./HostCapabilities";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

export function ComposerBranchPicker({
  gitStatus, disabled, open, onToggle, onSelect, onCreate, worktreeStart,
}: {
  gitStatus: GitStatusResult;
  disabled: boolean;
  open: boolean;
  onToggle: () => void;
  onSelect: (branch: string) => void | Promise<void>;
  onCreate?: (branch: string) => Promise<void>;
  /** Set while the draft starts in its own worktree: choosing a branch then
   * only moves the start point, and nothing is checked out in the project. */
  worktreeStart?: { branch: string };
}): JSX.Element {
  const { t } = useI18n();
  const anchorRef = useRef<HTMLDivElement>(null);
  const branch = worktreeStart?.branch || gitStatus.branch || "HEAD";
  return (
    <div className="composer-branch-control" ref={anchorRef} onKeyDown={(event) => {
      if (event.key === "Escape" && open) {
        event.stopPropagation();
        onToggle();
        anchorRef.current?.querySelector("button")?.focus();
      }
    }}>
      <button type="button" className="hero-project-pill" aria-haspopup="menu"
        aria-expanded={open && !disabled}
        aria-label={t(worktreeStart ? "composer.worktreeStartBranchLabel" : "composer.switchBranch", { branch })}
        title={branch}
        disabled={disabled} onClick={onToggle}>
        <GitBranch className="hero-project-pill-icon" />
        <span className="hero-project-pill-text">{branch}</span>
      </button>
      {open && !disabled ? (
        <FloatingMenuPortal anchorRef={anchorRef} owner="composer-runtime" placement="above" align="left" width={COMPOSER_PROJECT_MENU_WIDTH}
          mobileSheet={{ label: t(worktreeStart ? "composer.worktreeStartBranch" : "git.branch"), onClose: onToggle }}>
          <ComposerBranchMenu gitStatus={gitStatus} selectedBranch={worktreeStart ? branch : gitStatus.branch}
            checkout={!worktreeStart} onSelect={onSelect} onCreate={onCreate} />
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

function ComposerBranchMenu({ gitStatus, selectedBranch, checkout, onSelect, onCreate }: {
  gitStatus: GitStatusResult;
  selectedBranch?: string;
  /** Choosing a branch checks it out in the project, rather than only
   * naming a worktree's start point. */
  checkout: boolean;
  onSelect: (branch: string) => void | Promise<void>;
  onCreate?: (branch: string) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [pending, setPending] = useState(false);
  const inFlight = useRef(false);
  const name = query.trim();
  // The core lists branches most recently committed first. The checked-out
  // branch (a commit when detached) and the local default branch lead
  // because they are the usual targets.
  const local = gitStatus.branches ?? [];
  const defaultBranch = gitStatus.default_branch && local.includes(gitStatus.default_branch) ? gitStatus.default_branch : "";
  const ordered = [...new Set([gitStatus.branch ?? "", defaultBranch, ...local])].filter(Boolean);
  const branches = ordered.filter((branch) => branch.toLocaleLowerCase().includes(name.toLocaleLowerCase()));
  const canCreate = Boolean(onCreate) && hostSupports("createCheckoutGitBranch");
  const offerCreate = canCreate && name !== "" && !ordered.includes(name);

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

  function selectable(branch: string): boolean {
    return branch !== selectedBranch && !pending && (!checkout || hostSupports("checkoutGitBranch"));
  }

  // Enter acts only on an unambiguous target: the exact branch name, or a new
  // name nothing matches. A partial match never becomes a new branch.
  function submit(): void {
    if (ordered.includes(name)) {
      if (selectable(name)) void run(() => onSelect(name));
    } else if (offerCreate && branches.length === 0) {
      void run(() => onCreate!(name));
    }
  }

  const searchLabel = t(canCreate ? "composer.findOrCreateBranch" : "environment.searchBranches");
  return (
    <div className="composer-project-menu composer-branch-menu" role="menu"
      aria-label={t(checkout ? "git.branch" : "composer.worktreeStartBranch")} aria-busy={pending}
      style={{ "--composer-project-menu-width": `${COMPOSER_PROJECT_MENU_WIDTH}px` } as CSSProperties}>
      <label className="menu-search">
        <Search className="icon-sm" aria-hidden="true" />
        <input autoFocus value={query} aria-label={searchLabel} placeholder={searchLabel}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== "Enter" || event.nativeEvent.isComposing) return;
            event.preventDefault();
            submit();
          }} />
      </label>
      {/* The start point changes nothing in the project; the heading says so. */}
      {checkout ? null : <div className="project-picker-heading">{t("composer.worktreeStartBranch")}</div>}
      {branches.length > 0 || !offerCreate ? <div className="project-picker-list">
        {branches.length === 0 ? <div className="project-picker-empty">{t("environment.noMatchingBranches")}</div> : null}
        {branches.map((branch) => {
          const selected = branch === selectedBranch;
          const separator = branch.lastIndexOf("/") + 1;
          const dirty = branch === gitStatus.branch && gitStatus.dirty_count > 0
            ? t("composer.branchDirtyFiles", { count: gitStatus.dirty_count })
            : "";
          return (
            <button key={branch} type="button" role="menuitemradio" aria-checked={selected}
              disabled={!selectable(branch)} title={branch}
              onClick={() => void run(() => onSelect(branch))}>
              <GitBranch />
              <span className="project-picker-item-title">
                {separator > 0 ? <span className="composer-branch-prefix">{branch.slice(0, separator)}</span> : null}
                {branch.slice(separator)}
              </span>
              <span className="composer-branch-trailing">
                {dirty ? (
                  <span className="composer-branch-dirty" role="img" aria-label={dirty} title={dirty}>
                    <FileDiff aria-hidden="true" />{gitStatus.dirty_count}
                  </span>
                ) : null}
                {selected ? <Check /> : null}
              </span>
            </button>
          );
        })}
      </div> : null}
      {offerCreate ? <>
        {branches.length > 0 ? <div className="project-picker-divider" /> : null}
        <button type="button" role="menuitem" disabled={pending} title={name} onClick={() => void run(() => onCreate!(name))}>
          <Plus />
          <span>{t("composer.createNamedBranch", { branch: name })}</span>
        </button>
      </> : null}
    </div>
  );
}
