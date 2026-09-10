import { useContext, useEffect, useRef, useState, type ComponentProps } from "react";
import { ArrowLeft, ChevronDown, Check, Folder, FolderPlus, FolderOpen, MessageCircle, SquarePen, Settings2, Monitor } from "lucide-react";
import { PhoneNavigationContext } from './PhoneNavigationContext';
import type { AppSidebar } from "./AppSidebar";
import { SCRATCH_PSEUDO_PROJECT_ID, isThreadExecuting, isThreadRunning, isThreadUnread, threadTime } from "./AppState";
import { baseThreadTitle } from "./ThreadTitles";
import { Modal } from "./Modal";
import { MobileSessionRow } from "./MobileSessionRow";
import type { NavigationSourceNode } from "./plugins/NavigationPresentation";
import { useI18n } from "./i18n";
import "./styles/mobile-sidebar.css";

type Props = Pick<ComponentProps<typeof AppSidebar>,
  "state" | "sidebarProjects" | "activeThreadID" | "pendingThreadID" |
  "projectThreadsByProjectID" | "loadingProjectThreadIDs" | "expandedSidebarSectionIDs" |
  "onToggleSidebarSectionCollapsed" | "onStartNewThreadForProject" | "onSelectProjectThread" |
  "onTogglePinned" | "onArchiveThread" | "onRenameThread" | "onDeleteThread" |
  "onRemoveProject" | "onRelocateProject" | "onSelectProjectWorkspace" |
  "onCreateProject" | "onOpenProjectFolder" | "groupChatEnabled" |
  "onSwitchToCollaboration" | "onNavigateAway"
> & { visible: boolean; commands: readonly NavigationSourceNode[] };

