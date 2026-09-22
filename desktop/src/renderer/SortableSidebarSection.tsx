import { ChevronRight } from "./WuuIcons";
import { type CSSProperties, type HTMLAttributes, type ReactNode, useEffect } from "react";
import { arrayMove, useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { SidebarSectionDragHandleContext } from "./SidebarSection";

export type SidebarSectionHeaderInfo = {
  label: string;
  iconKind: string;
  CollapsedIcon: React.ComponentType<{ className?: string }>;
  ExpandedIcon: React.ComponentType<{ className?: string }>;
};

export function reorderSidebarSections(
  order: string[],
  activeId: string,
  overId: string | null | undefined,
): string[] {
  if (!overId || activeId === overId) return order;
  const from = order.indexOf(activeId);
  const to = order.indexOf(overId);
  if (from === -1 || to === -1) return order;
  return arrayMove(order, from, to);
}

export function SidebarSectionDragPreview({
  info,
}: {
  info: SidebarSectionHeaderInfo;
}): JSX.Element {
  return (
    <div className="sidebar-section-drag-overlay">
      <div className="sidebar-section-row project-row expanded">
        <span className="project-row-icon">
          <info.CollapsedIcon
            className="icon-lg project-row-icon-state collapsed"
            data-project-icon-kind={info.iconKind}
            data-project-icon-state="collapsed"
            aria-hidden="true"
          />
          <info.ExpandedIcon
            className="icon-lg project-row-icon-state expanded"
            data-project-icon-kind={info.iconKind}
            aria-hidden="true"
          />
        </span>
        <span className="project-row-label">
          <span className="project-row-name">{info.label}</span>
          <ChevronRight className="project-row-chevron icon" aria-hidden="true" />
        </span>
      </div>
    </div>
  );
}

export function SortableSidebarSection({
  id,
  className,
  ariaLabel,
  headerInfo,
  registerHeaderInfo,
  sortIndicator,
  containerProps,
  children,
}: {
  id: string;
  className: string;
  ariaLabel: string;
  headerInfo: SidebarSectionHeaderInfo;
  registerHeaderInfo: (id: string, info: SidebarSectionHeaderInfo | null) => void;
  sortIndicator?: "before" | "after";
  containerProps?: HTMLAttributes<HTMLElement>;
  children: ReactNode;
}): JSX.Element {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
    isOver,
  } = useSortable({ id });
  const { label, iconKind, CollapsedIcon, ExpandedIcon } = headerInfo;
  useEffect(() => {
    registerHeaderInfo(id, { label, iconKind, CollapsedIcon, ExpandedIcon });
    return () => registerHeaderInfo(id, null);
  }, [id, label, iconKind, CollapsedIcon, ExpandedIcon, registerHeaderInfo]);
  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };
  const dragHandleProps: HTMLAttributes<HTMLButtonElement> = {
    ...attributes,
    ...listeners,
  };
  return (
    <section
      {...containerProps}
      ref={setNodeRef}
      className={className}
      aria-label={ariaLabel}
      data-section-id={id}
      data-dragging={isDragging || undefined}
      data-drop-over={isOver || undefined}
      data-sort-indicator={sortIndicator}
      style={style}
    >
      <SidebarSectionDragHandleContext.Provider value={{ dragHandleProps, isDragging }}>
        {children}
      </SidebarSectionDragHandleContext.Provider>
    </section>
  );
}
