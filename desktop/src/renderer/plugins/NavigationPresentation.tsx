import { useCallback, useMemo, type ReactNode, type PointerEventHandler } from "react";

import {
  NAVIGATION_ACTIONS,
  type NavigationNodeV1,
  type NavigationSnapshotV1,
} from "../../shared/workbench";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import { PluginPresentation } from "./PluginPresentation";

export interface NavigationSourceNode extends NavigationNodeV1 {
  readonly onActivate?: () => void;
  readonly onTogglePinned?: () => void;
}

export interface NavigationPresentationProps {
  readonly nodes: readonly NavigationSourceNode[];
  readonly fallback: ReactNode;
  readonly onPointerEnter?: PointerEventHandler<HTMLElement>;
  readonly onPointerLeave?: PointerEventHandler<HTMLElement>;
}

interface NavigationModel {
  readonly snapshot: NavigationSnapshotV1;
  readonly actions: readonly string[];
  readonly dispatchAction: (action: string, input?: unknown) => void;
}

/** Build a public immutable DTO and keep all host callbacks outside it. */
export function createNavigationModel(
  sourceNodes: readonly NavigationSourceNode[],
): NavigationModel {
  const sourceById = new Map<string, NavigationSourceNode>();
  const nodes = sourceNodes.map((source) => {
    if (sourceById.has(source.id)) {
      throw new Error(`Duplicate navigation node id: ${source.id}`);
    }
    sourceById.set(source.id, source);
    return Object.freeze({
      id: source.id,
      kind: source.kind,
      label: source.label,
      parentId: source.parentId,
      depth: source.depth,
      description: source.description,
      icon: source.icon,
      active: source.active,
      pinned: source.pinned,
      unread: source.unread,
      running: source.running,
      disabled: source.disabled,
    } satisfies NavigationNodeV1);
  });
  const activeNodeId = nodes.find((node) => node.active)?.id;
  const snapshot = Object.freeze({
    contractVersion: 1 as const,
    nodes: Object.freeze(nodes),
    activeNodeId,
  });
  const actions = Object.freeze([
    ...(sourceNodes.some((node) => node.onActivate !== undefined)
      ? [NAVIGATION_ACTIONS.activateNode]
      : []),
    ...(sourceNodes.some((node) => node.onTogglePinned !== undefined && !node.pinned)
      ? [NAVIGATION_ACTIONS.pinNode]
      : []),
    ...(sourceNodes.some((node) => node.onTogglePinned !== undefined && node.pinned)
      ? [NAVIGATION_ACTIONS.unpinNode]
      : []),
  ]);

  return Object.freeze({
    snapshot,
    actions,
    dispatchAction(action: string, input?: unknown): void {
      const id = navigationActionNodeId(input);
      const source = sourceById.get(id);
      if (!source) throw new Error(`Unknown navigation node id: ${id}`);
      if (source.disabled) throw new Error(`Navigation node is disabled: ${id}`);
      if (action === NAVIGATION_ACTIONS.activateNode && source.onActivate) {
        source.onActivate();
        return;
      }
      if (
        action === NAVIGATION_ACTIONS.pinNode &&
        source.onTogglePinned &&
        !source.pinned
      ) {
        source.onTogglePinned();
        return;
      }
      if (
        action === NAVIGATION_ACTIONS.unpinNode &&
        source.onTogglePinned &&
        source.pinned
      ) {
        source.onTogglePinned();
        return;
      }
      throw new Error(`Navigation action ${action} is unavailable for node: ${id}`);
    },
  });
}

export function NavigationPresentation({
  nodes,
  fallback,
  onPointerEnter,
  onPointerLeave,
}: NavigationPresentationProps): ReactNode {
  const model = useMemo(() => createNavigationModel(nodes), [nodes]);
  const dispatchAction = useCallback(
    (action: string, input?: unknown) => model.dispatchAction(action, input),
    [model],
  );
  // Views portal into this host-owned shell, whose lifetime must not depend
  // on a presenter retaining the native navigation fallback.
  return (
    <aside
      className="sidebar"
      data-wuu-component="sidebar"
      data-workbench-region="navigation"
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
    >
      <PluginPresentation
        host={desktopPluginHost}
        controller={desktopWorkbenchController}
        target="navigation.primary"
        snapshot={model.snapshot}
        fallback={fallback}
        actions={model.actions}
        dispatchAction={dispatchAction}
      />
    </aside>
  );
}

function navigationActionNodeId(input: unknown): string {
  if (typeof input !== "object" || input === null || Array.isArray(input)) {
    throw new Error("Navigation action input must be an object with an id");
  }
  const id = (input as { id?: unknown }).id;
  if (typeof id !== "string" || id.trim() === "") {
    throw new Error("Navigation action input id must be a non-empty string");
  }
  return id;
}
