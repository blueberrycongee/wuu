import type {
  PluginGenerationApi,
  RuntimeExecutionContext,
  RuntimeInitializeResult,
  RuntimePlugin,
  ServiceDescriptor,
  ServiceInvokeParams,
  UISlotRenderProps,
  ViewRenderProps,
} from "@wuu/plugin-sdk";

const HISTORY_SERVICE = "git.history";
const VIEW_ID = "git-history";
const ENTRY_ID = "git-history-entry";
const DEFAULT_LIMIT = 50;
const MAX_LIMIT = 100;

interface GitCommitV1 {
  readonly hash: string;
  readonly short_hash: string;
  readonly subject: string;
  readonly author: string;
  readonly authored_at: string;
  readonly refs: readonly string[];
  readonly parents: readonly string[];
}

interface GitHistoryV1 {
  readonly is_repo: boolean;
  readonly root?: string;
  readonly branch?: string;
  readonly commits: readonly GitCommitV1[];
  readonly truncated: boolean;
}

interface GitResult {
  readonly code: number;
  readonly stdout: string;
  readonly stderr: string;
}

interface HistoryState {
  readonly root: string;
  readonly value?: GitHistoryV1;
  readonly error?: unknown;
  readonly loading: boolean;
}

const serviceDescriptor: ServiceDescriptor = {
  name: HISTORY_SERVICE,
  version: "1.0.0",
  methods: [{
    name: "log",
    input_schema: "git.history.log.input.v1",
    output_schema: "git.history.log.output.v1",
  }],
};

function absolutePath(value: string): boolean {
  return value.startsWith("/") || value.startsWith("\\\\") || /^[A-Za-z]:[\\/]/.test(value);
}

function exitCode(error: unknown): number {
  const code = (error as { code?: unknown } | undefined)?.code;
  return typeof code === "number" ? code : 1;
}

// Session git wrappers need the host's original PATH to locate real git.
// Plugin processes do not inherit that bypass variable, so give the wrapper a
// wrapper-free PATH while keeping the wrapper itself as the executable.
function gitOriginalPath(): string {
  const configured = process.env.WUU_INTERNAL_GIT_ORIGINAL_PATH?.trim();
  if (configured) return configured;
  const separator = process.platform === "win32" ? ";" : ":";
  return (process.env.PATH ?? "")
    .split(separator)
    .filter(entry => entry && !entry.includes("git-wrapper"))
    .join(separator);
}

async function scanGit(args: readonly string[], signal: AbortSignal): Promise<GitResult> {
  const { execFile } = await import("node:child_process");
  return await new Promise<GitResult>((resolve, reject) => {
    signal.throwIfAborted();
    execFile("git", ["-C", args[0], ...args.slice(1)], {
      signal,
      timeout: 10_000,
      maxBuffer: 4 * 1024 * 1024,
      windowsHide: true,
      env: {
        ...process.env,
        ...(gitOriginalPath() ? { WUU_INTERNAL_GIT_ORIGINAL_PATH: gitOriginalPath() } : {}),
        GIT_OPTIONAL_LOCKS: "0",
        GIT_TERMINAL_PROMPT: "0",
        LC_ALL: "C",
      },
    }, (error, stdout, stderr) => {
      if (signal.aborted) {
        reject(signal.reason ?? error ?? new Error("Git history request was cancelled"));
        return;
      }
      if (error && (error as { code?: unknown }).code === "ENOENT") {
        resolve({ code: 127, stdout: "", stderr: String(error) });
        return;
      }
      resolve({ code: error ? exitCode(error) : 0, stdout, stderr });
    });
  });
}

function parseRefs(raw: string): string[] {
  const refs: string[] = [];
  for (let ref of raw.split(",")) {
    ref = ref.trim().replace(/^HEAD -> /, "").replace(/^tag: /, "");
    if (!ref || ref === "HEAD" || ref.endsWith("/HEAD") || refs.includes(ref)) continue;
    refs.push(ref);
  }
  return refs.slice(0, 6);
}

