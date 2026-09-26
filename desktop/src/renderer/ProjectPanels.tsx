import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import type { ProjectCandidate } from "../shared/protocol";
import { isThreadExecuting } from "./AppState";
import { useProjectActions, useProjectCandidates, type ProjectThread } from "./ProjectActions";
import { projectSessionsOf } from "./ProjectSessions";
import { candidateStatusKey } from "./ProjectViews";
import { SelectMenu } from "./SelectMenu";
import { baseThreadTitle } from "./ThreadTitles";
import { GitPatchLines } from "./WorkspaceReviewPanels";
import { ArrowUpRight, FileDiff, LoaderCircle } from "./WuuIcons";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

// Desktop plugins register publishers for this context; they receive
// { candidate, title } and may return { url }.
const PUBLISH_CONTEXT = "project-candidate.publish";

/** The newest proposal a session still holds, or its newest decided one. */
function liveCandidate(candidates: readonly ProjectCandidate[]): ProjectCandidate | undefined {
  return candidates.find((candidate) => candidate.disposition !== "superseded") ?? candidates[0];
}

/** A project's work at a glance: what awaits review, then every session. */
export function ProjectPanel({ projectID }: { projectID: string }): JSX.Element | null {
  const { t, formatNumber } = useI18n();
  const actions = useProjectActions();
  const project = actions?.threads.find((thread) => thread.id === projectID);
  const sessions = actions ? projectSessionsOf(projectID, actions.threads) : [];
  const refreshKey = `${project?.pending_candidates ?? 0}:${sessions.map((session) => session.latest_completed_turn_id ?? "").join(",")}`;
  const { candidates } = useProjectCandidates({ project_id: projectID }, refreshKey);
  if (!actions || !project) return null;
  const pending = sessions.flatMap((session) => {
    const candidate = candidates.find((item) => item.session_id === session.id && !item.disposition);
    return candidate ? [{ session, candidate }] : [];
  });
  return (
    <section className="project-panel" aria-label={baseThreadTitle(project)}>
      <header className="project-panel-header">
        <h2>{baseThreadTitle(project)}</h2>
        <button type="button" className="project-inline-link" onClick={() => actions.openThread(project.id)}>
          {t("projects.openCoordinator")}
        </button>
      </header>
      {pending.length ? <>
        <h3 className="project-panel-heading">{t("projects.panel.review")}</h3>
        <ul className="project-panel-list">
          {pending.map(({ session, candidate }) => (
            <li key={session.id} className="project-panel-row">
              <FileDiff className="project-panel-row-icon" aria-hidden="true" />
              <span className="project-panel-row-title">{baseThreadTitle(session)}</span>
              <span className="project-panel-row-meta">
                {t(candidate.changed_files.length === 1 ? "environment.fileCountOne" : "environment.fileCount", {
                  count: formatNumber(candidate.changed_files.length),
                })}
              </span>
              <button type="button" className="settings-button" onClick={() => actions.openProposal(session)}>
                {t("projects.review")}
              </button>
            </li>
          ))}
        </ul>
      </> : null}
      {sessions.length ? ([
        { label: t("projects.role.side"), members: sessions.filter((session) => session.project_role === "side") },
        { label: t("projects.role.workers"), members: sessions.filter((session) => session.project_role !== "side") },
      ].filter((group) => group.members.length > 0).map((group) => (
        <div key={group.label}>
          <h3 className="project-panel-heading">{group.label}</h3>
          <ul className="project-panel-list">
            {group.members.map((session) => <ProjectSessionRow key={session.id} session={session} />)}
          </ul>
        </div>
      ))) : <p className="project-panel-empty">{t("projects.panel.noSessions")}</p>}
    </section>
  );
}

function ProjectSessionRow({ session }: { session: ProjectThread }): JSX.Element | null {
  const { t } = useI18n();
  const actions = useProjectActions();
  if (!actions) return null;
  const running = isThreadExecuting(session);
  const control = session.session_control;
  const managed = control?.state === "active";
  return (
    <li className="project-panel-row">
      <span className="project-panel-row-icon">
        {running ? <LoaderCircle className="project-status-spinner" role="img" aria-label={t("projects.running")} /> : null}
      </span>
      <button type="button" className="project-panel-row-main" onClick={() => actions.openThread(session.id)}>
        <span className="project-panel-row-title">{baseThreadTitle(session)}</span>
      </button>
      {/* Managed is every session's default; only a session out of the coordinator's hands says so. */}
      {control && !managed ? (
        <span className="project-panel-row-meta project-panel-row-state">
          {t(control.state === "taken_over" ? "sessionControl.takenOver" : "sessionControl.paused")}
        </span>
      ) : null}
      {control ? (
        <button
          type="button"
          className="settings-button settings-button-ghost project-panel-row-action"
          title={managed ? t("projects.takeOverHint") : undefined}
          onClick={() => managed ? actions.takeOver(session) : actions.returnToProject(session)}
        >
          {t(managed ? "projects.takeOver" : "projects.returnToProject")}
        </button>
      ) : null}
      <button type="button" className="settings-button settings-button-ghost project-panel-row-action" onClick={() => actions.release(session)}>
        {t("projects.release")}
      </button>
    </li>
  );
}

/**
 * One session's proposal beside the conversation: every change the session
 * has not delivered yet, its diff, and the decision.
 */
