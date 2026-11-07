import type { HTMLAttributes, KeyboardEvent, ReactNode } from "react";
import { closestCenter, DndContext, TouchSensor, useSensor, useSensors } from "@dnd-kit/core";
import { restrictToParentElement, restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { arrayMove, SortableContext, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { SidebarPointerSensor } from "./SidebarPointerSensor";
import { useSortableTransition } from "./SortableMotion";

/** The full model order after dropping `id` onto `target`'s position. */
export function placedModelIDs(models: readonly { id: string }[], id: string, target: string): string[] {
  const ids = models.map(model => model.id);
  return arrayMove(ids, ids.indexOf(id), ids.indexOf(target));
}

/** Model lists reorder by dragging a row, or Alt+Arrow from any control in it. */
export function SortableModelRows({ ids, disabled, onReorder, children }: {
  ids: readonly string[];
  disabled: boolean;
  onReorder: (id: string, target: string) => void;
  children: ReactNode;
}): JSX.Element {
  const sensors = useSensors(
    useSensor(SidebarPointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 250, tolerance: 5 } }),
  );
  // dnd-kit renders its live region inline by default, which would add stray children to the role="list".
  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} accessibility={{ container: document.body }} modifiers={[restrictToVerticalAxis, restrictToParentElement]}
      onDragEnd={({ active, over }) => { if (over && active.id !== over.id) onReorder(String(active.id), String(over.id)); }}>
      <SortableContext items={[...ids]} strategy={verticalListSortingStrategy} disabled={disabled}>{children}</SortableContext>
    </DndContext>
  );
}

export function SortableModelRow({ id, onMove, onMenu, className, children, ...props }: HTMLAttributes<HTMLDivElement> & {
  id: string;
  onMove: (direction: -1 | 1) => void;
  onMenu?: (x: number, y: number) => void;
}): JSX.Element {
  const { listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id, transition: useSortableTransition() });
  function onKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    if (onMenu && (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10"))) {
      event.preventDefault();
      const bounds = (event.target as HTMLElement).getBoundingClientRect();
      onMenu(bounds.left, bounds.bottom + 4);
      return;
    }
    if (!event.altKey || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
    event.preventDefault();
    event.stopPropagation();
    onMove(event.key === "ArrowUp" ? -1 : 1);
  }
  return (
    <div {...props} {...listeners} ref={setNodeRef} className={className} onKeyDown={onKeyDown}
      onContextMenu={onMenu ? event => { event.preventDefault(); onMenu(event.clientX, event.clientY); } : undefined}
      style={{ transform: CSS.Translate.toString(transform), transition }} data-dragging={isDragging || undefined}>
      {children}
    </div>
  );
}
