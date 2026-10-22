import { type ReactNode, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from "react";
import type { TodoUpdate } from "../shared/protocol";
import type {
  ComposerStatusContext,
  ComposerStatusItem,
  ComposerStatusState,
  PluginHost,
  RegisteredComposerStatusSource,
} from "./plugins/PluginHost";
import { useI18n } from "./i18n";
import { externalAgentActivityStatusSource } from "./ExternalAgentActivityStore";
import { composerMenuWidth, FloatingMenuPortal, handleFloatingMenuKeyDown } from "./ComposerFloatingMenu";
import { useHoverReveal } from "./HoverReveal";

const EMPTY_ITEMS: readonly ResolvedStatusItem[] = Object.freeze([]);
const MAX_VISIBLE_ITEMS = 3;

type ResolvedStatusItem = ComposerStatusItem & Readonly<{ key: string }>;

interface ConversationStatusClusterProps {
  host: PluginHost;
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
  todoUpdate: TodoUpdate | undefined;
  onOpenSession: (sessionId: string) => void;
}

interface ComposerStatusStore {
  getSnapshot(): readonly ResolvedStatusItem[];
  subscribe(listener: () => void): () => void;
}

export function ConversationStatusCluster({
  host,
  visible,
  clusterRef,
  navigation,
  hostStatus,
  threadId,
  todoUpdate,
  onOpenSession,
}: ConversationStatusClusterProps) {
  const { t } = useI18n();
  const sources = useSyncExternalStore(
    (listener) => host.subscribeComposerStatusSources(listener),
    () => host.getComposerStatusSources(),
    () => host.getComposerStatusSources(),
  );
  const allSources = useMemo<readonly RegisteredComposerStatusSource[]>(
    () => Object.freeze([...sources, externalAgentActivityStatusSource]),
    [sources],
  );
  const context = useMemo<ComposerStatusContext>(
    () => Object.freeze({ threadId: visible ? threadId : undefined, mainConversation: true as const }),
    [threadId, visible],
  );
  const store = useMemo(() => createComposerStatusStore(allSources, context), [allSources, context]);
  const items = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
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
    document.addEventListener("pointerdown", dismiss);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("pointerdown", dismiss);
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
      <button
        type="button"
        className={className}
        title={item.tooltip}
        onClick={() => onOpenSession(item.action!.sessionId)}
      >
        {content}
      </button>
    );
  }
  return <div className={className} title={item.tooltip}>{content}</div>;
}

function createComposerStatusStore(
  sources: readonly RegisteredComposerStatusSource[],
  context: ComposerStatusContext,
): ComposerStatusStore {
  let cachedItems: readonly ResolvedStatusItem[] = EMPTY_ITEMS;

  const getSnapshot = (): readonly ResolvedStatusItem[] => {
    const next: ResolvedStatusItem[] = [];
    const seenKeys = new Set<string>();
    for (const source of sources) {
      let sourceItems: readonly ComposerStatusItem[];
      try {
        sourceItems = source.getSnapshot(context);
      } catch {
        continue;
      }
      if (!Array.isArray(sourceItems)) continue;
      for (const item of sourceItems) {
        const normalized = normalizeStatusItem(source, item);
        if (normalized && !seenKeys.has(normalized.key)) {
          seenKeys.add(normalized.key);
          next.push(normalized);
        }
      }
    }
    if (sameStatusItems(cachedItems, next)) return cachedItems;
    cachedItems = Object.freeze(next);
    return cachedItems;
  };

  const subscribe = (listener: () => void): (() => void) => {
    const cleanups: Array<() => void> = [];
    for (const source of sources) {
      try {
        const cleanup = source.subscribe(context, listener);
        if (typeof cleanup === "function") cleanups.push(cleanup);
      } catch {
        // One invalid source must not prevent other status sources from updating.
      }
    }
    return () => {
      for (const cleanup of cleanups) {
        try {
          cleanup();
        } catch {
          // Cleanup is best-effort at the untrusted plugin boundary.
        }
      }
    };
  };

  return { getSnapshot, subscribe };
}

function normalizeStatusItem(
  source: RegisteredComposerStatusSource,
  item: ComposerStatusItem,
): ResolvedStatusItem | undefined {
  if (!item || typeof item !== "object") return undefined;
  const id = typeof item.id === "string" ? item.id.trim() : "";
  const label = typeof item.label === "string" ? item.label.trim() : "";
  if (!id || !label) return undefined;
  const action = item.action?.kind === "open-session" && typeof item.action.sessionId === "string"
    && item.action.sessionId.trim()
    ? Object.freeze({ kind: "open-session" as const, sessionId: item.action.sessionId.trim() })
    : undefined;
  return Object.freeze({
    key: `${source.pluginId}:${source.id}:${id}`,
    id,
    label,
    state: normalizeStatusState(item.state),
    secondaryText: cleanOptionalText(item.secondaryText),
    tooltip: cleanOptionalText(item.tooltip),
    updatedAt: cleanOptionalText(item.updatedAt),
    action,
  });
}

function normalizeStatusState(value: unknown): ComposerStatusState | undefined {
  return value === "running" || value === "queued" || value === "waiting"
    || value === "error" || value === "idle"
    ? value
    : undefined;
}

function cleanOptionalText(value: string | undefined): string | undefined {
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function sameStatusItems(
  previous: readonly ResolvedStatusItem[],
  next: readonly ResolvedStatusItem[],
): boolean {
  return previous.length === next.length && previous.every((item, index) => {
    const candidate = next[index];
    return candidate !== undefined
      && item.key === candidate.key
      && item.label === candidate.label
      && item.state === candidate.state
      && item.secondaryText === candidate.secondaryText
      && item.tooltip === candidate.tooltip
      && item.updatedAt === candidate.updatedAt
      && item.action?.kind === candidate.action?.kind
      && item.action?.sessionId === candidate.action?.sessionId;
  });
}