export function ProposalPanel({ sessionID }: { sessionID: string }): JSX.Element | null {
  const { t, formatNumber } = useI18n();
  const actions = useProjectActions();
  const session = actions?.threads.find((thread) => thread.id === sessionID);
  const refreshKey = `${session?.pending_candidates ?? 0}:${session?.latest_completed_turn_id ?? ""}`;
  const { candidates, reload } = useProjectCandidates({ session_id: sessionID }, refreshKey);
  const candidate = liveCandidate(candidates);
  const candidateKey = candidate ? `${candidate.session_id}:${candidate.turn_id}` : "";
  const [diff, setDiff] = useState<{ key: string; patch: string }>();
  const [busy, setBusy] = useState(false);
  const subscribe = useCallback((listener: () => void) => desktopPluginHost.subscribe(listener), []);
  const getCommands = useCallback(() => desktopPluginHost.getCommands(), []);
  const publishers = useSyncExternalStore(subscribe, getCommands, getCommands)
    .filter((command) => command.contexts?.includes(PUBLISH_CONTEXT));
  const [publisherID, setPublisherID] = useState("");
  const publisher = publishers.find((command) => `${command.pluginId}:${command.id}` === publisherID) ?? publishers[0];

  useEffect(() => {
    if (!candidate || !window.wuu.projectCandidate) return;
    let current = true;
    window.wuu.projectCandidate({ action: "get", session_id: candidate.session_id, turn_id: candidate.turn_id })
      .then((result) => { if (current) setDiff({ key: candidateKey, patch: result.candidate?.diff ?? "" }); })
      .catch((error) => { if (current) showErrorToast(error); });
    return () => { current = false; };
  }, [candidateKey]);

  if (!actions || !session) return null;
  const title = baseThreadTitle(session);

  async function decide(request: () => Promise<unknown>): Promise<void> {
    setBusy(true);
    try {
      await request();
    } catch (error) {
      showErrorToast(error);
    } finally {
      // A conflict leaves the proposal pending; either way show the host's state.
      await reload();
      setBusy(false);
    }
  }

  function decideCandidate(action: "apply" | "discard"): void {
    if (!candidate || !window.wuu.projectCandidate) return;
    void decide(() => window.wuu.projectCandidate!({ action, session_id: candidate.session_id, turn_id: candidate.turn_id }));
  }

  function publish(): void {
    if (!candidate || !publisher || !window.wuu.projectCandidate) return;
    void decide(async () => {
      const result = await publisher.execute({ candidate, title }) as { url?: unknown } | undefined;
      if (typeof result?.url !== "string" || !/^https?:\/\//.test(result.url)) {
        throw new Error(t("projects.candidate.publishNoURL"));
      }
      await window.wuu.projectCandidate!({ action: "publish", session_id: candidate.session_id, turn_id: candidate.turn_id, url: result.url });
    });
  }

  const files = candidate?.changed_files ?? [];
  const patch = diff?.key === candidateKey ? diff.patch : undefined;
  return (
    <section className="project-proposal" aria-busy={busy || undefined} aria-label={t("projects.candidate.title")}>
      <header className="project-panel-header">
        <h2>{title}</h2>
        <button type="button" className="project-inline-link" onClick={() => actions.openThread(session.id)}>
          {t("projects.openSession")}
        </button>
      </header>
      {!candidate ? <p className="project-panel-empty">{t("projects.candidate.none")}</p> : (
        <>
          <p className="project-proposal-status" data-disposition={candidate.disposition || "pending"}>
            {[
              t(files.length === 1 ? "environment.fileCountOne" : "environment.fileCount", { count: formatNumber(files.length) }),
              t(candidateStatusKey(candidate)),
            ].join(" · ")}
            {candidate.url ? (
              <a className="project-inline-link" href={candidate.url} target="_blank" rel="noreferrer">
                {t("projects.candidate.viewPR")}<ArrowUpRight aria-hidden="true" />
              </a>
            ) : null}
          </p>
          <ul className="project-proposal-files">
            {files.map((path) => <li key={path} title={path}>{path}</li>)}
          </ul>
          <div className="project-proposal-diff workspace-diff-code-scroll">
            {patch === undefined ? <p className="project-panel-empty">{t("workspaceReview.readingDiff")}</p>
              : patch ? <GitPatchLines patch={patch} label={t("projects.candidate.title")} />
                : <p className="project-panel-empty">{t("workspaceReview.noTextDiff")}</p>}
          </div>
          {!candidate.disposition ? (
            <footer className="project-proposal-actions">
              {publishers.length > 1 ? <SelectMenu ariaLabel={t("projects.candidate.publisher")}
                value={publisher ? `${publisher.pluginId}:${publisher.id}` : ""} onChange={setPublisherID}
                options={publishers.map((command) => ({ value: `${command.pluginId}:${command.id}`, label: command.title }))} flip /> : null}
              <button type="button" className="settings-button settings-button-ghost" disabled={busy}
                title={t("projects.candidate.rejectHint")} onClick={() => decideCandidate("discard")}>
                {t("projects.candidate.discard")}
              </button>
              {publisher ? (
                <button type="button" className="settings-button" disabled={busy} title={publisher.title} onClick={publish}>
                  {t("projects.candidate.openPR")}
                </button>
              ) : null}
              <button type="button" className="settings-button settings-button-primary" disabled={busy} onClick={() => decideCandidate("apply")}>
                {t("projects.candidate.apply")}
              </button>
            </footer>
          ) : null}
        </>
      )}
    </section>
  );
}
