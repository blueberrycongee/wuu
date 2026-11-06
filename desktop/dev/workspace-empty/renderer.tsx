import { useLayoutEffect } from "react";
import { createRoot } from "react-dom/client";
import type { ApplicationPreviewContentV1 } from "../../../packages/plugin-sdk/src/ui-preview-content";
import { I18nProvider } from "../../src/renderer/i18n";
import { PreviewContentProvider } from "../../src/renderer/PreviewContentContext";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { applyThemePreference } from "../../src/renderer/Theme";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { WorkspaceFileTree } from "../../src/renderer/WorkspaceFiles";
import "../../src/renderer/styles.css";

// Isolated workspace-panel fixture: the real file tree and empty state with a
// synthetic, empty project. No application server or local files are read.
const params = new URLSearchParams(location.search);
const root = "/Users/you/empty-project";
const service: ApplicationPreviewContentV1 = {
  capabilities: { nativeItemMenu: false, revealItem: false, exportFile: false },
  resolveResourceURL: () => undefined,
  openFile: async () => undefined,
  listDirectory: async () => ({ root, path: "", entries: [], truncated: false }),
  readFile: async () => { throw new Error("No file is selected"); },
  revealItem: async () => undefined,
  showItemMenu: async () => ({ action: "none" }),
  exportFile: async () => undefined,
};

function Fixture(): JSX.Element {
  useLayoutEffect(() => {
    applyThemePreference(params.get("theme") === "dark" ? "dark" : "light");
    document.documentElement.dataset.platform = "darwin";
    applyMessageFlowFontSize(Number(params.get("font")) || 15);
  }, []);
  return (
    <div style={{ display: "grid", height: "100vh", placeItems: "center", background: "var(--canvas)" }}>
      <div className="workspace-right-panel files right-panel-open" data-wuu-view="files"
        style={{ width: 360, height: 640, gridTemplateRows: "var(--canvas-titlebar-height) minmax(0, 1fr)", "--canvas-titlebar-height": "44px" } as React.CSSProperties}>
        <div className="workspace-panel-tabbar">
          <div className="workspace-panel-tabs">
            <div className="workspace-tool-tab active">
              <span className="workspace-tool-tab-main">文件</span>
            </div>
          </div>
        </div>
        <div className="workspace-panel-body">
          <PreviewContentProvider service={service}>
            <WorkspaceFileTree
              activeContext={{ kind: "project", project_id: "synthetic-empty-project", cwd: root }}
              open
              onOpenFile={() => undefined}
            />
          </PreviewContentProvider>
        </div>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <I18nProvider preferenceStore={{ get: () => "zh-CN", set: async () => undefined, subscribe: () => () => undefined }}>
    <WuuUIRoot><Fixture /></WuuUIRoot>
  </I18nProvider>,
);
