import {
  Component,
  createElement,
  useCallback,
  useSyncExternalStore,
  type ErrorInfo,
  type ReactNode,
} from "react";

import type { PresentationTarget } from "../../shared/workbench";
import type { PluginHost, RegisteredPresenter } from "./PluginHost";
import type { WorkbenchController } from "./Workbench";

export interface PluginPresentationProps {
  host: PluginHost;
  controller: WorkbenchController;
  target: PresentationTarget;
  presentationKey?: string;
  snapshot: unknown;
  fallback: ReactNode;
  actions?: readonly string[];
  dispatchAction?: (action: string, input?: unknown) => unknown | Promise<unknown>;
  /** Keep the host boundary mounted while temporarily bypassing presenters. */
  enabled?: boolean;
}

const EMPTY_PRESENTERS: readonly RegisteredPresenter[] = Object.freeze([]);

interface BoundaryProps {
  activation: object | undefined;
  host: PluginHost;
  contribution: RegisteredPresenter;
  fallback: ReactNode;
  children?: ReactNode;
}

class PresentationBoundary extends Component<BoundaryProps, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError(): { failed: boolean } { return { failed: true }; }
  componentDidCatch(error: unknown, _info: ErrorInfo): void {
    this.props.host.recordPresenterFailure(this.props.contribution, error);
  }
  componentDidUpdate(previous: BoundaryProps): void {
    if (this.state.failed && previous.activation !== this.props.activation) {
      this.setState({ failed: false });
    }
  }

  render(): ReactNode { return this.state.failed ? this.props.fallback : this.props.children; }
}

function PresentationContent({
  contribution,
  presenterProps,
}: {
  contribution: RegisteredPresenter;
  presenterProps: import("../../shared/workbench").PresenterProps;
}): ReactNode {
  return contribution.render(presenterProps);
}

function renderContribution(
  props: PluginPresentationProps,
  contribution: RegisteredPresenter,
  fallback: ReactNode,
  next: import("../../shared/workbench").PresenterProps["next"],
): ReactNode {
  const presentationHost = props.controller.createPresentationHostAPI(
    contribution.pluginId,
    contribution.generation,
    props.actions ?? [],
    props.dispatchAction,
  );
  return createElement(
    PresentationBoundary,
    {
      key: `${contribution.pluginId}:${contribution.generation}:${contribution.id}`,
      host: props.host,
      activation: props.host.getGenerationActivation(contribution.pluginId, contribution.generation),
      contribution,
      fallback,
    },
    createElement(PresentationContent, {
      contribution,
      presenterProps: Object.freeze({
        contractVersion: 1 as const,
        target: props.target,
        key: props.presentationKey,
        snapshot: props.snapshot,
        host: presentationHost,
        fallback,
        original: props.fallback,
        next,
      }),
    }),
  );
}

export function PluginPresentation(props: PluginPresentationProps): ReactNode {
  const { enabled = true, host, target, presentationKey } = props;
  const subscribe = useCallback((listener: () => void) => host.subscribe(listener), [host]);
  const getSnapshot = useCallback(
    () => enabled ? host.getPresenters(target, presentationKey) : EMPTY_PRESENTERS,
    [enabled, host, target, presentationKey],
  );
  const presenters = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  // Preserve wrapper nesting and the preferred replacement's precedence. A
  // presenter only mounts the rest of the chain when it returns next/fallback.
  const chain = [
    ...presenters.filter((presenter) => presenter.mode === "wrap").reverse(),
    ...presenters.filter((presenter) => presenter.mode === "replace").reverse(),
  ];
  const draw = (index: number, snapshot: unknown): ReactNode => {
    const contribution = chain[index];
    if (contribution === undefined) return props.fallback;
    const fallback = draw(index + 1, snapshot);
    return renderContribution(
      { ...props, snapshot },
      contribution,
      fallback,
      (input) => input === undefined ? fallback : draw(index + 1, input.snapshot),
    );
  };
  return draw(0, props.snapshot);
}
