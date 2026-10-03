import { createRoot } from "react-dom/client";
import type { GitChangeFile, GitFileDiffResult, GitStatusResult } from "../../src/shared/protocol";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { WorkspaceReviewPanel } from "../../src/renderer/WorkspaceReviewPanels";
import "../../src/renderer/styles.css";
import "./fixture.css";

// Synthetic review data. Query parameters: theme=dark, size=20, lang=en,
// width=<panel px>, set=one|clean|repo|long.
const query = new URLSearchParams(location.search);
document.documentElement.dataset.theme = query.get("theme") || "light";
document.documentElement.dataset.platform = "darwin";
document.documentElement.style.setProperty("--conversation-message-font-size", `${query.get("size") || 14}px`);
const set = query.get("set") ?? "many";

function lines(count: number, make: (index: number) => string): string {
  return Array.from({ length: count }, (_, index) => make(index)).join("\n") + "\n";
}

const sidebarOriginal = lines(80, (index) => `  --sidebar-token-${index}: ${index * 2}px;`);
const sidebarModified = sidebarOriginal
  .replace("--sidebar-token-12: 24px;", "--sidebar-token-12: 28px;")
  .replace("--sidebar-token-40: 80px;", "--sidebar-token-40: 72px;\n  --sidebar-token-40b: 6px;");

const sources: Record<string, { file: GitChangeFile; original: string; modified: string }> = {};

function add(file: GitChangeFile, original: string, modified: string): void {
  sources[file.path] = { file, original, modified };
}

add(
  { path: "desktop/src/renderer/WorkspaceReviewPanels.tsx", status: "modified", additions: 214, deletions: 96 },
  lines(120, (index) => `const value${index} = compute(${index});`),
  lines(140, (index) => (index % 9 === 0 ? `const value${index} = computeLive(${index}, options);` : `const value${index} = compute(${index});`)),
);
add({ path: "desktop/src/renderer/styles/sidebar.css", status: "modified", additions: 3, deletions: 2 }, sidebarOriginal, sidebarModified);
add({ path: "desktop/src/renderer/styles/workspace.css", status: "modified", additions: 48, deletions: 31 }, sidebarOriginal, sidebarModified.replace("12", "14"));
add(
  { path: "desktop/src/renderer/ReviewSession.ts", status: "added", additions: 42, deletions: 0 },
  "",
  lines(42, (index) => `export const reviewStep${index} = ${index};`),
);
add(
  { path: "internal/appserver/git_review_handlers.go", status: "untracked", additions: 18, deletions: 0 },
  "",
  lines(18, (index) => `// handler line ${index}`),
);
add(
  { path: "docs/en/project/review.md", old_path: "docs/en/project/diff-viewer.md", status: "renamed", additions: 6, deletions: 4 },
  lines(30, (index) => `Line ${index} of the diff viewer guide.`),
  lines(32, (index) => `Line ${index} of the review guide.`),
);
add(
  { path: "docs/zh-cn/project/diff-viewer.md", status: "deleted", additions: 0, deletions: 30 },
  lines(30, (index) => `差异查看器指南第 ${index} 行。`),
  "",
);
add({ path: "assets/review/empty-state.png", status: "added", additions: 0, deletions: 0, binary: true }, "", "");
add(
  { path: "go.mod", status: "modified", additions: 1, deletions: 1 },
  "module example\n\ngo 1.24\n",
  "module example\n\ngo 1.25\n",
);
add(
  {
    path: "packages/protocol/src/a-very-long-directory-name-that-keeps-going/another-nested-folder/review-protocol-contract-with-a-long-name.ts",
    status: "modified",
    additions: 12,
    deletions: 3,
  },
  lines(20, (index) => `export type Field${index} = string;`),
  lines(29, (index) => `export type Field${index} = string | undefined;`),
);

const selection =
  set === "one" ? ["desktop/src/renderer/styles/sidebar.css"]
    : set === "clean" ? []
      : Object.keys(sources);
const files = selection.map((path) => sources[path].file).sort((left, right) => left.path.localeCompare(right.path));

const totals = files.reduce(
  (sum, file) => ({ additions: sum.additions + file.additions, deletions: sum.deletions + file.deletions }),
  { additions: 0, deletions: 0 },
);
const gitStatus: GitStatusResult = {
  is_repo: set !== "repo",
  branch: set === "long" ? "codex/review-workbench-with-a-really-long-branch-name" : "feature/review-redo",
  dirty_count: files.length,
  diff: { files: files.length, ...totals },
};

Object.defineProperty(window, "wuu", {
  configurable: true,
  value: {
    initialLanguagePreference: query.get("lang") === "en" ? "en-US" : "zh-CN",
    listGitChanges: async () => ({ is_repo: set !== "repo", root: "/repo", files: set === "repo" ? [] : files }),
    readGitFileDiff: async (path: string): Promise<GitFileDiffResult> => {
      const source = sources[path];
      return {
        is_repo: true,
        path,
        old_path: source.file.old_path,
        status: source.file.status,
        additions: source.file.additions,
        deletions: source.file.deletions,
        binary: source.file.binary,
        patch: "",
        original_text: source.file.binary ? undefined : source.original,
        modified_text: source.file.binary ? undefined : source.modified,
        truncated: false,
      };
    },
  },
});

const width = Number(query.get("width")) || 760;

createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <WuuUIRoot>
      <div className="review-preview-host" style={{ width }}>
        <WorkspaceReviewPanel gitStatus={gitStatus} workspaceRoot="/repo" onOpenFile={(path) => console.info("open", path)} />
      </div>
    </WuuUIRoot>
  </I18nProvider>,
);
