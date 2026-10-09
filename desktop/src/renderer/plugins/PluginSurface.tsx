import {
  Component,
  createElement,
  useCallback,
  useSyncExternalStore,
  type ErrorInfo,
  type ReactNode,
} from "react";

import {
  type PluginHost,
  type PluginSlotRenderContext,
  type PluginSurfaceId,
  type RegisteredPluginSurfaceContribution,
} from "./PluginHost";

export interface PluginSurfaceProps {
  host: PluginHost;
  id: PluginSurfaceId;
  fallback: ReactNode;
  context?: PluginSlotRenderContext;
}

interface SurfaceBoundaryProps {
  activation: object | undefined;
  host: PluginHost;
  surfaceId: PluginSurfaceId;
  contribution: RegisteredPluginSurfaceContribution;
  fallback: ReactNode;
  children?: ReactNode;
}

interface SurfaceBoundaryState {
  failed: boolean;
}

const EMPTY_CONTEXT: PluginSlotRenderContext = Object.freeze({});

class SurfaceBoundary extends Component<SurfaceBoundaryProps, SurfaceBoundaryState> {
  state: SurfaceBoundaryState = { failed: false };

  static getDerivedStateFromError(): SurfaceBoundaryState {
    return { failed: true };
  }

  componentDidCatch(error: unknown, _errorInfo: ErrorInfo): void {
    const { contribution, host, surfaceId } = this.props;
    host.recordRenderFailure(contribution, { surfaceId }, error);
  }

  componentDidUpdate(previous: SurfaceBoundaryProps): void {
    if (this.state.failed && previous.activation !== this.props.activation) {
      this.setState({ failed: false });
    }
  }

  render(): ReactNode {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}

function SurfaceContent({
  context,
  contribution,
  fallback,
}: {
  context: PluginSlotRenderContext;
  contribution: RegisteredPluginSurfaceContribution;
  fallback: ReactNode;
}): ReactNode {
  return contribution.render(context, fallback);
}

function renderContribution(
  host: PluginHost,
  surfaceId: PluginSurfaceId,
  contribution: RegisteredPluginSurfaceContribution,
  context: PluginSlotRenderContext,
  fallback: ReactNode,
): ReactNode {
  return createElement(
    SurfaceBoundary,
    {
      key: `${contribution.pluginId}:${contribution.generation}:${contribution.id}`,
      host,
      surfaceId,
      activation: host.getGenerationActivation(contribution.pluginId, contribution.generation),
      contribution,
      fallback,
    },
    <div
      className="plugin-contribution-root"
      data-wuu-component="plugin-contribution"
      data-wuu-plugin={contribution.pluginId}
      data-wuu-surface={surfaceId}
      data-wuu-contribution={contribution.id}
    >
      <SurfaceContent context={context} contribution={contribution} fallback={fallback} />
    </div>,
  );
}

export function PluginSurface({
  host,
  id,
  fallback,
  context = EMPTY_CONTEXT,
}: PluginSurfaceProps): ReactNode {
  const subscribe = useCallback((listener: () => void) => host.subscribeSurface(id, listener), [host, id]);
  const getSnapshot = useCallback(() => host.getSurfaceSnapshot(id), [host, id]);
  const contributions = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const replacement = contributions.filter((contribution) => contribution.mode === "replace").at(-1);
  let content = replacement
    ? renderContribution(host, id, replacement, context, fallback)
    : fallback;

  for (const wrapper of contributions.filter((contribution) => contribution.mode === "wrap")) {
    content = renderContribution(host, id, wrapper, context, content);
  }
  return content;
}
