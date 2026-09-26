import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import type { ProjectCandidate, Thread } from "../shared/protocol";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { SelectMenu } from "./SelectMenu";
import { baseThreadTitle } from "./ThreadTitles";
import { ArrowUpRight, FileDiff } from "./WuuIcons";
import { GitPatchLines } from "./WorkspaceReviewPanels";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

// Desktop plugins register publishers for this context; they receive
// { candidate, title } and may return { url }.
const PUBLISH_CONTEXT = "project-candidate.publish";
const VISIBLE_FILE_COUNT = 8;

function candidateKey(candidate: ProjectCandidate): string {
  return `${candidate.session_id}:${candidate.turn_id}`;
}

/**
 * A managed session's frozen changes, newest first. Each turn that changed
 * files leaves one candidate; the user applies or discards it here.
 */
export function ProjectCandidateReview({ thread }: { thread: Thread }): JSX.Element | null {
  const { t } = useI18n();
  const [candidates, setCandidates] = useState<ProjectCandidate[]>([]);
  const latestTurn = thread.turns[thread.turns.length - 1];
  const load = useCallback(async (): Promise<void> => {
    if (!window.wuu.projectCandidate) return;
    try {
      const result = await window.wuu.projectCandidate({ action: "list", session_id: thread.id });
      setCandidates([...(result.candidates ?? [])].sort((a, b) =>
        (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0)));
    } catch (error) {
      showErrorToast(error);
    }
  }, [thread.id]);
  // A finished turn can freeze a new candidate.
  useEffect(() => { void load(); }, [load, thread.latest_completed_turn_id, latestTurn?.status]);
  if (!candidates.length) return null;
  const title = baseThreadTitle(thread);
  return <section className="project-candidate-list" aria-label={t("projects.candidate.list")}>
    {candidates.map(candidate => <ProjectCandidateCard key={candidateKey(candidate)} candidate={candidate}
      title={title} onDecided={load} />)}
  </section>;
}

function ProjectCandidateCard({ candidate, title, onDecided }: {
  candidate: ProjectCandidate; title: string; onDecided: () => Promise<void>;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const [diffOpen, setDiffOpen] = useState(false);
  const [diff, setDiff] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [showAllFiles, setShowAllFiles] = useState(false);
  const [publishedURL, setPublishedURL] = useState("");
  const subscribe = useCallback((listener: () => void) => desktopPluginHost.subscribe(listener), []);
  const getCommands = useCallback(() => desktopPluginHost.getCommands(), []);
  const publishers = useSyncExternalStore(subscribe, getCommands, getCommands)
    .filter(command => command.contexts?.includes(PUBLISH_CONTEXT));
  const [publisherID, setPublisherID] = useState("");
  const publisher = publishers.find(command => `${command.pluginId}:${command.id}` === publisherID) ?? publishers[0];
  const request = { session_id: candidate.session_id, turn_id: candidate.turn_id };

  async function toggleDiff(): Promise<void> {
    setDiffOpen(!diffOpen);
    if (diffOpen || diff !== undefined || !window.wuu.projectCandidate) return;
    try {
      const result = await window.wuu.projectCandidate({ action: "get", ...request });
      setDiff(result.candidate?.diff ?? "");
    } catch (error) {
      setDiffOpen(false);
      showErrorToast(error);
    }
  }

  async function decide(action: "apply" | "discard"): Promise<void> {
    if (!window.wuu.projectCandidate) return;
    setBusy(true);
    try {
      await window.wuu.projectCandidate({ action, ...request });
    } catch (error) {
      showErrorToast(error);
    } finally {
      // A conflict leaves the candidate pending; either way show the host's state.
      await onDecided();
      setBusy(false);
    }
  }

  async function publish(): Promise<void> {
    if (!publisher) return;
    setBusy(true);
    try {
      const result = await publisher.execute({ candidate, title }) as { url?: unknown } | undefined;
      if (typeof result?.url === "string" && /^https?:\/\//.test(result.url)) setPublishedURL(result.url);
    } catch (error) {
      showErrorToast(error);
    } finally {
      setBusy(false);
    }
  }

  const files = candidate.changed_files;
  const visibleFiles = showAllFiles ? files : files.slice(0, VISIBLE_FILE_COUNT);
  return <article className="project-candidate-card" aria-busy={busy || undefined}>
    <div className="project-candidate-card-inner">
      <header className="project-candidate-header">
        <FileDiff className="icon" aria-hidden="true" />
        <strong>{t("projects.candidate.title")}</strong>
        <span>{t(files.length === 1 ? "environment.fileCountOne" : "environment.fileCount", { count: formatNumber(files.length) })}</span>
        {candidate.disposition ? <span className="project-candidate-disposition" role="status">
          {t(candidate.disposition === "applied" ? "projects.candidate.applied" : "projects.candidate.discarded")}
        </span> : null}
      </header>
      <ul className="project-candidate-files">
        {visibleFiles.map(path => <li key={path} title={path}>{path}</li>)}
      </ul>
      {files.length > visibleFiles.length ? <button type="button" className="project-candidate-link" onClick={() => setShowAllFiles(true)}>
        {t("turnEdits.moreFiles", { count: formatNumber(files.length - visibleFiles.length) })}
      </button> : null}
      <button type="button" className="project-candidate-link" aria-expanded={diffOpen} onClick={() => void toggleDiff()}>
        {t(diffOpen ? "projects.candidate.hideDiff" : "projects.candidate.viewDiff")}
      </button>
      {diffOpen ? <div className="project-candidate-diff">
        {diff === undefined ? <p className="project-candidate-note">{t("workspaceReview.readingDiff")}</p>
          : diff ? <GitPatchLines patch={diff} label={t("projects.candidate.title")} />
            : <p className="project-candidate-note">{t("workspaceReview.noTextDiff")}</p>}
      </div> : null}
      {publishedURL || !candidate.disposition ? <div className="project-candidate-footer">
        {publishedURL ? <a className="project-candidate-link" href={publishedURL} target="_blank" rel="noreferrer">
          {t("projects.candidate.viewPR")}<ArrowUpRight aria-hidden="true" />
        </a> : null}
        {!candidate.disposition ? <div className="project-candidate-actions">
          {publishers.length > 1 ? <SelectMenu ariaLabel={t("projects.candidate.publisher")}
            value={publisher ? `${publisher.pluginId}:${publisher.id}` : ""} onChange={setPublisherID}
            options={publishers.map(command => ({ value: `${command.pluginId}:${command.id}`, label: command.title }))} flip /> : null}
          <button type="button" className="settings-button settings-button-ghost" disabled={busy} onClick={() => void decide("discard")}>
            {t("projects.candidate.discard")}
          </button>
          {publisher ? <button type="button" className="settings-button" disabled={busy} title={publisher.title} onClick={() => void publish()}>
            {t("projects.candidate.openPR")}
          </button> : null}
          <button type="button" className="settings-button settings-button-primary" disabled={busy} onClick={() => void decide("apply")}>
            {t("projects.candidate.apply")}
          </button>
        </div> : null}
      </div> : null}
    </div>
  </article>;
}
