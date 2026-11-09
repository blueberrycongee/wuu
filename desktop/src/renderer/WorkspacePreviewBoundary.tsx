import { Component, type ReactNode } from "react";
import { useI18n } from "./i18n";
import { WorkspacePanelEmpty } from "./WorkspacePanelEmpty";

class PreviewBoundary extends Component<{ children: ReactNode; fallback: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  render(): ReactNode {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}

// A lazy preview failure must not remove the conversation or panel navigation.
export function WorkspacePreviewBoundary({
  resourceKey,
  children,
}: {
  resourceKey: string;
  children: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <PreviewBoundary
      key={resourceKey}
      fallback={
        <WorkspacePanelEmpty
          title={t("workspace.files.previewLoadFailed")}
          action={
            <button
              className="settings-button"
              type="button"
              data-wuu-action="reload-preview"
              // Browsers cache rejected module loads; remounting React.lazy
              // alone cannot retry the same module graph.
              onClick={() => window.location.reload()}
            >
              {t("workspace.files.reloadWindow")}
            </button>
          }
        />
      }
    >
      {children}
    </PreviewBoundary>
  );
}
