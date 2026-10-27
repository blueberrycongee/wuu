import * as React from "react";
import { createRef, useMemo } from "react";
import { PluginHost } from "../../src/renderer/plugins/PluginHost";
import { createUIComposition } from "../../src/renderer/plugins/ApplicationShell";
// @ts-expect-error Bundled desktop extensions are plain JavaScript modules.
import { activate } from "../../../internal/plugin/bundled/todo/desktop.js";
import { createRoot } from "react-dom/client";
import type { GitStatusResult, InitializeResult } from "../../src/shared/protocol";
import { EnvironmentPanel } from "../../src/renderer/EnvironmentPanel";
import type { ApplicationEnvironmentV1 } from "../../../packages/plugin-sdk/src/ui-environment";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";
import "./fixture.css";

const query = new URLSearchParams(location.search);
document.documentElement.dataset.theme = query.get("theme") || "light";
document.documentElement.dataset.platform = "darwin";
document.documentElement.style.setProperty("--conversation-message-font-size", `${query.get("size") || 15}px`);

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

const host = new PluginHost({ react: React });
await host.activateGeneration({ pluginId: "wuu.default-shell", generation: "preview", register(api) {
  api.registerUISlot({ id: "inspector.sections", cardinality: "many" });
} });
await host.activateGeneration({ pluginId: "todo", generation: "preview", register: activate });
const slots = createUIComposition(host);

function TodoSections(): JSX.Element {
  const snapshot = { contractVersion: 1, session: { status: "idle" }, todo: {
    completed: 1, total: 3, items: [
      { status: "completed", content: "统一变更行的四列网格" },
      { status: "in_progress", content: "关闭按钮与待办标题同一行，互不遮挡" },
      { status: "pending", content: "待办标记列与变更图标列对齐" },
    ],
  } };
  return (
    <div className="plugin-inspector-sections" data-wuu-component="plugin-inspector-sections">
      <slots.Slot name="wuu.default-shell/inspector.sections" context={{ snapshot }} />
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
  const environment = useMemo<ApplicationEnvironmentV1>(() => {
    const snapshot = { actionBusy: false, contextKey: "environment-preview", root: "/repo", status: { ...git, branch },
      capabilities: { review: false, checkout: true, createBranch: true, commit: false, pullRequest: false } };
    const unavailable = async (): Promise<never> => { throw new Error("Git mutations are unavailable in this visual preview"); };
    return { getSnapshot: () => snapshot, subscribe: () => () => {},
      listChanges: unavailable, readDiff: unavailable, checkoutBranch: unavailable, createAndCheckoutBranch: unavailable, commit: unavailable,
      generateCommitMessage: unavailable, createPullRequest: unavailable, scheduleStatusRefresh: () => {} };
  }, [branch]);
  return (
    <figure className="environment-panel-preview-card">
      <figcaption>{label}</figcaption>
      <div className="environment-panel-preview-host conversation-pane">
        <EnvironmentPanel
          panelRef={createRef<HTMLDivElement>()}
          motionState="open"
          environment={environment}
          activeMenu={null}
          running={false}
          pullRequestDisabledReason="先创建功能分支"
          pluginSections={pluginSections}
          onSetActiveMenu={() => {}}
          onClose={() => {}}
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
