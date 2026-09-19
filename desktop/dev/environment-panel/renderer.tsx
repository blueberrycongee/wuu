import { createRef } from "react";
import { createRoot } from "react-dom/client";
import type { GitStatusResult, InitializeResult } from "../../src/shared/protocol";
import { EnvironmentPanel } from "../../src/renderer/EnvironmentPanel";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";
import "./fixture.css";

const query = new URLSearchParams(location.search);
document.documentElement.dataset.theme = query.get("theme") || "light";
document.documentElement.dataset.platform = "darwin";
document.documentElement.style.setProperty("--wuu-font-size-ui", `${query.get("size") || 14}px`);

const initialized: InitializeResult = {
  protocol_version: "wuu-app-server/v0.1",
  provider: "preview",
  model: "preview",
  workspace_root: "/repo",
};

const git: GitStatusResult = {
  is_repo: true,
  branch: "main",
  branches: ["main", "feature/panel-layout"],
  dirty_count: 15,
  diff: { files: 15, additions: 163, deletions: 48 },
};

function TodoSections(): JSX.Element {
  return (
    <div className="plugin-inspector-sections" data-wuu-component="plugin-inspector-sections">
      <section className="plugin-inspector-section" data-plugin-id="todo">
        <h2>TODO</h2>
        <div className="plugin-inspector-section-content">
          <ol className="plugin-todo-list">
            <li className="plugin-todo-item" data-status="completed">
              <span className="plugin-todo-marker">✓</span>
              <span>统一变更行的四列网格</span>
            </li>
            <li className="plugin-todo-item" data-status="in_progress">
              <span className="plugin-todo-marker">●</span>
              <span>关闭按钮与待办标题同一行，互不遮挡</span>
            </li>
            <li className="plugin-todo-item" data-status="pending">
              <span className="plugin-todo-marker">○</span>
              <span>待办标记列与变更图标列对齐</span>
            </li>
          </ol>
        </div>
      </section>
    </div>
  );
}

function Card({
  label,
  pluginSections,
  branch = "main",
}: {
  label: string;
  pluginSections?: JSX.Element;
  branch?: string;
}): JSX.Element {
  return (
    <figure className="environment-panel-preview-card">
      <figcaption>{label}</figcaption>
      <div className="environment-panel-preview-host conversation-pane">
        <EnvironmentPanel
          panelRef={createRef<HTMLDivElement>()}
          motionState="open"
          initialized={initialized}
          gitStatus={{ ...git, branch }}
          activeMenu={null}
          running={false}
          pullRequestDisabledReason="先创建功能分支"
          pluginSections={pluginSections}
          onSetActiveMenu={() => {}}
          onClose={() => {}}
          onSelectBranch={() => {}}
          onCreateBranch={() => Promise.resolve()}
          onOpenReview={() => {}}
          onOpenCommit={() => {}}
          onOpenPullRequest={() => {}}
        />
      </div>
    </figure>
  );
}

function Preview(): JSX.Element {
  return (
    <main className="environment-panel-preview">
      <Card label="仅 Git 变更" />
      <Card label="TODO + Git 变更" pluginSections={<TodoSections />} />
      <Card label="长分支名" branch="feature/environment-panel-close-alignment" />
    </main>
  );
}

createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <WuuUIRoot>
      <Preview />
    </WuuUIRoot>
  </I18nProvider>,
);
