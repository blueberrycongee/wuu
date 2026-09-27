import { isThreadExecuting } from "./AppState";
import { useProjectActions, type ProjectThread } from "./ProjectActions";
import { projectSessionsOf } from "./ProjectSessions";
import { baseThreadTitle } from "./ThreadTitles";
import { LoaderCircle, LogOut, MessagesSquare } from "./WuuIcons";
import { useI18n } from "./i18n";

/** A project's sessions at a glance, the Side Agent first. */
export function ProjectPanel({ projectID }: { projectID: string }): JSX.Element | null {
  const { t } = useI18n();
  const actions = useProjectActions();
  const project = actions?.threads.find((thread) => thread.id === projectID);
  if (!actions || !project) return null;
  const sessions = projectSessionsOf(projectID, actions.threads);
  return (
    <section className="project-panel" aria-label={baseThreadTitle(project)}>
      <header className="project-panel-header">
        <h2 title={baseThreadTitle(project)}>{baseThreadTitle(project)}</h2>
        <button type="button" className="icon-button project-icon-button"
          title={t("projects.openCoordinator")} aria-label={t("projects.openCoordinator")}
          onClick={() => actions.openThread(project.id)}>
          <MessagesSquare aria-hidden="true" />
        </button>
      </header>
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
  return (
    <li className="project-panel-row">
      <button type="button" className="project-panel-row-main" onClick={() => actions.openThread(session.id)}>
        <span className="project-panel-row-title" title={baseThreadTitle(session)}>{baseThreadTitle(session)}</span>
      </button>
      <div className="project-panel-row-tools">
        {running ? (
          <span className="project-panel-row-state">
            <LoaderCircle className="project-status-spinner" role="img" aria-label={t("projects.running")} />
          </span>
        ) : null}
        <div className="project-panel-row-actions">
          <button type="button" className="icon-button project-icon-button project-panel-row-action"
            title={t("projects.release")} aria-label={t("projects.release")} onClick={() => actions.release(session)}>
            <LogOut aria-hidden="true" />
          </button>
        </div>
      </div>
    </li>
  );
}
