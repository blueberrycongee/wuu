import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";
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

const EMPTY_ITEMS: readonly ResolvedStatusItem[] = Object.freeze([]);
const MAX_VISIBLE_ITEMS = 3;

type ResolvedStatusItem = ComposerStatusItem & Readonly<{ key: string }>;

interface ConversationStatusClusterProps {
  host: PluginHost;
  visible: boolean;
  clusterRef?: (node: HTMLDivElement | null) => void;
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
  threadId,
  todoUpdate,
  onOpenSession,
}: ConversationStatusClusterProps) {
  const { t, formatNumber } = useI18n();
  const overflowRef = useRef<HTMLDetailsElement>(null);
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

  useEffect(() => {
    const dismissOverflow = (event: PointerEvent): void => {
      const overflow = overflowRef.current;
      const target = event.target;
      if (overflow?.open && target instanceof Node && !overflow.contains(target)) {
        overflow.open = false;
      }
    };
    document.addEventListener("pointerdown", dismissOverflow);
    return () => document.removeEventListener("pointerdown", dismissOverflow);
  }, []);

  if (!visible || (!todoVisible && items.length === 0)) return null;

  const visibleItemLimit = MAX_VISIBLE_ITEMS - (todoVisible ? 1 : 0);
  const visibleItems = items.slice(0, visibleItemLimit);
  const hiddenItems = items.slice(visibleItemLimit);
  return (
    <div
      className="jump-to-latest-cluster conversation-status-cluster"
      ref={clusterRef}
      aria-label={t("channels.status")}
    >
      {todoVisible && todoUpdate ? <TodoStatusCapsule todoUpdate={todoUpdate} /> : null}
      {visibleItems.map((item) => (
        <ComposerStatusCapsule key={item.key} item={item} onOpenSession={onOpenSession} />
      ))}
      {hiddenItems.length > 0 ? (
        <details ref={overflowRef} className="conversation-status-overflow">
          <summary
            className="conversation-status-capsule conversation-status-overflow-trigger"
            aria-label={t("common.showMore")}
          >
            +{formatNumber(hiddenItems.length)}
          </summary>
          <div className="conversation-status-overflow-menu">
            {hiddenItems.map((item) => (
              <ComposerStatusCapsule key={item.key} item={item} onOpenSession={onOpenSession} overflow />
            ))}
          </div>
        </details>
      ) : null}
    </div>
  );
}

function TodoStatusCapsule({ todoUpdate }: { todoUpdate: TodoUpdate }) {
  const { t, formatNumber } = useI18n();
  const completed = todoUpdate.todos.filter((item) => item.status === "completed").length;
  const total = todoUpdate.todos.length;
  const progressLabel = t("app.todoProgressLabel", {
    completed: formatNumber(completed),
    total: formatNumber(total),
  });
  return (
    <div className="conversation-status-todo">
      <div
        className="conversation-status-capsule conversation-status-todo-trigger"
        tabIndex={0}
        aria-label={progressLabel}
      >
        <span className="conversation-status-label">TODO</span>
        <span className="conversation-status-secondary">
          {formatNumber(completed)}/{formatNumber(total)}
        </span>
      </div>
      <div className="conversation-status-todo-card" role="tooltip">
        <div className="conversation-status-todo-card-header">
          <strong>TODO</strong>
          <span>{formatNumber(completed)}/{formatNumber(total)}</span>
        </div>
        {todoUpdate.explanation ? (
          <p className="conversation-status-todo-explanation">{todoUpdate.explanation}</p>
        ) : null}
        <ol className="conversation-status-todo-list">
          {todoUpdate.todos.map((item, index) => (
            <li className={`is-${item.status}`} key={`${index}:${item.content}`}>
              <span aria-hidden="true">{item.status === "completed" ? "✓" : index + 1}</span>
              <span>{item.content}</span>
            </li>
          ))}
        </ol>
      </div>
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
