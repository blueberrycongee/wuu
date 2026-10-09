import * as React from "react";
import { useSyncExternalStore } from "react";

import type { InspectorSnapshotV1 } from "../../shared/workbench";
import type { PluginHost, RegisteredInspectorSection } from "./PluginHost";
import type { WorkbenchController } from "./Workbench";

export interface PluginInspectorSectionsProps {
  host: PluginHost;
  controller: WorkbenchController;
  snapshot: InspectorSnapshotV1;
}

export function PluginInspectorSections({
  host,
  controller,
  snapshot,
}: PluginInspectorSectionsProps): React.ReactNode {
  const contributions = useSyncExternalStore(
    (listener) => host.subscribe(listener),
    () => host.getInspectorSections(),
    () => host.getInspectorSections(),
  );

  if (contributions.length === 0) return null;
  return (
    <div className="plugin-inspector-sections" data-wuu-component="plugin-inspector-sections">
      {contributions.map((contribution) => (
        <PluginInspectorSection
          key={`${contribution.pluginId}:${contribution.generation}:${contribution.id}`}
          contribution={contribution}
          controller={controller}
          host={host}
          snapshot={snapshot}
        />
      ))}
    </div>
  );
}

function PluginInspectorSection({
  contribution,
  controller,
  host,
  snapshot,
}: {
  contribution: RegisteredInspectorSection;
  controller: WorkbenchController;
  host: PluginHost;
  snapshot: InspectorSnapshotV1;
}): React.ReactNode {
  if (contribution.when?.(snapshot) === false) return null;
  const Content = contribution.render;
  return (
    <section
      className="plugin-inspector-section"
      data-plugin-id={contribution.pluginId}
      data-plugin-generation={contribution.generation}
    >
      <h2>{contribution.title}</h2>
      <div className="plugin-inspector-section-content">
        <InspectorSectionErrorBoundary contribution={contribution} host={host} activation={host.getGenerationActivation(contribution.pluginId, contribution.generation)}>
          <Content
            snapshot={snapshot}
            host={controller.createInspectorHostAPI(
              contribution.pluginId,
              contribution.generation,
            )}
          />
        </InspectorSectionErrorBoundary>
      </div>
    </section>
  );
}

interface InspectorSectionErrorBoundaryProps {
  activation: object | undefined;
  contribution: RegisteredInspectorSection;
  host: PluginHost;
  children: React.ReactNode;
}

interface InspectorSectionErrorBoundaryState {
  error?: unknown;
}

class InspectorSectionErrorBoundary extends React.Component<
  InspectorSectionErrorBoundaryProps,
  InspectorSectionErrorBoundaryState
> {
  state: InspectorSectionErrorBoundaryState = {};

  static getDerivedStateFromError(error: unknown): InspectorSectionErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: unknown): void {
    this.props.host.recordInspectorFailure(this.props.contribution, error);
  }

  componentDidUpdate(previous: InspectorSectionErrorBoundaryProps): void {
    const before = previous.contribution;
    const current = this.props.contribution;
    if (this.state.error !== undefined && (
      previous.activation !== this.props.activation
      || before.pluginId !== current.pluginId
      || before.generation !== current.generation
      || before.id !== current.id
    )) {
      this.setState({ error: undefined });
    }
  }

  render(): React.ReactNode {
    if (this.state.error === undefined) return this.props.children;
    return <p className="plugin-inspector-section-error" role="alert">This section could not be displayed.</p>;
  }
}