function parseLog(output: string): GitCommitV1[] {
  const commits: GitCommitV1[] = [];
  for (const record of output.split("\0")) {
    if (!record) continue;
    const fields = record.split("\n");
    if (fields.length < 7) continue;
    const [hash, shortHash, author, authoredAt, decoration, parentLine] = fields;
    let subject = fields.slice(6).join("\n").trim();
    if (subject.length > 2_000) subject = `${subject.slice(0, 1_999)}…`;
    commits.push({
      hash,
      short_hash: shortHash,
      author,
      authored_at: authoredAt,
      refs: parseRefs(decoration),
      parents: parentLine.split(" ").filter(Boolean),
      subject,
    });
  }
  return commits;
}

async function readHistory(root: string, limit: number, signal: AbortSignal): Promise<GitHistoryV1> {
  const top = await scanGit([root, "rev-parse", "--show-toplevel"], signal);
  if (top.code !== 0 || !top.stdout.trim()) {
    return { is_repo: false, commits: [], truncated: false };
  }

  const repositoryRoot = top.stdout.trim();
  const branchResult = await scanGit([repositoryRoot, "symbolic-ref", "--quiet", "--short", "HEAD"], signal);
  const branch = branchResult.code === 0 ? branchResult.stdout.trim() || undefined : undefined;
  const format = "%H%n%h%n%an%n%aI%n%D%n%P%n%s";
  const log = await scanGit([
    repositoryRoot,
    "log",
    "--exclude=refs/stash",
    "--all",
    "--topo-order",
    `--max-count=${limit + 1}`,
    "-z",
    "--decorate=short",
    "--no-color",
    "--no-show-signature",
    `--format=${format}`,
  ], signal);

  if (log.code !== 0) {
    if (/does not have any commits yet|bad default revision 'HEAD'|unknown revision/i.test(log.stderr)) {
      return { is_repo: true, root: repositoryRoot, branch, commits: [], truncated: false };
    }
    throw new Error(log.stderr.trim() || `git log exited with code ${log.code}`);
  }

  const commits = parseLog(log.stdout);
  return {
    is_repo: true,
    root: repositoryRoot,
    branch,
    commits: commits.slice(0, limit),
    truncated: commits.length > limit,
  };
}

const runtime: RuntimePlugin = {
  concurrentServices: [HISTORY_SERVICE],
  initialize(): RuntimeInitializeResult {
    return { protocol_version: 3, provided_services: [serviceDescriptor] };
  },
  async invokeService(
    call: ServiceInvokeParams,
    _host: unknown,
    execution: RuntimeExecutionContext,
  ): Promise<GitHistoryV1> {
    if (call.service !== HISTORY_SERVICE) {
      throw new Error(`Unknown service ${JSON.stringify(call.service)}`);
    }
    if (call.method !== "log") {
      throw new Error(`Unknown ${HISTORY_SERVICE} method ${JSON.stringify(call.method)}`);
    }
    const input = (call.params ?? {}) as { root?: unknown; limit?: unknown };
    const root = typeof input.root === "string" ? input.root.trim() : "";
    if (!root || root.includes("\0") || !absolutePath(root)) {
      throw new Error("root must be an absolute workspace path");
    }
    const limit = typeof input.limit === "number" && Number.isSafeInteger(input.limit)
      ? Math.min(MAX_LIMIT, Math.max(1, input.limit))
      : DEFAULT_LIMIT;
    return await readHistory(root, limit, execution.signal);
  },
};

interface GraphLane {
  hash: string;
  color: number;
}

// Keep lanes in place until their ancestry joins another lane. Empty slots can
// be reused without shifting unrelated branches sideways on every commit.
function layoutGraph(commits: readonly GitCommitV1[]) {
  const lanes: (GraphLane | null)[] = [];
  let nextColor = 0;
  let width = 1;
  const rows = commits.map(commit => {
    let column = lanes.findIndex(lane => lane?.hash === commit.hash);
    const hasChildren = column >= 0;
    if (column < 0) {
      column = lanes.indexOf(null);
      if (column < 0) column = lanes.length;
      lanes[column] = { hash: commit.hash, color: nextColor++ % 6 };
    }
    const color = lanes[column]!.color;
    const paths: { from: number; to: number; color: number; incoming?: boolean; through?: boolean }[] = [];
    lanes.forEach((lane, index) => {
      if (!lane) return;
      if (index === column) {
        if (hasChildren) paths.push({ from: index, to: index, color, incoming: true });
      } else {
        paths.push({ from: index, to: index, color: lane.color, through: true });
      }
    });
    lanes[column] = null;
    commit.parents.forEach((parent, index) => {
      let target = lanes.findIndex(lane => lane?.hash === parent);
      if (target < 0) {
        target = index === 0 ? column : lanes.indexOf(null);
        if (target < 0) target = lanes.length;
        lanes[target] = { hash: parent, color: index === 0 ? color : nextColor++ % 6 };
      }
      paths.push({ from: column, to: target, color: lanes[target]!.color });
    });
    width = Math.max(width, lanes.length, column + 1);
    while (lanes.length && lanes[lanes.length - 1] === null) lanes.pop();
    return { column, color, paths };
  });
  return { rows, width: width * 16 + 12 };
}

