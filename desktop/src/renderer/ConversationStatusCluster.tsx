import { Tooltip } from "./Tooltip";
import { type ReactNode, useCallback, useEffect, useId, useRef, useState, useSyncExternalStore } from "react";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationComposerStatusItemV1, ApplicationComposerStatusSnapshotV1 } from "../../../packages/plugin-sdk/src/ui-composer-presentation";
import { showErrorToast } from "./Toast";
import { useI18n } from "./i18n";
import { composerMenuWidth, FloatingMenuPortal, handleFloatingMenuKeyDown } from "./ComposerFloatingMenu";
import { useHoverReveal } from "./HoverReveal";

const MAX_VISIBLE_ITEMS = 3;

type ResolvedStatusItem = ApplicationComposerStatusItemV1;
type TodoUpdate = NonNullable<ApplicationComposerStatusSnapshotV1["todoUpdate"]>;

interface ConversationStatusClusterProps {
  application: Pick<ApplicationServiceV1, "composerPresentation" | "invoke">;
  visible: boolean;
  /**
   * Measures the compact row while it holds status; expanded popovers do not
   * resize the stream. Navigation alone is not measured: it shows only away
   * from the latest content, where reserved trailing space is off-screen, and
   * releasing that space as the reader arrives would pull the scroll end back.
   */
  clusterRef?: (node: HTMLDivElement | null) => void;
  navigation?: ReactNode;
  /** A host-owned capsule that precedes plugin status, such as a project's work. */
  hostStatus?: ReactNode;
  threadId?: string;
}

export function ConversationStatusCluster({
  application,
  visible,
  clusterRef,
  navigation,
  hostStatus,
  threadId,
}: ConversationStatusClusterProps) {
  const { t } = useI18n();
  const service = application.composerPresentation;
  const sourceThreadId = visible ? threadId : undefined;
  const subscribe = useCallback((listener: () => void) => service.subscribeStatus(sourceThreadId, listener), [service, sourceThreadId]);
  const getSnapshot = useCallback(() => service.getStatusSnapshot(sourceThreadId), [service, sourceThreadId]);
  const { items, todoUpdate } = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const onOpenSession = (sessionId: string) => { void application.invoke("conversation.open-split", { threadId: sessionId }).catch(showErrorToast); };
  const todoVisible = Boolean(
    todoUpdate
      && todoUpdate.todos.length > 0
      && todoUpdate.todos.some((item) => item.status !== "completed"),
  );

  const statusVisible = Boolean(hostStatus) || todoVisible || items.length > 0;
  if (!visible || (!navigation && !statusVisible)) return null;

  const visibleItemLimit = MAX_VISIBLE_ITEMS - (hostStatus ? 1 : 0) - (todoVisible ? 1 : 0);
  const visibleItems = items.slice(0, visibleItemLimit);
  const hiddenItems = items.slice(visibleItemLimit);
  return (
    <div
      className="jump-to-latest-cluster conversation-status-cluster"
      ref={statusVisible ? clusterRef : undefined}
      aria-label={t("common.status")}
    >
      {navigation}
      {hostStatus}
      {todoVisible && todoUpdate ? <TodoStatusCapsule key={`todo:${threadId}`} todoUpdate={todoUpdate} /> : null}
      {visibleItems.map((item) => (
        <ComposerStatusCapsule key={item.key} item={item} onOpenSession={onOpenSession} />
      ))}
      {hiddenItems.length > 0 ? (
        <StatusOverflow key={`overflow:${threadId}`} items={hiddenItems} onOpenSession={onOpenSession} />
      ) : null}
    </div>
  );
}

function StatusOverflow({ items, onOpenSession }: {
  items: readonly ResolvedStatusItem[];
  onOpenSession: (sessionId: string) => void;
}) {
  const { t, formatNumber } = useI18n();
  const [open, setOpen] = useState(false);
  const detailsRef = useRef<HTMLDetailsElement>(null);
  const triggerRef = useRef<HTMLElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  function close(): void {
    setOpen(false);
  }

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      // Read-only statuses still need a focusable scroller when no action exists.
      (menuRef.current?.querySelector<HTMLButtonElement>("button") ?? menuRef.current)?.focus({ preventScroll: true });
    });
    const dismiss = (event: PointerEvent): void => {
      const target = event.target;
      if (target instanceof Node && !detailsRef.current?.contains(target) && !menuRef.current?.contains(target)) close();
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("pointerdown", dismiss, true);
    };
  }, [open]);

  return (
    <details ref={detailsRef} className="conversation-status-overflow" open={open}>
      <summary ref={triggerRef} className="conversation-status-capsule conversation-status-overflow-trigger"
        aria-label={t("common.showMore")}
        onKeyDown={event => { if (open) handleFloatingMenuKeyDown(event, close, triggerRef.current); }}
        onClick={event => { event.preventDefault(); setOpen(value => !value); }}>
        +{formatNumber(items.length)}
      </summary>
      {open ? (
        <FloatingMenuPortal anchorRef={triggerRef} owner="conversation-status" placement="above" align="right"
          width={320} boundarySelector=".conversation-pane" flip>
          <div ref={menuRef} className="conversation-status-overflow-menu" tabIndex={-1}
            role="region" aria-label={t("common.status")}
            onKeyDown={event => handleFloatingMenuKeyDown(event, close, triggerRef.current)}>
            {items.map(item => (
              <ComposerStatusCapsule key={item.key} item={item} overflow
                onOpenSession={sessionId => { close(); onOpenSession(sessionId); }} />
            ))}
          </div>
        </FloatingMenuPortal>
      ) : null}
    </details>
  );
}