export function MobileSidebar(props: Props): JSX.Element {
  const { t } = useI18n();
  const phoneNavigation = useContext(PhoneNavigationContext);
  const activeProject = props.sidebarProjects.find(project =>
    props.projectThreadsByProjectID[project.id]?.some(thread => thread.id === props.activeThreadID),
  )?.id ?? props.state.activeProjectId ?? SCRATCH_PSEUDO_PROJECT_ID;
  const [projectID, setProjectID] = useState(activeProject);
  const [page, setPage] = useState<"threads" | "projects" | "more">("threads");
  const [actionsID, setActionsID] = useState<string>();
  const [actionPage, setActionPage] = useState<"menu" | "rename" | "delete">("menu");
  const [renameTitle, setRenameTitle] = useState("");
  const heading = useRef<HTMLButtonElement>(null);
  const actionTrigger = useRef<HTMLButtonElement | undefined>(undefined);
  const wasVisible = useRef(false);
  const project = props.sidebarProjects.find(item => item.id === projectID)
    ?? props.sidebarProjects.find(item => item.id === SCRATCH_PSEUDO_PROJECT_ID);
  const selectedID = project?.id ?? SCRATCH_PSEUDO_PROJECT_ID;
  const threads = props.projectThreadsByProjectID[selectedID] ?? [];
  const actionThread = threads.find(thread => thread.id === actionsID);
  const loading = props.loadingProjectThreadIDs?.has(selectedID);
  const byRecency = (left: typeof threads[number], right: typeof threads[number]) =>
    threadTime(right) - threadTime(left);
  const groups = [
    { label: t("sidebar.pinned"), heading: true, threads: threads.filter(thread => thread.pinned).sort(byRecency) },
    { label: t("sidebar.conversations"), heading: false, threads: threads.filter(thread => !thread.pinned).sort(byRecency) },
  ];

  function closeActions() {
    setActionsID(undefined);
    setActionPage("menu");
    actionTrigger.current?.focus({ preventScroll: true });
  }
  function openPage(next: typeof page) {
    const keyboardFocus = document.activeElement?.matches(":focus-visible");
    closeActions();
    setPage(next);
    if (keyboardFocus) heading.current?.focus({ preventScroll: true });
  }
  function activateCommand(node: NavigationSourceNode | undefined) {
    if (!node?.onActivate || node.disabled) return;
    props.onNavigateAway?.();
    node.onActivate();
  }

  useEffect(() => {
    if (props.visible && !wasVisible.current) {
      setProjectID(activeProject);
      setPage("threads");
    }
    if (!props.visible) setActionsID(undefined);
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
      if (actionsID) {
        if (actionPage !== "menu") setActionPage("menu");
        else closeActions();
      } else openPage("threads");
    };
    window.addEventListener("wuu:workbench-back", back, true);
    return () => window.removeEventListener("wuu:workbench-back", back, true);
  }, [props.visible, page, actionsID, actionPage]);

  return <aside className="sidebar mobile-sidebar" data-wuu-component="sidebar">
    <div className="sidebar-content">
      <header className="mobile-sidebar-header">
        <button ref={heading} type="button" className="mobile-sidebar-project"
          aria-label={page === "threads" ? t("sidebar.switchProject") : t("common.back")}
          aria-expanded={page === "projects"}
          onClick={() => openPage(page === "threads" ? "projects" : "threads")}>
          {page !== "threads" ? <ArrowLeft /> : null}
          <span>{page === "threads" ? project?.name ?? t("sidebar.conversations")
            : t(page === "projects" ? "sidebar.switchProject" : "sidebar.more")}</span>
          {page === "threads" ? <ChevronDown /> : null}
        </button>
      </header>

      {page === "threads" ? <>
        <div className="mobile-sidebar-toolbar">
          <button type="button" className="mobile-sidebar-new" disabled={!props.state.activeContext || project?.missing}
            onClick={() => props.onStartNewThreadForProject(selectedID)}>
            <SquarePen />{t("sidebar.newConversation")}
          </button>
        </div>
        <div className="mobile-sidebar-scroll" aria-label={t("sidebar.conversations")} aria-busy={loading}>
          {project?.missing ? <p className="mobile-sidebar-empty" role="status">{t("threadSidebar.missingWorkspace")}</p> : null}
          {groups.filter(group => group.threads.length > 0).map(group => <section
            key={group.label} className="mobile-sidebar-group" aria-label={group.label}>
            {group.heading ? <h3>{group.label}</h3> : null}
            {group.threads.map(thread => {
              const running = isThreadExecuting(thread);
              return <MobileSessionRow key={thread.id}
                enabled={props.visible}
                title={baseThreadTitle(thread)}
                active={thread.id === props.activeThreadID}
                pending={thread.id === props.pendingThreadID}
                running={running}
                unread={isThreadUnread(thread, props.state.lastViewedTurnByThreadID[thread.id])}
                statusLabel={t(running ? "sidebar.runningConversations" : "sidebar.unreadConversations")}
                onSelect={() => props.onSelectProjectThread(selectedID, thread.id)}
                onActions={button => {
                  actionTrigger.current = button;
                  setActionsID(thread.id);
                  setActionPage("menu");
                  setRenameTitle(baseThreadTitle(thread));
                }}
              />;
            })}
          </section>)}
          {!threads.length ? <p className="mobile-sidebar-empty" role="status">
            {t(loading ? "common.loadingEllipsis" : "sidebar.noConversations")}
          </p> : null}
        </div>
      </> : page === "projects" ? <div className="mobile-sidebar-scroll" aria-label={t("sidebar.switchProject")}>
        {props.sidebarProjects.map(item => <button key={item.id} type="button" className="mobile-sidebar-choice mobile-sidebar-project-choice"
          aria-current={item.id === selectedID ? "true" : undefined}
          onClick={() => { setProjectID(item.id); openPage("threads"); }}>
          {item.id === SCRATCH_PSEUDO_PROJECT_ID ? <MessageCircle /> : <Folder />}
          <span><strong>{item.name}</strong>{item.path ? <small title={item.path}>{item.path}</small> : null}</span>
          {item.id === selectedID ? <Check /> : null}
        </button>)}
        <div className="mobile-sidebar-secondary">
          <button type="button" className="mobile-sidebar-choice" onClick={props.onCreateProject}><FolderPlus />{t("sidebar.newBlankProject")}</button>
          <button type="button" className="mobile-sidebar-choice" onClick={props.onOpenProjectFolder}><FolderOpen />{t("sidebar.useExistingFolder")}</button>
        </div>
      </div> : <nav className="mobile-sidebar-scroll" aria-label={t("sidebar.mainNavigation")}>
        {props.commands.filter(node => node.kind === "command" && node.id !== "command:new-conversation"
          && node.id !== "command:search-conversations").map(node =>
          <button key={node.id} type="button" className="mobile-sidebar-choice" disabled={node.disabled}
            aria-current={node.active ? "page" : undefined} onClick={() => activateCommand(node)}>{node.label}</button>)}
        {props.groupChatEnabled ? <button type="button" className="mobile-sidebar-choice" onClick={props.onSwitchToCollaboration}>{t("sidebar.collaboration")}</button> : null}
        {project && selectedID !== SCRATCH_PSEUDO_PROJECT_ID ? <section className="mobile-sidebar-secondary" aria-label={project.name}>
          <h3>{project.name}</h3>
          {props.onSelectProjectWorkspace ? <button type="button" className="mobile-sidebar-choice" disabled={project.missing}
            onClick={() => props.onSelectProjectWorkspace?.(selectedID)}>{t("threadSidebar.openWorkspace", { name: project.name })}</button> : null}
          <button type="button" className="mobile-sidebar-choice" onClick={() => props.onRelocateProject(selectedID)}>{t("threadSidebar.relocate")}</button>
          <button type="button" className="mobile-sidebar-choice" onClick={() => props.onRemoveProject(selectedID)}>{t("threadSidebar.removeWorkspace")}</button>
        </section> : null}
      </nav>}

      <footer className="mobile-sidebar-footer">
        {phoneNavigation && <button type="button" className="mobile-sidebar-device" onClick={phoneNavigation.openDevices}>
          <Monitor aria-hidden="true" /><span><strong>{t('account.computersAndAccount')}</strong><small>{phoneNavigation.computer || t('account.computer')}</small></span>
        </button>}
        <button type="button" onClick={() => openPage(page === "more" ? "threads" : "more")} aria-expanded={page === "more"}>
          <Settings2 />{t("sidebar.more")}
        </button>
      </footer>
    </div>

    {props.visible && actionThread ? <Modal
      ariaLabel={t("sidebar.conversationActions", { title: baseThreadTitle(actionThread) })}
      title={baseThreadTitle(actionThread)} panelClassName="mobile-session-sheet"
      onClose={closeActions} asForm={actionPage === "rename"}
      onSubmit={() => {
        if (!renameTitle.trim()) return;
        props.onRenameThread(actionThread, renameTitle.trim());
        closeActions();
      }}>
      {actionPage === "rename" ? <>
        <label>{t("threadSidebar.rename")}<input autoFocus value={renameTitle} onChange={event => setRenameTitle(event.target.value)} /></label>
        <button type="submit" disabled={!renameTitle.trim()}>{t("common.save")}</button>
      </> : actionPage === "delete" ? <>
        <p>{t("threadSidebar.deleteConfirmation")}</p>
        <button type="button" className="danger" disabled={isThreadRunning(actionThread)}
          onClick={() => { props.onDeleteThread(actionThread); closeActions(); }}>{t("threadSidebar.delete")}</button>
        <button type="button" onClick={() => setActionPage("menu")}>{t("common.cancel")}</button>
      </> : <>
        <button type="button" onClick={() => { props.onTogglePinned(actionThread); closeActions(); }}>{t(actionThread.pinned ? "sidebar.unpin" : "sidebar.pin")}</button>
        <button type="button" onClick={() => setActionPage("rename")}>{t("threadSidebar.rename")}</button>
        <button type="button" disabled={isThreadRunning(actionThread)} onClick={() => { props.onArchiveThread(actionThread); closeActions(); }}>{t("sidebar.archiveAction")}</button>
        <button type="button" className="danger" disabled={isThreadRunning(actionThread)} onClick={() => setActionPage("delete")}>{t("threadSidebar.delete")}</button>
      </>}
    </Modal> : null}
  </aside>;
}
