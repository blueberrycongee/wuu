import {
  Component,
  createElement,
  Fragment,
  useCallback,
  useMemo,
  useSyncExternalStore,
  type ErrorInfo,
  type ReactNode,
} from "react";

import {
  type PluginHost,
  type PluginSlotId,
  type PluginSlotRenderContext,
  type RegisteredPluginSlotContribution,
} from "./PluginHost";
import { useI18n } from "../i18n";
import { createPluginTranslator } from "./pluginI18n";

export interface PluginSlotProps {
  host: PluginHost;
  id: PluginSlotId;
  context?: PluginSlotRenderContext;
}

export interface PluginSlotContributionProps extends PluginSlotProps {
  contribution: RegisteredPluginSlotContribution;
}

interface ContributionBoundaryProps {
  activation: object | undefined;
  host: PluginHost;
  slotId: PluginSlotId;
  contribution: RegisteredPluginSlotContribution;
  children?: ReactNode;
}

interface ContributionBoundaryState {
  failed: boolean;
}

const EMPTY_CONTEXT: PluginSlotRenderContext = Object.freeze({});

class ContributionBoundary extends Component<ContributionBoundaryProps, ContributionBoundaryState> {
  state: ContributionBoundaryState = { failed: false };

  static getDerivedStateFromError(): ContributionBoundaryState {
    return { failed: true };
  }

  componentDidCatch(error: unknown, _errorInfo: ErrorInfo): void {
    const { contribution, host, slotId } = this.props;
    host.recordRenderFailure(contribution, { slotId }, error);
  }

  componentDidUpdate(previous: ContributionBoundaryProps): void {
    if (this.state.failed && previous.activation !== this.props.activation) {
      this.setState({ failed: false });
    }
  }

  render(): ReactNode {
    return this.state.failed ? null : this.props.children;
  }
}

function ContributionContent({
  context,
  contribution,
  host,
  locale,
}: {
  context: PluginSlotRenderContext;
  contribution: RegisteredPluginSlotContribution;
  host: PluginHost;
  locale: string;
}): ReactNode {
  const localizedContext = useMemo(() => Object.freeze({
    ...context,
    locale,
    translate: createPluginTranslator(host, locale),
  }), [context, host, locale]);
  return contribution.render(localizedContext);
}

export function PluginSlot({ host, id, context = EMPTY_CONTEXT }: PluginSlotProps): ReactNode {
  const subscribe = useCallback((listener: () => void) => host.subscribeSlot(id, listener), [host, id]);
  const getSnapshot = useCallback(() => host.getSlotSnapshot(id), [host, id]);
  const contributions = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

  return createElement(
    Fragment,
    null,
    ...contributions.map((contribution) => createElement(PluginSlotContribution, {
      key: `${contribution.pluginId}:${contribution.generation}:${contribution.id}`,
      host,
      id,
      context,
      contribution,
    })),
  );
}

export function PluginSlotContribution({
  host,
  id,
  context = EMPTY_CONTEXT,
  contribution,
}: PluginSlotContributionProps): ReactNode {
  const { locale } = useI18n();
  return createElement(
    ContributionBoundary,
    {
      host,
      slotId: id,
      activation: host.getGenerationActivation(contribution.pluginId, contribution.generation),
      contribution,
    },
    <div
      className="plugin-contribution-root"
      data-wuu-component="plugin-contribution"
      data-wuu-plugin={contribution.pluginId}
      data-wuu-slot={id}
      data-wuu-contribution={contribution.id}
    >
      <ContributionContent contribution={contribution} context={context} host={host} locale={locale} />
    </div>,
  );
}