function TodoStatusCapsule({ todoUpdate }: { todoUpdate: TodoUpdate }) {
  const { t, formatNumber } = useI18n();
  const triggerRef = useRef<HTMLDivElement>(null);
  const cardRef = useRef<HTMLOListElement>(null);
  const cardId = useId();
  const { revealed, anchorHandlers, layerProps } = useHoverReveal<true>({ interactive: true });
  const completed = todoUpdate.todos.filter((item) => item.status === "completed").length;
  const total = todoUpdate.todos.length;
  const progressLabel = t("app.todoProgressLabel", {
    completed: formatNumber(completed),
    total: formatNumber(total),
  });
  return (
    <div className="conversation-status-todo">
      <div
        ref={triggerRef}
        className="conversation-status-capsule conversation-status-todo-trigger"
        tabIndex={0}
        aria-label={progressLabel}
        aria-describedby={revealed ? cardId : undefined}
        {...anchorHandlers(true)}
        onKeyDown={event => {
          if (event.key === "Tab" && !event.shiftKey && revealed) {
            event.preventDefault();
            cardRef.current?.focus();
          }
        }}
      >
        <svg className="conversation-status-todo-progress" viewBox="0 0 20 20" aria-hidden="true" focusable="false">
          <circle className="todo-progress-track" cx="10" cy="10" r="7" />
          <circle
            className="todo-progress-value"
            cx="10" cy="10" r="7" pathLength="100"
            strokeDasharray="100"
            strokeDashoffset={100 - (completed / total) * 100}
          />
        </svg>
        <span className="conversation-status-label">TODO</span>
        <span className="conversation-status-secondary">
          {formatNumber(completed)}/{formatNumber(total)}
        </span>
      </div>
      {revealed ? (
        <FloatingMenuPortal anchorRef={triggerRef} owner="conversation-todo" layer="tooltip"
          placement="above" align="center" width={composerMenuWidth(390)} boundarySelector=".conversation-pane" flip>
          <div {...layerProps}>
            <ol ref={cardRef} id={cardId} className="conversation-status-todo-card" role="tooltip" tabIndex={0}
              onKeyDown={event => {
                if (event.key === "Escape" || event.key === "Tab") {
                  triggerRef.current?.focus();
                  if (event.key === "Tab" && event.shiftKey) event.preventDefault();
                }
              }}>
              {todoUpdate.todos.map((item, index) => (
                <li className={`is-${item.status}`} key={`${index}:${item.content}`}>
                  <span className="conversation-status-todo-marker" aria-hidden="true">
                    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" focusable="false">
                      <circle className="todo-step-ring" cx="10" cy="10" r="7" pathLength="100" />
                      <path className="todo-step-check" d="m5 10 3.2 3.2L15 6.5" pathLength="1" />
                    </svg>
                  </span>
                  <span>{item.content}</span>
                </li>
              ))}
            </ol>
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

function ComposerStatusCapsule({
  item,
  onOpenSession,
  overflow = false,
}: {
  item: ResolvedStatusItem;
  onOpenSession: (sessionId: string) => void;
  overflow?: boolean;
}) {
  const content = (
    <>
      <span className="conversation-status-label">{item.label}</span>
      {item.secondaryText ? (
        <span className="conversation-status-secondary">{item.secondaryText}</span>
      ) : null}
    </>
  );
  const className = `conversation-status-capsule${overflow ? " is-overflow-item" : ""}`;
  if (item.action?.kind === "open-session") {
    return (
      <Tooltip asChild content={item.tooltip}><button aria-description={item.tooltip}
        type="button"
        className={className}
        onClick={() => onOpenSession(item.action!.sessionId)}
      >
        {content}
      </button></Tooltip>
    );
  }
  return <Tooltip asChild content={item.tooltip}><div className={className}>{content}</div></Tooltip>;
}
