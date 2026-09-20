import { ChevronRight } from "lucide-react";
import {
  createContext,
  type HTMLAttributes,
  type ReactNode,
  useContext,
  useEffect,
  useState,
} from "react";
import { SectionRowIcon } from "./ThreadSidebar";
import { motionDurationMs, prefersReducedMotion } from "./motion";

/**
 * Sidebar folds share a native 0/auto height transition. Keep the empty shell
 * between toggles so opening has a starting style, but release hidden rows
 * after closing. Nested folds mount at their own intrinsic height, rather than
 * inheriting a parent's temporary measurement.
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
  const [retained, setRetained] = useState(expanded);
  useEffect(() => {
    if (expanded) {
      setRetained(true);
      return;
    }
    if (!retained) return;
    const duration = prefersReducedMotion() ? 0 : motionDurationMs("--motion-slow", 280);
    if (duration <= 0) {
      setRetained(false);
      return;
    }
    // transitionend normally releases the rows. Empty bodies, disabled CSS
    // transitions and background windows still need a bounded cleanup path.
    const timer = window.setTimeout(() => setRetained(false), duration + 32);
    return () => window.clearTimeout(timer);
  }, [expanded, retained]);

  if (!children) return null;
  const collapseClassName = ["thread-list-collapse", className].filter(Boolean).join(" ");
  return (
    <div
      className={collapseClassName}
      data-state={expanded ? "open" : retained ? "closing" : "closed"}
      aria-hidden={!expanded || undefined}
      inert={!expanded}
      onTransitionEnd={(event) => {
        if (!expanded && event.target === event.currentTarget && event.propertyName === "height") {
          setRetained(false);
        }
      }}
    >
      <div className="thread-list-collapse-inner">{expanded || retained ? children : null}</div>
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
 * rows mounted until the height transition finishes after `expanded` flips
 * false, so all four sections share the identical collapse motion.
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
