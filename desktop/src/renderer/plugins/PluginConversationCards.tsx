import {
  Component,
  createElement,
  useCallback,
  useLayoutEffect,
  useSyncExternalStore,
  type ErrorInfo,
  type ReactNode,
} from "react";
import { X } from "../WuuIcons";

import type { PluginHost, RegisteredConversationCard } from "./PluginHost";
import { useI18n } from "../i18n";

export interface PluginConversationCardsProps {
  host: PluginHost;
  threadId: string;
  onStreamFrame?: () => void;
}

class ConversationCardBoundary extends Component<{
  card: RegisteredConversationCard;
  host: PluginHost;
  fallback: ReactNode;
  children?: ReactNode;
}, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  componentDidCatch(error: unknown, _info: ErrorInfo): void {
    this.props.host.recordConversationCardFailure(this.props.card, error);
  }

  render(): ReactNode {
    return this.state.failed
      ? this.props.fallback
      : this.props.children;
  }
}

function ConversationCardContent({ card }: { card: RegisteredConversationCard }): ReactNode {
  return card.render(Object.freeze({
    id: card.id,
    threadId: card.threadId,
    state: card.state,
    dismiss: card.dismiss,
  }));
}

export function PluginConversationCards({
  host,
  threadId,
  onStreamFrame,
}: PluginConversationCardsProps): JSX.Element | null {
  const { t } = useI18n();
  const subscribe = useCallback(
    (listener: () => void) => host.subscribeConversationCards(listener),
    [host],
  );
  const getSnapshot = useCallback(
    () => host.getConversationCards(),
    [host],
  );
  const cards = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
    .filter((card) => card.threadId === threadId);

  useLayoutEffect(() => {
    if (cards.length > 0) onStreamFrame?.();
  }, [cards, onStreamFrame]);

  if (cards.length === 0) return null;
  return (
    <>
      {cards.map((card) => (
        <article
          className="plugin-conversation-card"
          data-plugin-id={card.pluginId}
          key={`${card.pluginId}:${card.generation}:${card.id}`}
        >
          <div className="plugin-conversation-card-shell">
            <header className="plugin-conversation-card-header">
              <div className="plugin-conversation-card-heading">
                <strong>{card.title}</strong>
                <span>{card.pluginId}</span>
              </div>
              <button
                aria-label={t("common.close")}
                className="icon-button plugin-conversation-card-dismiss"
                onClick={card.dismiss}
                type="button"
              >
                <X className="icon" />
              </button>
            </header>
            <div className="plugin-conversation-card-content">
              <ConversationCardBoundary
                card={card}
                fallback={<div className="plugin-conversation-card-error">{t("pluginCard.renderFailed")}</div>}
                host={host}
              >
                {createElement(ConversationCardContent, { card })}
              </ConversationCardBoundary>
            </div>
          </div>
        </article>
      ))}
    </>
  );
}