function formatDate(value: string, locale: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const seconds = (date.getTime() - Date.now()) / 1_000;
  const distance = Math.abs(seconds);
  const relative = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "narrow" });
  if (distance < 60) return relative.format(Math.round(seconds), "second");
  if (distance < 3_600) return relative.format(Math.round(seconds / 60), "minute");
  if (distance < 86_400) return relative.format(Math.round(seconds / 3_600), "hour");
  if (distance < 2_592_000) return relative.format(Math.round(seconds / 86_400), "day");
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric" }).format(date);
}

function gitBranchIcon(h: typeof import("react").createElement, size = 18) {
  return h("svg", {
    viewBox: "0 0 24 24",
    width: size,
    height: size,
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.7,
    strokeLinecap: "round",
    strokeLinejoin: "round",
    "aria-hidden": true,
    focusable: "false",
  },
  h("circle", { cx: 6, cy: 5, r: 2.2 }),
  h("circle", { cx: 18, cy: 5, r: 2.2 }),
  h("circle", { cx: 6, cy: 19, r: 2.2 }),
  h("path", { d: "M6 7.2v9.6" }),
  h("path", { d: "M18 7.2v2.1a4.7 4.7 0 0 1-4.7 4.7H8.2" }));
}

function repoName(root: string | undefined): string {
  if (!root) return "";
  return root.replace(/[\\/]+$/, "").split(/[\\/]/).pop() ?? "";
}

