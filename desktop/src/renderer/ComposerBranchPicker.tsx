import { Check, ChevronDown, GitBranch, Plus, Search } from "lucide-react";
import { useRef, useState } from "react";
import type { GitStatusResult } from "../shared/protocol";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { hostSupports } from "./HostCapabilities";
import { useI18n } from "./i18n";

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
  return (
    <div className="composer-branch-control" ref={anchorRef} onKeyDown={(event) => {
      if (event.key === "Escape" && open) {
        event.stopPropagation();
        onToggle();
        anchorRef.current?.querySelector("button")?.focus();
      }
    }}>
      <button type="button" className="hero-project-pill" aria-haspopup="menu"
        aria-expanded={open && !disabled} aria-label={t("composer.switchBranch", { branch })}
        title={disabled ? t("git.checkoutBlockedByRunningThread") : branch}
        disabled={disabled} onClick={onToggle}>
        <GitBranch className="hero-project-pill-icon" />
        <span className="hero-project-pill-text">{branch}</span>
        <ChevronDown className="hero-project-pill-chevron" />
      </button>
      {open && !disabled ? (
        <FloatingMenuPortal anchorRef={anchorRef} owner="composer-runtime" placement="above" align="left" width={280}
          mobileSheet={{ label: t("git.branch"), onClose: onToggle }}>
          <ComposerBranchMenu gitStatus={gitStatus} onSelect={onSelect} onCreate={onCreate} />
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

function ComposerBranchMenu({ gitStatus, onSelect, onCreate }: {
  gitStatus: GitStatusResult;
  onSelect: (branch: string) => void | Promise<void>;
  onCreate?: (branch: string) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const branches = [...new Set([...(gitStatus.branch ? [gitStatus.branch] : []), ...(gitStatus.branches ?? [])])]
    .filter((branch) => branch.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));

  async function run(action: () => void | Promise<void>): Promise<void> {
    if (inFlight.current) return;
    inFlight.current = true;
    setPending(true);
    setError("");
    try {
      await action();
    } catch (error) {
      setError(error instanceof Error ? error.message : t("git.checkoutFailed"));
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  }

  return (
    <div className="composer-project-menu composer-branch-menu" role="menu" aria-label={t("git.branch")} aria-busy={pending}>
      <label className="menu-search">
        <Search />
        <input autoFocus value={query} aria-label={t("environment.searchBranches")} placeholder={t("environment.searchBranches")}
          onChange={(event) => setQuery(event.target.value)} />
      </label>
      <div className="project-picker-list">
        {branches.length === 0 ? <div className="project-picker-empty">{t("environment.noMatchingBranches")}</div> : null}
        {branches.map((branch) => {
          const selected = branch === gitStatus.branch;
          return (
            <button key={branch} type="button" role="menuitemradio" aria-checked={selected}
              disabled={selected || pending || !hostSupports("checkoutGitBranch")} title={branch}
              onClick={() => void run(() => onSelect(branch))}>
              <GitBranch />
              <span>{branch}{selected && gitStatus.dirty_count > 0 ? (
                <small>{t("composer.branchDirtyFiles", { count: gitStatus.dirty_count })}</small>
              ) : null}</span>
              {selected ? <Check /> : null}
            </button>
          );
        })}
      </div>
      {onCreate && hostSupports("createCheckoutGitBranch") ? <>
        <div className="project-picker-divider" />
        {creating ? (
          <form className="composer-branch-create" onSubmit={(event) => {
            event.preventDefault();
            if (name.trim()) void run(() => onCreate(name.trim()));
          }}>
            <input autoFocus aria-label={t("environment.newBranchName")} placeholder={t("environment.newBranchName")}
              value={name} disabled={pending} onChange={(event) => setName(event.target.value)} />
            <button type="submit" disabled={pending || !name.trim()} aria-label={t("composer.createBranch")}><Plus /></button>
          </form>
        ) : <button type="button" role="menuitem" disabled={pending} onClick={() => { setName(query.trim()); setCreating(true); }}>
          <Plus /><span>{t("composer.createBranch")}</span>
        </button>}
      </> : null}
      {error ? <div className="environment-side-error" role="alert">{error}</div> : null}
    </div>
  );
}
