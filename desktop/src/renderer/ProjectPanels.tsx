import type { ProjectWork } from "../shared/protocol";
import { useProjectWork } from "./ProjectWork";
import { isThreadExecuting } from "./AppState";
import { useProjectActions, type ProjectThread } from "./ProjectActions";
import { projectSessionsOf } from "./ProjectSessions";
import { baseThreadTitle } from "./ThreadTitles";
import { LoaderCircle, LogOut, MessagesSquare } from "./WuuIcons";
import { useI18n } from "./i18n";

/** Durable outcomes first; session details remain available below each work. */
export function ProjectPanel({ projectID }: { projectID: string }): JSX.Element | null {
  const { t } = useI18n();
  const actions = useProjectActions();
  const work = useProjectWork(projectID);
  const project = actions?.threads.find((thread) => thread.id === projectID);
  if (!actions || !project) return null;
  const managed = new Set(work.snapshot?.works.flatMap(item => [item.lead_id, item.executor_id]) ?? []);
  const sessions = projectSessionsOf(projectID, actions.threads).filter(item => !managed.has(item.id));
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
      {work.error ? <p role="alert" className="project-work-error">{work.error}</p> : null}
      {work.snapshot?.works.map(item => <ProjectWorkRow key={item.id} work={item} busy={work.busy === item.id} control={work.control} />)}
      {work.snapshot && work.snapshot.usage.length > 0 ? <details className="project-work-usage">
        <summary>{t("projects.work.usage")}</summary>
        {work.snapshot.usage.map((item) => <p key={`${item.role}:${item.provider}:${item.model}`}>
          {item.role} · {item.provider}/{item.model}<br />
          {t("projects.work.tokens", { input: String(item.input_tokens), output: String(item.output_tokens), cached: String(item.cache_read_tokens) })}
        </p>)}
      </details> : null}
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
      ))) : work.snapshot?.works.length ? null : <p className="project-panel-empty">{t("projects.panel.noSessions")}</p>}
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

function ProjectWorkRow({ work, busy, control }: {
  work: ProjectWork; busy: boolean; control: (work: ProjectWork, operation: "stop" | "resume") => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const actions = useProjectActions();
  const receipt = work.phase === "executing" || work.phase === "verifying" ? work.executor_dispatch : work.lead_dispatch;
  const resume = work.phase === "stopped" || work.phase === "blocked";
  return <article className="project-work" data-work-id={work.id} data-work-phase={work.phase}>
    <div className="project-work-heading">
      <button type="button" className="project-panel-row-main" onClick={() => actions?.openThread(work.lead_id)}>
        <span>{work.title}</span>
      </button>
      {work.phase !== "delivered" ? <button type="button" className="settings-button settings-button-ghost" disabled={busy}
        data-work-action={resume ? "resume" : "stop"} onClick={() => void control(work, resume ? "resume" : "stop")}>
        {t(resume ? "projects.work.resume" : "projects.work.stop")}
      </button> : null}
    </div>
    <p className="project-work-state">{t(`projects.work.phase.${work.phase}`)} · {t("projects.work.revision", { revision: String(work.revision) })}</p>
    {receipt?.delivery_state && !["accepted", "delivered", "stopped", "blocked"].includes(work.phase) ? <p className="project-work-receipt">{t(`projects.work.receipt.${receipt.delivery_state}`)}</p> : null}
    {work.blocker ? <p className="project-work-blocker">{work.blocker}</p> : null}
    {work.summary ? <p>{work.summary}</p> : null}
    {work.delivery ? <p>{work.delivery}</p> : null}
    <details>
      <summary>{t("projects.work.details")}</summary>
      <dl>
        {([
          ["projects.work.requirements", work.brief], ["projects.work.acceptance", work.acceptance],
          ["projects.work.authority", work.authority], ["projects.work.evidence", work.evidence],
          ["projects.work.review", work.review], ["projects.work.version", work.code_ref],
        ] as const).map(([label, value]) => value ? <div key={label}><dt>{t(label)}</dt><dd>{value}</dd></div> : null)}
      </dl>
      <div className="project-work-members">
        <button type="button" className="settings-button settings-button-ghost" onClick={() => actions?.openThread(work.lead_id)}>{t("projects.role.technicalLead")} · {actions?.threads.find(item => item.id === work.lead_id)?.preview || work.title}</button>
        {work.executor_id ? <button type="button" className="settings-button settings-button-ghost" onClick={() => actions?.openThread(work.executor_id!)}>{t("projects.role.executor")}</button> : null}
      </div>
    </details>
  </article>;
}
