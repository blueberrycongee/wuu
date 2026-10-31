import type { CSSProperties } from "react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { Pin, Volume2, VolumeX } from "./WuuIcons";
import { useSortableTransition } from "./SortableMotion";
import { TruncatedText } from "./TruncatedText";
import { Tooltip } from "./Tooltip";
import type { NavigationRibbonItem } from "./NavigationRibbon";

export function focusAfterRibbonClose(id: string): void {
  const buttons = [...document.querySelectorAll<HTMLButtonElement>("[data-ribbon-id]")];
  const current = buttons.find(button => button.dataset.ribbonId === id);
  // Middle-clicking a background tab must not take focus from a page editor.
  if (!current?.closest(".sidebar-ribbon-tab-row")?.contains(document.activeElement)) return;
  const available = buttons.filter(button => button.getAttribute("aria-disabled") !== "true");
  const index = available.indexOf(current);
  (available[index + 1] ?? available[index - 1] ?? current.closest("nav")?.querySelector<HTMLButtonElement>(".sidebar-ribbon-add"))?.focus();
}

export function RibbonDestination({ item, onMove, onMenu, nativeTooltip, labelVisible = false, tabStop = false }: {
  item: NavigationRibbonItem;
  onMove: (direction: -1 | 1) => void;
  nativeTooltip: boolean;
  labelVisible?: boolean;
  tabStop?: boolean;
  onMenu?: (x: number, y: number) => void;
}): JSX.Element {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging, active } = useSortable({
    id: item.id,
    transition: useSortableTransition(),
  });
  const style: CSSProperties = { transform: CSS.Transform.toString(transform), transition };
  const hasAudio = Boolean(item.audible || item.muted);
  return (
    <div ref={setNodeRef} style={style}
      className={`sidebar-ribbon-tab-row${item.active ? " active" : ""}${hasAudio ? " has-audio" : ""}`}
      data-dragging={isDragging || undefined}>
      <Tooltip content={item.title} disabled={Boolean(active) || labelVisible} native={nativeTooltip}>
        <button
          {...attributes}
          {...listeners}
          ref={setActivatorNodeRef}
          type="button"
          tabIndex={tabStop && !item.disabled ? 0 : -1}
          className={`sidebar-ribbon-button${item.pluginId ? " plugin-navigation-item" : ""}${item.active ? " active" : ""}`}
          aria-label={item.title}
          aria-current={item.active ? "page" : undefined}
          aria-disabled={item.disabled || undefined}
          aria-keyshortcuts="ArrowUp ArrowDown Home End Alt+ArrowUp Alt+ArrowDown"
          aria-haspopup={onMenu ? "menu" : undefined}
          data-ribbon-id={item.id}
          data-wuu-component={item.component}
          data-wuu-plugin={item.pluginId}
          data-pinned={item.pinned || undefined}
          data-dragging={isDragging || undefined}
          onClick={() => { if (!item.disabled) item.onSelect(); }}
          onAuxClick={(event) => {
            if (event.button === 1 && item.onClose) { event.preventDefault(); item.onClose(); }
          }}
          onContextMenu={(event) => {
            event.preventDefault();
            if (!onMenu) return;
            event.currentTarget.focus();
            onMenu(event.clientX, event.clientY);
          }}
          onKeyDown={(event) => {
            if (event.altKey && (event.key === "ArrowUp" || event.key === "ArrowDown")) {
              event.preventDefault();
              event.stopPropagation();
              onMove(event.key === "ArrowUp" ? -1 : 1);
            } else if (!event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey
              && ["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) {
              event.preventDefault();
              event.stopPropagation();
              const buttons = [...(event.currentTarget.closest(".sidebar-ribbon-main")?.querySelectorAll<HTMLButtonElement>("[data-ribbon-id]") ?? [])]
                .filter(button => button.getAttribute("aria-disabled") !== "true");
              const index = buttons.indexOf(event.currentTarget);
              const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1
                : (index + (event.key === "ArrowUp" ? -1 : 1) + buttons.length) % buttons.length;
              buttons[next]?.focus();
            } else if (onMenu && (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10"))) {
              event.preventDefault();
              const rect = event.currentTarget.getBoundingClientRect();
              onMenu(rect.right, rect.top);
            } else if (event.key === "Delete" && item.onClose) {
              event.preventDefault();
              item.onClose();
            }
          }}
        >
          {item.icon}
          <TruncatedText className="sidebar-ribbon-label" text={item.title} nativeTooltip={nativeTooltip} />
          {item.pinned ? <Pin className="sidebar-ribbon-pin" aria-hidden="true" /> : null}
        </button>
      </Tooltip>
      {hasAudio ? <span className="sidebar-ribbon-audio-badge" aria-hidden="true">{item.muted ? <VolumeX /> : <Volume2 />}</span> : null}
    </div>
  );
}