export const manifest = {
  version: "0.1.0",
  name: "Git History",
  description: "Git commit graph.",
  requested_permissions: ["files.read", "process.spawn"],
  desktop: {
    required_services: [
      // The default application provider is renderer-owned. Requiring it in a
      // mixed plugin's Go graph can strand activation before that provider
      // connects, so keep it optional; preview surfaces still supply it.
      { name: "ui.application", major_version: 1, required: false },
      { name: HISTORY_SERVICE, major_version: 1, required: true },
    ],
  },
  contributes: {
    workspaceTools: [{
      id: ENTRY_ID,
      view: VIEW_ID,
      title: "Git history",
      icon: "workflow",
      order: 40,
    }],
  },
  interface: {
    displayName: "Git History",
    shortDescription: "Git commit graph.",
    developerName: "Wuu",
    category: "Developer Tools",
    capabilities: ["Read"],
  },
};

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const h = React.createElement;
  const { useEffect, useState } = React;

  api.registerLocale({
    id: "git-history-zh-cn",
    locale: "zh-CN",
    entries: {
      "gitHistory.title": "Git 历史",
      "gitHistory.open": "打开 Git 历史",
      "gitHistory.loading": "加载中…",
      "gitHistory.loadFailed": "无法读取 Git 历史。",
      "gitHistory.openFailed": "无法打开 Git 历史视图。",
      "gitHistory.retry": "重试",
      "gitHistory.notRepository": "不是 Git 仓库",
      "gitHistory.noCommits": "暂无提交",
      "gitHistory.noSubject": "（无提交说明）",
    },
  });
  api.registerLocale({
    id: "git-history-en-us",
    locale: "en-US",
    entries: {
      "gitHistory.title": "Git history",
      "gitHistory.open": "Open Git history",
      "gitHistory.loading": "Loading…",
      "gitHistory.loadFailed": "Could not read Git history.",
      "gitHistory.openFailed": "Could not open the Git history view.",
      "gitHistory.retry": "Retry",
      "gitHistory.notRepository": "Not a Git repository",
      "gitHistory.noCommits": "No commits yet",
      "gitHistory.noSubject": "(no commit message)",
    },
  });

  function useWorkspace() {
    // Avoid a hard ui.application dependency and match by root while the
    // application's active project selection is still settling.
    const [workspace, setWorkspace] = useState<{ id?: string; root?: string }>({});

    useEffect(() => {
      let current = true;
      let reading = false;
      const read = async () => {
        if (reading) return;
        reading = true;
        try {
          const snapshot = await api.listWorkspaces();
          if (!current || api.signal.aborted) return;
          const activeRoot = snapshot.activeRoot;
          const active = snapshot.workspaces.find(candidate =>
            candidate.id === snapshot.activeWorkspaceId ||
            (activeRoot !== undefined && candidate.root === activeRoot));
          const next = { id: active?.id, root: active ? activeRoot ?? active.root : undefined };
          setWorkspace(previous =>
            previous.id === next.id && previous.root === next.root ? previous : next);
        } catch (error) {
          if (!current || api.signal.aborted) return;
          console.error("Git history workspace lookup failed", error);
        } finally {
          reading = false;
        }
      };
      void read();
      const timer = window.setInterval(() => { void read(); }, 4_000);
      const focus = () => { void read(); };
      window.addEventListener("focus", focus);
      return () => {
        current = false;
        window.clearInterval(timer);
        window.removeEventListener("focus", focus);
      };
    }, [api]);

    return workspace;
  }

  function useHistory(
    root: string | undefined,
    workspaceId: string | undefined,
    limit: number,
    revision: number,
    refreshMs: number,
  ): HistoryState | undefined {
    const [state, setState] = useState<HistoryState>();
    const [tick, setTick] = useState(0);

    useEffect(() => {
      if (!root || refreshMs <= 0) return undefined;
      const timer = window.setInterval(() => setTick(value => value + 1), refreshMs);
      const focus = () => setTick(value => value + 1);
      window.addEventListener("focus", focus);
      return () => {
        window.clearInterval(timer);
        window.removeEventListener("focus", focus);
      };
    }, [root, refreshMs]);

    useEffect(() => {
      if (!root) {
        setState(undefined);
        return undefined;
      }
      const controller = new AbortController();
      let current = true;
      setState(previous => ({
        root,
        value: previous?.root === root ? previous.value : undefined,
        loading: true,
      }));
      void api.invokeService<GitHistoryV1>({
        service: HISTORY_SERVICE,
        method: "log",
        params: { root, limit },
      }, { workspaceId, workspaceRoot: root, signal: controller.signal }).then(value => {
        if (!current || controller.signal.aborted || api.signal.aborted) return;
        setState({ root, value, loading: false });
      }).catch(error => {
        if (!current || controller.signal.aborted || api.signal.aborted) return;
        console.error("Git history request failed", error);
        setState({ root, error, loading: false });
      });
      return () => {
        current = false;
        controller.abort();
      };
    }, [api, root, workspaceId, limit, revision, tick]);

    return state?.root === root ? state : undefined;
  }

  function EmptyState({ message }: { message: string }) {
    return h("div", { className: "git-history-page-empty" }, h("p", null, message));
  }

  function GitHistoryView({ locale, translate }: ViewRenderProps) {
    const { id: workspaceId, root } = useWorkspace();
    const [revision, setRevision] = useState(0);
    const state = useHistory(root, workspaceId, DEFAULT_LIMIT, revision, 30_000);

    if (!root) return h(EmptyState, { message: translate("gitHistory.notRepository") });
    if (!state || (state.loading && !state.value)) {
      return h("div", { className: "git-history-page-empty", role: "status" },
        h("p", null, translate("gitHistory.loading")));
    }
    if (state.error !== undefined) {
      return h("div", { className: "git-history-page-empty", role: "status" },
        h("p", null, translate("gitHistory.loadFailed")),
        h("button", {
          className: "git-history-retry",
          type: "button",
          onClick: () => setRevision(value => value + 1),
        }, translate("gitHistory.retry")));
    }
    const history = state.value;
    if (!history?.is_repo) return h(EmptyState, { message: translate("gitHistory.notRepository") });
    if (history.commits.length === 0) return h(EmptyState, { message: translate("gitHistory.noCommits") });

    const graph = layoutGraph(history.commits);
    return h("div", { className: "git-history-page" },
      h("header", { className: "git-history-page-header" },
        h("div", { className: "git-history-page-heading" },
          h("h2", { className: "git-history-page-title" },
            gitBranchIcon(h, 16),
            h("span", null, repoName(history.root) || translate("gitHistory.title"))),
          history.branch ? h("p", { className: "git-history-page-subtitle" },
            h("span", { className: "git-history-page-branch", title: history.branch }, history.branch)) : null)),
      h("ol", { className: "git-history-page-list", "aria-label": translate("gitHistory.title") },
        history.commits.map((commit, index) => h("li", {
          className: "git-history-page-item",
          key: commit.hash,
          style: { gridTemplateColumns: `${graph.width}px minmax(220px, 1fr)` },
          title: `${commit.hash}\n${commit.author} · ${commit.authored_at}\n\n${commit.subject || translate("gitHistory.noSubject")}`,
        },
        h("svg", {
          className: "git-history-graph",
          width: graph.width,
          viewBox: `0 0 ${graph.width} 60`,
          preserveAspectRatio: "none",
          "aria-hidden": true,
        },
        graph.rows[index].paths.map((path, pathIndex) => {
          const from = path.from * 16 + 12;
          const to = path.to * 16 + 12;
          const d = path.through ? `M${from} 0V60`
            : path.incoming ? `M${from} 0V20`
            : from === to ? `M${from} 20V60`
            : `M${from} 20C${from} 40 ${to} 40 ${to} 60`;
          return h("path", { key: pathIndex, d, className: `git-history-color-${path.color}`, fill: "none", stroke: "currentColor", strokeWidth: 2 });
        }),
        h("circle", { cx: graph.rows[index].column * 16 + 12, cy: 20, r: 4,
          className: `git-history-color-${graph.rows[index].color}`, fill: "currentColor" })),
        h("div", { className: "git-history-commit" },
        h("div", { className: "git-history-page-row" },
          h("span", { className: "git-history-page-subject" },
            commit.subject || translate("gitHistory.noSubject"))),
        h("div", { className: "git-history-page-meta" },
          h("span", { className: "git-history-page-hash" }, commit.short_hash),
          h("span", { className: "git-history-author" }, commit.author),
          h("span", { "aria-hidden": true }, "·"),
          h("time", { dateTime: commit.authored_at }, formatDate(commit.authored_at, locale)),
          commit.refs.map(ref => h("span", { className: "git-history-ref", key: ref, title: ref }, ref))))))));
  }

  function GitHistoryRail({ translate }: UISlotRenderProps) {
    const { id: workspaceId, root } = useWorkspace();
    const state = useHistory(root, workspaceId, 1, 0, 0);
    const history = state?.value;
    if (!history?.is_repo || history.commits.length === 0) return null;
    const open = async () => {
      await api.application.invoke("preview.open-plugin", {
        entryId: ENTRY_ID,
        pluginId: api.pluginId,
        generation: api.generation,
        viewTypeId: VIEW_ID,
        title: translate("gitHistory.title"),
        icon: { name: "workflow" },
      });
      await api.application.invoke("navigation.reveal-region", { region: "auxiliary" });
    };
    return h("button", {
      className: "icon-button git-history-rail-button",
      type: "button",
      "aria-label": translate("gitHistory.open"),
      title: translate("gitHistory.open"),
      onClick: () => {
        void open().catch(error => {
          console.error("Opening Git history failed", error);
          api.showNotice({ message: translate("gitHistory.openFailed"), tone: "error", key: "git-history-open" });
        });
      },
    }, gitBranchIcon(h));
  }

  api.registerCSSSnippet({
    id: "git-history-layout",
    css: `
      .git-history-rail-button svg {
        display: block;
      }
      .git-history-page {
        display: flex;
        flex-direction: column;
        box-sizing: border-box;
        height: 100%;
        min-height: 0;
        padding: 14px 14px 18px;
        overflow: auto;
        color: var(--wuu-color-text);
        font-family: var(--wuu-font-family-ui, inherit);
        font-size: var(--wuu-font-size-ui, inherit);
      }
      .git-history-page-header {
        display: flex;
        flex: none;
        gap: 12px;
        align-items: flex-start;
        justify-content: space-between;
        padding-bottom: 10px;
      }
      .git-history-page-heading {
        display: grid;
        min-width: 0;
        gap: 4px;
      }
      .git-history-page-title {
        display: flex;
        gap: 6px;
        align-items: center;
        min-width: 0;
        margin: 0;
        overflow: hidden;
        color: var(--wuu-color-text);
        font-size: 1em;
        font-weight: 600;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .git-history-page-title svg {
        flex: none;
        color: var(--wuu-color-text-muted);
      }
      .git-history-page-title > span {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .git-history-page-subtitle {
        display: flex;
        flex-wrap: wrap;
        gap: 6px;
        align-items: center;
        margin: 0;
        color: var(--wuu-color-text-muted);
        font-size: 0.85em;
      }
      .git-history-page-branch {
        overflow: hidden;
        min-width: 0;
        max-width: 22em;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .git-history-retry:focus-visible {
        outline: 2px solid var(--focus-ring, currentColor);
        outline-offset: 1px;
      }
      .git-history-page-list {
        display: grid;
        align-content: start;
        margin: 0;
        padding: 8px 0 0;
        list-style: none;
      }
      .git-history-page-item {
        display: grid;
        min-width: 0;
        height: 4em;
      }
      .git-history-graph {
        display: block;
        height: 100%;
        overflow: visible;
      }
      .git-history-color-0 { color: light-dark(#386ac9, #7ba7f5); }
      .git-history-color-1 { color: light-dark(#a04fbd, #ce8ee6); }
      .git-history-color-2 { color: light-dark(#218276, #60c2b0); }
      .git-history-color-3 { color: light-dark(#b57419, #e7b760); }
      .git-history-color-4 { color: light-dark(#c15369, #ee8da0); }
      .git-history-color-5 { color: light-dark(#657c29, #a8c76b); }
      .git-history-commit {
        min-width: 0;
        align-self: center;
        padding: 0 8px;
      }
      .git-history-page-item:hover {
        background: color-mix(in srgb, var(--wuu-color-text) 5%, transparent);
      }
      .git-history-page-row {
        display: flex;
        gap: 8px;
        align-items: baseline;
        min-width: 0;
      }
      .git-history-page-hash {
        flex: none;
        color: var(--wuu-color-text-muted);
        font-family: var(--wuu-font-family-mono, ui-monospace, monospace);
        font-size: var(--wuu-font-size-code, 13px);
      }
      .git-history-page-subject {
        min-width: 0;
        overflow: hidden;
        color: var(--wuu-color-text);
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .git-history-page-meta {
        display: flex;
        overflow: hidden;
        white-space: nowrap;
        gap: 3px 6px;
        align-items: center;
        margin-top: 3px;
        color: var(--wuu-color-text-muted);
        font-size: 0.85em;
      }
      .git-history-page-meta > time,
      .git-history-page-hash {
        flex: none;
      }
      .git-history-author {
        overflow: hidden;
        max-width: 16em;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .git-history-ref {
        overflow: hidden;
        max-width: 15em;
        padding: 0 6px;
        color: var(--wuu-color-text-muted);
        text-overflow: ellipsis;
        white-space: nowrap;
        border: 1px solid var(--wuu-color-border-subtle);
        border-radius: 999px;
        line-height: 1.5;
      }
      .git-history-page-empty {
        display: grid;
        height: 100%;
        min-height: 180px;
        place-items: center;
        align-content: center;
        gap: 10px;
        padding: 24px;
        color: var(--wuu-color-text-muted);
        font-family: var(--wuu-font-family-ui, inherit);
        font-size: var(--wuu-font-size-ui, inherit);
        text-align: center;
      }
      .git-history-page-empty p {
        margin: 0;
      }
      .git-history-retry {
        padding: 5px 12px;
        color: var(--wuu-color-text);
        font: inherit;
        background: var(--wuu-color-surface-muted);
        border: 1px solid var(--wuu-color-border-subtle);
        border-radius: var(--wuu-radius-control, 10px);
        cursor: pointer;
      }
    `,
  });

  api.registerViewType({
    id: VIEW_ID,
    title: "Git history",
    icon: "workflow",
    defaultRegion: "auxiliary",
    persistence: "durable",
    render: GitHistoryView,
  });

  api.registerUIContribution({
    id: "git-history-rail",
    target: "wuu.default-preview/workspace.rail",
    order: 30,
    render: GitHistoryRail,
  });
}

export default runtime;
