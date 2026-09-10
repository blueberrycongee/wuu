import { useEffect, useRef, useState, type ComponentProps } from "react";
import { ArrowLeft, ChevronDown, Folder, MessageSquarePlus, MoreHorizontal, Search } from "lucide-react";
import type { AppSidebar } from "./AppSidebar";
import { SCRATCH_PSEUDO_PROJECT_ID, isThreadExecuting, isThreadRunning, isThreadUnread } from "./AppState";
import { baseThreadTitle } from "./ThreadTitles";
import type { NavigationSourceNode } from "./plugins/NavigationPresentation";
import { useI18n } from "./i18n";
import "./styles/mobile-sidebar.css";

type Props = Pick<ComponentProps<typeof AppSidebar>,
  "state" | "sidebarProjects" | "activeThreadID" | "pendingThreadID" |
  "projectThreadsByProjectID" | "loadingProjectThreadIDs" | "expandedSidebarSectionIDs" |
  "onToggleSidebarSectionCollapsed" | "onStartNewThreadForProject" | "onSelectProjectThread" |
  "onTogglePinned" | "onArchiveThread" | "onRenameThread" | "onDeleteThread" |
  "onRemoveProject" | "onRelocateProject" | "onSelectProjectWorkspace" |
  "onCreateProject" | "onOpenProjectFolder" |
  "groupChatEnabled" | "onSwitchToCollaboration" | "onNavigateAway"
> & {
  visible: boolean;
  commands: readonly NavigationSourceNode[];
};

