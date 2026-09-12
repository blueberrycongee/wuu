import { ChevronRight } from "lucide-react";
import {
  createContext,
  type CSSProperties,
  type HTMLAttributes,
  type ReactNode,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { SectionRowIcon } from "./ThreadSidebar";
import { motionDurationMs } from "./motion";

// Mirrors --section-fold-ms in sidebar.css (= var(--motion-slow)). The body
// stays mounted while collapsing so the exit motion can finish before React
// removes the rows.
export const SECTION_COLLAPSE_MS = motionDurationMs("--motion-slow", 280);

type CollapsePhase = "open" | "opening" | "closing";

/**
 * Shared measured-height unfurl used by workspace rows and the functional
 * group headings (插件 / 置顶 / 工作区 / 文件夹). Opening writes 0px →
 * content height, then releases to auto; closing snapshots height and
 * animates to 0 before unmount.
 */
export function SidebarCollapseBody({
  expanded,
  children,
  className,
}: {
  expanded: boolean;
  children?: ReactNode;
  className?: string;
}): JSX.Element | null {
  const [mounted, setMounted] = useState(expanded);
  const [phase, setPhase] = useState<CollapsePhase>(expanded ? "open" : "closing");
  const collapseRef = useRef<HTMLDivElement | null>(null);
  const [bodyHeight, setBodyHeight] = useState<number | null>(expanded ? null : 0);
  const prevExpandedRef = useRef(expanded);
  useEffect(() => {
    const wasExpanded = prevExpandedRef.current;
    prevExpandedRef.current = expanded;
    if (expanded) {
      if (!wasExpanded) {
        setBodyHeight(0);
        setPhase("opening");
        setMounted(true);
      }
      return;
    }
    if (!wasExpanded) {
      return;
    }
    const currentHeight = collapseRef.current?.scrollHeight ?? 0;
    setBodyHeight(currentHeight);
    setPhase("closing");
    const frame = window.requestAnimationFrame(() => {
      setBodyHeight(0);
    });
    const timer = window.setTimeout(() => {
      setMounted(false);
    }, SECTION_COLLAPSE_MS);
    return () => {
      window.cancelAnimationFrame(frame);
      window.clearTimeout(timer);
    };
  }, [expanded]);

  useLayoutEffect(() => {
    if (!expanded || !mounted) return;
    if (phase !== "opening") return;
    const element = collapseRef.current;
    if (!element) return;
    const measuredHeight = element.scrollHeight;
    setBodyHeight(measuredHeight);
    const frame = window.requestAnimationFrame(() => {
      setBodyHeight(measuredHeight);
    });
    const timer = window.setTimeout(() => {
      setPhase("open");
      setBodyHeight(null);
    }, SECTION_COLLAPSE_MS);
    return () => {
      window.cancelAnimationFrame(frame);
      window.clearTimeout(timer);
    };
  }, [expanded, mounted, phase]);

  useEffect(() => {
    if (!expanded || phase !== "open") return;
    const element = collapseRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      setBodyHeight(null);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [expanded, phase]);

  if (!mounted || !children) return null;

  const collapseStyle =
    bodyHeight === null
      ? undefined
      : ({
          "--sidebar-section-body-height": `${Math.max(0, bodyHeight)}px`,
        } as CSSProperties);
  const collapseClassName = ["thread-list-collapse", className].filter(Boolean).join(" ");
  return (
    <div
      ref={collapseRef}
      className={collapseClassName}
      data-state={phase}
      style={collapseStyle}
      aria-hidden={phase === "closing" || undefined}
    >
      <div className="thread-list-collapse-inner">{children}</div>
    </div>
  );
}

/**
 * dnd-kit activator context shared between SortableSection (AppSidebar)
 * and SidebarSection. The default value is `null`, so callsites that
 * are NOT inside a SortableSection read null and render the header as a
 * plain toggle. Pinned folders and workspaces reuse the same sortable
 * section contract as their source groups.
 */
export type SidebarSectionDragHandle = {
  dragHandleProps: HTMLAttributes<HTMLButtonElement>;
  isDragging: boolean;
};

export const SidebarSectionDragHandleContext = createContext<SidebarSectionDragHandle | null>(
  null,
);

export function useSidebarSectionDragHandle(): SidebarSectionDragHandle | null {
  return useContext(SidebarSectionDragHandleContext);
}

/**
 * Shared section header + collapse-body component.
 *
 * One component renders the four section types in the left sidebar:
 * 置顶 / Agents / 对话 scratch / project. They all share the same visual
 * anatomy (icon + label + chevron + collapse body) and the same
 * expand/collapse unfurl animation. Wiring them through one component
 * keeps icon size, spacing, and motion unified across the panel —
 * the parallel look-alike markup that the previous restructure
 * shipped led to subtle size and spacing drift between the four.
 *
 * Differences between sections surface as props rather than as
 * duplicated markup:
 *   - iconKind + CollapsedIcon / ExpandedIcon pair (pinned uses Pin /
 *     Pin with a CSS rotate; agents uses UserRound / UsersRound; 对话
 *     uses MessageSquare / MessagesSquare; project uses Folder /
 *     FolderOpen).
 *   - Optional `loading` / `running` / `unread` indicators in the right-hand
 *     grid track of the header. Running wins visually over unread so a folded
 *     section keeps exposing live work without hiding the individual unread
 *     rows when it is opened.
 *   - Optional `actions` slot, rendered as a sibling of the header
 *     button so they stay OUTSIDE the <button>. The Agents section
 *     uses this to host its ＋ (new agent) and … (overflow) controls
 *     while keeping the toggle button a leaf DOM node (c4a3a50d landed
 *     this fix).
 *   - Optional `newItemButton` slot, rendered as a sibling `<button>`
 *     for the project-only "+ new conversation" hover affordance.
 *   - Optional `ariaLabel` / `title` overrides so the section can
 *     describe its toggle verb ("展开 / 收起 对话") and surface unread
 *     state in the label.
 *   - `emptyNote`: shown in the body when no `children` are mounted
 *     so the height collapse animation has real content.
 *
 * The close animation is self-contained: SidebarCollapseBody keeps the
 * rows mounted in `data-state="closing"` for SECTION_COLLAPSE_MS after
 * `expanded` flips false, so all four sections share the identical
 * collapse motion.
 */
export function SidebarSection({
  expanded,
  iconKind,
  CollapsedIcon,
  ExpandedIcon,
  label,
  ariaLabel,
  title,
  active,
  pending,
  running,
  unread,
  loading,
  actions,
  newItemButton,
  emptyNote,
  children,
  onToggle,
  onContextMenu,
}: {
  expanded: boolean;
  iconKind: string;
  CollapsedIcon: React.ComponentType<{ className?: string }>;
  ExpandedIcon: React.ComponentType<{ className?: string }>;
  label: ReactNode;
  ariaLabel: string;
  title: string;
  active?: boolean;
  pending?: boolean;
  running?: boolean;
  unread?: boolean;
  loading?: boolean;
  // Rendered as a sibling of the header <button> so it can stay outside
  // the toggle's interactive area (React 18 rejects nested <button>s
  // and the click semantics get muddied if the actions live inside).
  actions?: ReactNode;
  // Project rows show a + new-conversation button on hover. Rendered as
  // a sibling so it can position over the right edge of the header.
  newItemButton?: ReactNode;
  // Optional placeholder row shown when the body has no children. Stays
  // mounted while expanded so the height-collapse animation has content
  // to animate (a 0→0 grid transition would otherwise vanish).
  emptyNote?: ReactNode;
  onToggle: () => void;
  // Optional right-click handler for the header row. Project rows use it to
  // open the "移除工作区" context menu; sections without it (置顶 / Agents /
  // 对话 / 群聊) simply have no custom context menu.
  onContextMenu?: (event: React.MouseEvent) => void;
  children?: ReactNode;
}): JSX.Element {
  // Pulled from the SortableSection provider so the header <button>
  // becomes the dnd-kit activator and the reduced-opacity visual lands
  // on the dragged source. Null when this section isn't sortable (置顶
  // — fixed-position and never inside a SortableContext).
  const dragHandle = useSidebarSectionDragHandle();
  const dragHandleProps = dragHandle?.dragHandleProps;
  const isDragging = dragHandle?.isDragging ?? false;
  const headerClassName = [
    "project-row",
    "sidebar-section-row",
    active ? "active" : "",
    expanded ? "expanded" : "",
    pending ? "pending-switch" : "",
    running ? "running" : "",
    unread ? "has-unread" : "",
    isDragging ? "dragging" : "",
    dragHandleProps ? "can-reorder" : "",
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <>
      <div className="sidebar-section-header-group" onContextMenu={onContextMenu}>
        <button
          className={headerClassName}
          type="button"
          aria-expanded={expanded}
          aria-label={ariaLabel}
          aria-busy={pending || running || undefined}
          aria-current={active ? "page" : undefined}
          title={title}
          onClick={onToggle}
          {...dragHandleProps}
        >
          <SectionRowIcon
            collapsed={!expanded}
            iconKind={iconKind}
            CollapsedIcon={CollapsedIcon}
            ExpandedIcon={ExpandedIcon}
          />
          <span className="project-row-label">
            <span className="project-row-name">{label}</span>
            <ChevronRight
              className="project-row-chevron icon"
              aria-hidden="true"
            />
          </span>
          {loading || (running && !expanded) ? (
            <span className="project-row-loading" aria-hidden="true" />
          ) : null}
          {unread && !loading && !running ? (
            <span className="project-row-unread" aria-hidden="true" />
          ) : null}
        </button>
        {newItemButton}
        {actions ? <div className="sidebar-section-actions">{actions}</div> : null}
      </div>
      <SidebarCollapseBody expanded={expanded}>
        {children ?? (emptyNote ? (
          <div className="sidebar-section-empty-note">{emptyNote}</div>
        ) : null)}
      </SidebarCollapseBody>
    </>
  );
}