export function MobileSidebar(props: Props): JSX.Element {
  const { t } = useI18n();
  const activeProject = props.sidebarProjects.find((project) =>
    props.projectThreadsByProjectID[project.id]?.some((thread) => thread.id === props.activeThreadID),
  )?.id ?? props.state.activeProjectId ?? SCRATCH_PSEUDO_PROJECT_ID;
  const [projectID, setProjectID] = useState(activeProject);
  const [page, setPage] = useState<"threads" | "projects" | "more">("threads");
  const [filter, setFilter] = useState<"all" | "pinned" | "attention">("all");
  const [actionsID, setActionsID] = useState<string>();
  const [renameTitle, setRenameTitle] = useState<string>();
  const heading = useRef<HTMLButtonElement>(null);
  const wasVisible = useRef(false);
  const project = props.sidebarProjects.find((item) => item.id === projectID)
    ?? props.sidebarProjects.find((item) => item.id === SCRATCH_PSEUDO_PROJECT_ID);
  const selectedID = project?.id ?? SCRATCH_PSEUDO_PROJECT_ID;

  useEffect(() => {
    if (props.visible && !wasVisible.current) {
      setProjectID(activeProject);
      setPage("threads");
      setActionsID(undefined);
      setRenameTitle(undefined);
      setFilter("all");
    }
    wasVisible.current = props.visible;
  }, [props.visible, activeProject]);

  useEffect(() => {
    if (props.visible && !props.expandedSidebarSectionIDs.has(selectedID)) {
      props.onToggleSidebarSectionCollapsed(selectedID);
    }
  }, [props.visible, selectedID, props.expandedSidebarSectionIDs, props.onToggleSidebarSectionCollapsed]);

  useEffect(() => {
    if (!props.visible) return;
    const back = (event: Event) => {
      if (!actionsID && page === "threads") return;
      event.preventDefault();
      event.stopImmediatePropagation();
      setActionsID(undefined);
      setRenameTitle(undefined);
      setPage("threads");
      heading.current?.focus();
    };
    window.addEventListener("wuu:workbench-back", back, true);
    return () => window.removeEventListener("wuu:workbench-back", back, true);
  }, [props.visible, page, actionsID]);

  const threads = props.projectThreadsByProjectID[selectedID] ?? [];
  const visibleThreads = threads.filter((thread) => filter === "pinned" ? thread.pinned
    : filter === "attention" ? isThreadExecuting(thread) || isThreadUnread(thread, props.state.lastViewedTurnByThreadID[thread.id])
    : true);
  const loading = props.loadingProjectThreadIDs?.has(selectedID);
  const search = props.commands.find((node) => node.id === "command:search-conversations");
  const activateCommand = (node: NavigationSourceNode | undefined) => {
    if (!node?.onActivate || node.disabled) return;
    props.onNavigateAway?.();
    node.onActivate();
  };
  const openPage = (next: typeof page) => {
    setPage(next);
    setActionsID(undefined);
    setRenameTitle(undefined);
  };

  return (
    <aside className="sidebar mobile-sidebar" data-wuu-component="sidebar">
      <div className="sidebar-content">
        <header className="mobile-sidebar-header">
          <button ref={heading} type="button" className="mobile-sidebar-project" onClick={() => openPage(page === "threads" ? "projects" : "threads")}
            aria-label={page === "threads" ? t("sidebar.switchProject") : t("common.back")} aria-expanded={page === "projects"}>
            {page === "threads" ? <Folder /> : <ArrowLeft />}
            <span>{page === "threads" ? project?.name ?? t("sidebar.conversations") : page === "projects" ? t("sidebar.switchProject") : t("sidebar.more")}</span>
            {page === "threads" ? <ChevronDown /> : null}
          </button>
        </header>
        {page === "projects" ? (
          <div className="mobile-sidebar-scroll" aria-label={t("sidebar.switchProject")}>
            {props.sidebarProjects.map((item) => (
              <button key={item.id} type="button" className="mobile-sidebar-choice" aria-current={item.id === selectedID ? "true" : undefined}
                onClick={() => { setProjectID(item.id); setFilter("all"); openPage("threads"); heading.current?.focus(); }}>
                <Folder /><span><strong>{item.name}</strong>{item.path ? <small>{item.path}</small> : null}</span>
              </button>
            ))}
            <button type="button" className="mobile-sidebar-choice" onClick={props.onCreateProject}>{t("sidebar.newBlankProject")}</button>
            <button type="button" className="mobile-sidebar-choice" onClick={props.onOpenProjectFolder}>{t("sidebar.useExistingFolder")}</button>
          </div>
        ) : page === "more" ? (
          <nav className="mobile-sidebar-scroll" aria-label={t("sidebar.mainNavigation")}>
            {project && selectedID !== SCRATCH_PSEUDO_PROJECT_ID ? <section aria-label={project.name}>
              <h3 className="mobile-sidebar-section-title">{project.name}</h3>
              {props.onSelectProjectWorkspace ? <button type="button" className="mobile-sidebar-choice" disabled={project.missing} onClick={() => props.onSelectProjectWorkspace?.(selectedID)}>{t("threadSidebar.openWorkspace", { name: project.name })}</button> : null}
              <button type="button" className="mobile-sidebar-choice" onClick={() => props.onRelocateProject(selectedID)}>{t("threadSidebar.relocate")}</button>
              <button type="button" className="mobile-sidebar-choice" onClick={() => props.onRemoveProject(selectedID)}>{t("threadSidebar.removeWorkspace")}</button>
            </section> : null}
            {props.commands.filter((node) => node.kind === "command" && node.id !== "command:new-conversation" && node.id !== "command:search-conversations").map((node) => (
              <button key={node.id} type="button" className="mobile-sidebar-choice" disabled={node.disabled} aria-current={node.active ? "page" : undefined} onClick={() => activateCommand(node)}>{node.label}</button>
            ))}
            {props.groupChatEnabled ? <button type="button" className="mobile-sidebar-choice" onClick={props.onSwitchToCollaboration}>{t("sidebar.collaboration")}</button> : null}
          </nav>
        ) : (
          <>
            <div className="mobile-sidebar-primary">
              <button type="button" className="mobile-sidebar-new" disabled={!props.state.activeContext || project?.missing} onClick={() => props.onStartNewThreadForProject(selectedID)}><MessageSquarePlus />{t("sidebar.newConversation")}</button>
              <button type="button" aria-label={t("sidebar.searchConversations")} disabled={!search || search.disabled} onClick={() => activateCommand(search)}><Search /></button>
            </div>
            <div className="mobile-sidebar-filters" aria-label={t("sidebar.conversations")}>
              {(["all", "pinned", "attention"] as const).map((value) => <button key={value} type="button" aria-pressed={filter === value} onClick={() => { setFilter(value); setActionsID(undefined); }}>{t(value === "all" ? "sidebar.conversations" : value === "pinned" ? "sidebar.pinned" : "sidebar.attentionConversations")}</button>)}
            </div>
            <div className="mobile-sidebar-scroll" aria-label={t("sidebar.conversations")} aria-busy={loading}>
              {project?.missing ? <p className="mobile-sidebar-empty" role="status">{t("threadSidebar.missingWorkspace")}</p> : null}
              {visibleThreads.map((thread) => {
                const title = baseThreadTitle(thread);
                const running = isThreadExecuting(thread);
                const unread = isThreadUnread(thread, props.state.lastViewedTurnByThreadID[thread.id]);
                return <div className="mobile-sidebar-thread" key={thread.id}>
                  <div className="mobile-sidebar-thread-row">
                    <button type="button" className="mobile-sidebar-thread-main" aria-label={title} aria-current={thread.id === props.activeThreadID ? "page" : undefined} aria-busy={thread.id === props.pendingThreadID}
                      onClick={() => props.onSelectProjectThread(selectedID, thread.id)}>
                      <span>{title}</span>
                      {running || unread ? <small>{t(running ? "sidebar.runningConversations" : "sidebar.unreadConversations")}</small> : null}
                    </button>
                    <button type="button" aria-label={t("sidebar.conversationActions", { title })} aria-expanded={actionsID === thread.id} onClick={() => { setActionsID(actionsID === thread.id ? undefined : thread.id); setRenameTitle(undefined); }}><MoreHorizontal /></button>
                  </div>
                  {actionsID === thread.id ? <div className="mobile-sidebar-thread-actions">
                    <button type="button" onClick={() => { props.onTogglePinned(thread); setActionsID(undefined); }}>{t(thread.pinned ? "sidebar.unpin" : "sidebar.pin")}</button>
                    <button type="button" onClick={() => setRenameTitle(title)}>{t("threadSidebar.rename")}</button>
                    <button type="button" disabled={isThreadRunning(thread)} onClick={() => { props.onArchiveThread(thread); setActionsID(undefined); }}>{t("sidebar.archiveAction")}</button>
                    <button type="button" disabled={isThreadRunning(thread)} onClick={() => {
                      if (!window.confirm(t("threadSidebar.deleteConfirmation"))) return;
                      props.onDeleteThread(thread);
                      setActionsID(undefined);
                    }}>{t("threadSidebar.delete")}</button>
                    {renameTitle !== undefined ? <form className="mobile-sidebar-rename" onSubmit={(event) => {
                      event.preventDefault();
                      if (!renameTitle.trim()) return;
                      props.onRenameThread(thread, renameTitle.trim());
                      setRenameTitle(undefined);
                      setActionsID(undefined);
                    }}>
                      <input aria-label={t("threadSidebar.rename")} value={renameTitle} onChange={(event) => setRenameTitle(event.target.value)} autoFocus />
                      <button type="submit" disabled={!renameTitle.trim()}>{t("common.save")}</button>
                      <button type="button" onClick={() => setRenameTitle(undefined)}>{t("common.cancel")}</button>
                    </form> : null}
                  </div> : null}
                </div>;
              })}
              {!visibleThreads.length ? <p className="mobile-sidebar-empty" role="status">{t(loading ? "common.loadingEllipsis" : filter === "pinned" ? "sidebar.noPinnedConversations" : filter === "attention" ? "sidebar.attentionEmpty" : "sidebar.noConversations")}</p> : null}
            </div>
          </>
        )}
        <footer className="mobile-sidebar-footer"><button type="button" onClick={() => openPage(page === "more" ? "threads" : "more")} aria-expanded={page === "more"}><MoreHorizontal />{t("sidebar.more")}</button></footer>
      </div>
    </aside>
  );
}
