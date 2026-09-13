import { afterEach, describe, expect, it } from "vitest";
import type { ThreadItem } from "../shared/protocol";
import { readableToolActivityCommand } from "./ToolActivity";
import {
  activitySummaryText,
  buildToolActivityProcessSegments,
  buildToolActivitySections,
  collectTurnSources,
  summarizeToolActivity,
  readableToolActivityName,
  toolDisplayLabel,
} from "./ToolActivityHelpers";
import { setActiveLocale } from "./i18n";

afterEach(() => setActiveLocale("zh-CN"));

describe("localized plugin tool names", () => {
  it("uses saved locale overrides with language and default fallbacks", () => {
    const display = {
      label: "Working notes",
      label_translations: { "zh-CN": "工作笔记", zh: "筆記" },
    };
    expect(toolDisplayLabel(display, "zh-CN")).toBe(display.label_translations["zh-CN"]);
    expect(toolDisplayLabel(display, "zh-HK")).toBe(display.label_translations.zh);
    expect(toolDisplayLabel(display, "fr-FR")).toBe(display.label);
    const item: ThreadItem = {
      id: "notes",
      type: "tool_call",
      name: "plugin_notes_work_0123456789abcdef",
      status: "completed",
      arguments: "{}",
      display,
    };
    for (const locale of ["zh-CN", "en-US"] as const) {
      setActiveLocale(locale);
      const expected = toolDisplayLabel(display, locale);
      expect(readableToolActivityName(item)).toBe(expected);
      expect(readableToolActivityCommand(item)).toBe(expected);
      expect(buildToolActivityProcessSegments([item])[0].text).toBe(expected);
    }
    expect(item.name).toBe("plugin_notes_work_0123456789abcdef");
  });

  it("keeps distinct plugin tools separate when their display names match", () => {
    const items = ["first", "second"].map((id): ThreadItem => ({
      id, type: "tool_call", name: `plugin_${id}_0123456789abcdef`, status: "completed",
      display: { label: "Shared name" },
    }));
    expect(buildToolActivityProcessSegments(items)).toHaveLength(2);
  });

  it("uses the localized label in the silent-turn summary", () => {
    setActiveLocale("zh-CN");
    const summary = summarizeToolActivity([{
      id: "notes",
      type: "tool_call",
      name: "plugin_notes_work_0123456789abcdef",
      status: "completed",
      arguments: "{}",
      display: { label: "Working notes", label_translations: { "zh-CN": "工作笔记" } },
    }]);
    expect(summary.text).toBe("已调用 工作笔记");
  });
});

describe("activitySummaryText", () => {
  it("does not label an aggregated tool summary as incomplete", () => {
    expect(
      activitySummaryText(
        [{
          id: "read",
          kind: "read",
          title: "查看文件",
          status: "failed",
          commands: [],
        }],
        {
          kind: "read",
          text: "查看文件",
          additions: 0,
          deletions: 0,
          running: false,
          failed: true,
        },
      ),
    ).toBe("查看文件");
  });
});

describe("readableToolActivityCommand", () => {
  it("shows Claude Code tool targets instead of generic PascalCase names", () => {
    expect(
      readableToolActivityCommand({
        name: "Bash",
        arguments: JSON.stringify({ command: "rg tool_call desktop/src" }),
      }),
    ).toBe("运行 rg tool_call desktop/src");
    expect(
      readableToolActivityCommand({
        name: "Read",
        arguments: JSON.stringify({ file_path: "/repo/desktop/src/App.tsx" }),
      }),
    ).toBe("读取 App.tsx");
  });

  it("uses structured rich tool results when the text projection is not JSON", () => {
    expect(
      readableToolActivityCommand({
        name: "web_fetch",
        arguments: JSON.stringify({ url: "https://request.test" }),
        result: "[image: screenshot (image/png)]",
        result_detail: {
          structured_content: { url: "https://result.test" },
          content: [{ type: "image", mime_type: "image/png", data: "aW1hZ2U=" }],
        },
      }),
    ).toBe("读取网页 https://result.test");
  });
  it("ignores tool-provided display text and waits for args to parse", () => {
    // The backend ships a preformatted `display.text` ("查看项目目录")
    // with item/started, but it's just a placeholder — once args parse
    // we render the real path. Ignoring display.text keeps the title
    // timing unified (no placeholder → real flicker).
    expect(
      readableToolActivityCommand({
        name: "list_files",
        arguments: undefined,
        display: { kind: "read", text: "查看项目目录" },
      })
    ).toBe("");

    // display.text is also ignored when args parse; we render from args.
    expect(
      readableToolActivityCommand({
        name: "list_files",
        arguments: JSON.stringify({ path: "." }),
        display: { kind: "read", text: "（已忽略）" },
      })
    ).toBe("查看项目目录");
  });

  it("returns empty string when args are missing for known tools", () => {
    // Until args (or result) actually parses, there is nothing to render.
    // The next item/toolCall/delta will reveal the title.
    expect(readableToolActivityCommand({ name: "read_file" })).toBe("");
    expect(readableToolActivityCommand({ name: "bash" })).toBe("");
  });

  it("returns empty string when args are partial JSON", () => {
    // Streaming `item/toolCall/delta` builds up the JSON one chunk at a
    // time; mid-stream it isn't valid JSON yet, so we wait.
    expect(
      readableToolActivityCommand({
        name: "read_file",
        arguments: '{"path":"/foo"',
      })
    ).toBe("");
  });

  it("renders the path once args parse", () => {
    // formatPathTarget collapses multi-segment paths to their basename,
    // so use a single-segment path to assert on the basename directly.
    expect(
      readableToolActivityCommand({
        name: "read_file",
        arguments: JSON.stringify({ path: "bar.ts" }),
      })
    ).toBe("读取 bar.ts");
  });

  it("renders tool calls as process log lines instead of raw JSON", () => {
    expect(
      readableToolActivityCommand({
        name: "list_files",
        arguments: JSON.stringify({ path: "." })
      })
    ).toBe("查看项目目录");

    expect(
      readableToolActivityCommand({
        name: "bash",
        arguments: JSON.stringify({ command: "git status" })
      })
    ).toBe("检查 Git 状态");

    expect(
      readableToolActivityCommand({
        name: "glob",
        arguments: JSON.stringify({ pattern: "**/AGENTS.md", path: "." })
      })
    ).toBe("搜索 AGENTS.md");

    expect(
      readableToolActivityCommand({
        name: "grep",
        arguments: JSON.stringify({
          pattern: "\\]\\([^h#][^:)]*\\)",
          path: "docs/app-server-protocol.md",
        }),
      })
    ).toBe("在 app-server-protocol.md 中搜索 \\]\\([^h#][^:)]*\\)");
  });

  it("keeps explicit shell commands readable", () => {
    expect(
      readableToolActivityCommand({
        name: "bash",
        arguments: JSON.stringify({ command: "npm run typecheck" })
      })
    ).toBe("运行 npm run typecheck");

    expect(
      readableToolActivityCommand({
        name: "bash",
        arguments: JSON.stringify({ command: "npx vitest run" }),
        display: { capability: "command.bash" }
      })
    ).toBe("运行 npx vitest run — command.bash");
  });

  it("renders apply_patch as a file update tool", () => {
    expect(
      readableToolActivityCommand({
        name: "apply_patch",
        arguments: JSON.stringify({ patch: "*** Begin Patch\n*** End Patch" })
      })
    ).toBe("更新文件");
  });

  it("renders bash background actions from capability metadata", () => {
    expect(
      readableToolActivityCommand({
        name: "bash",
        arguments: JSON.stringify({ action: "start_background", command: "npm run dev" }),
        display: { capability: "command.background" }
      })
    ).toBe("启动 npm run dev — command.background");
  });

  it("summarizes apply_patch result metadata as file updates", () => {
    const summary = summarizeToolActivity([
      {
        id: "tool-1",
        type: "tool_call",
        name: "apply_patch",
        status: "completed",
        result: JSON.stringify({
          changed_files: ["src/app.ts"],
          risk_summary: { added_lines: 3, deleted_lines: 1 }
        })
      } satisfies ThreadItem
    ]);

    expect(summary).toMatchObject({
      kind: "edit",
      text: "已编辑",
      fileName: "app.ts",
      additions: 3,
      deletions: 1,
      running: false,
      failed: false
    });
  });

  it("keeps MCP tool calls raw", () => {
    expect(
      readableToolActivityCommand({
        name: "mcp_docs_search",
        arguments: JSON.stringify({ query: "abc" })
      })
    ).toBe('mcp_docs_search {"query":"abc"}');
  });

  it("appends the capability suffix when display.capability is set", () => {
    expect(
      readableToolActivityCommand({
        name: "bash",
        arguments: JSON.stringify({ command: "npx vitest" }),
        display: { kind: "command", text: "运行 npx vitest", capability: "command.bash" }
      })
    ).toBe("运行 npx vitest — command.bash");
  });

  it("localizes generated activity copy without changing command arguments", () => {
    setActiveLocale("en-US");

    expect(
      readableToolActivityCommand({
        name: "bash",
        arguments: JSON.stringify({ command: "npm run typecheck" }),
      }),
    ).toBe("Run npm run typecheck");

    expect(
      summarizeToolActivity([
        {
          id: "tool-1",
          type: "tool_call",
          name: "bash",
          status: "completed",
          arguments: JSON.stringify({ command: "npm run typecheck" }),
        },
      ]).text,
    ).toBe("Ran 1 command");
  });

  it("omits the capability suffix when display.capability is missing", () => {
    expect(
      readableToolActivityCommand({
        name: "bash",
        arguments: JSON.stringify({ command: "npx vitest" }),
        display: { kind: "command", text: "运行 npx vitest" }
      })
    ).toBe("运行 npx vitest");
  });
});

describe("buildToolActivityProcessSegments", () => {
  it("labels fresh-window and history tools by their actual operation", () => {
    const items = [
      {
        id: "new-context",
        type: "tool_call",
        name: "new_context",
        status: "completed",
        arguments: "{}",
        display: { capability: "context.window" },
      },
      {
        id: "history-read",
        type: "tool_call",
        name: "history_read",
        status: "completed",
        arguments: JSON.stringify({ start_seq: 10 }),
        display: { capability: "context.history" },
      },
      {
        id: "history-search",
        type: "tool_call",
        name: "history_search",
        status: "completed",
        arguments: JSON.stringify({ query: "decision" }),
        display: { capability: "context.history" },
      },
    ] satisfies ThreadItem[];

    expect(buildToolActivitySections(items).map((section) => section.title)).toEqual([
      "开启新上下文窗口",
      "读取会话历史",
      "搜索会话历史",
    ]);
    expect(buildToolActivityProcessSegments(items).map((segment) => segment.text)).toEqual([
      "开启新上下文窗口",
      "读取会话历史",
      "搜索会话历史",
    ]);
  });

  it("groups Claude Code Bash and Read calls by their actual activity", () => {
    const items = [
      {
        id: "tool-1",
        type: "tool_call",
        name: "Bash",
        status: "completed",
        arguments: JSON.stringify({ command: "npm test" }),
      },
      {
        id: "tool-2",
        type: "tool_call",
        name: "Read",
        status: "completed",
        arguments: JSON.stringify({ file_path: "/repo/package.json" }),
      },
    ] satisfies ThreadItem[];

    expect(buildToolActivitySections(items)).toMatchObject([
      { kind: "command", detail: "运行测试" },
      { kind: "read", detail: "package.json" },
    ]);
    expect(buildToolActivityProcessSegments(items).map((segment) => segment.kind)).toEqual([
      "command",
      "read",
    ]);
  });

  it("turns multiple file reads into a count segment", () => {
    const segments = buildToolActivityProcessSegments([
      {
        id: "tool-1",
        type: "tool_call",
        name: "read_file",
        status: "completed",
        arguments: JSON.stringify({ path: "src/App.tsx" }),
      },
      {
        id: "tool-2",
        type: "tool_call",
        name: "read_file",
        status: "completed",
        arguments: JSON.stringify({ path: "src/turns.css" }),
      },
    ] satisfies ThreadItem[]);

    expect(segments).toMatchObject([
      {
        kind: "read",
        count: 2,
      },
    ]);
  });

  it("counts command invocations even when they share the same purpose", () => {
    const items = ["npm test", "npm test", "pnpm test"].map((command, index): ThreadItem => ({
      id: `command-${index}`,
      type: "tool_call",
      name: "bash",
      status: "completed",
      arguments: JSON.stringify({ command }),
    }));
    expect(buildToolActivityProcessSegments(items)).toMatchObject([{ kind: "command", count: 3 }]);
  });

  it("counts distinct file paths without merging equal basenames or repeated reads", () => {
    const items = ["src/index.ts", "test/index.ts", "src/index.ts"].map((path, index): ThreadItem => ({
      id: `read-${index}`,
      type: "tool_call",
      name: "read_file",
      status: "completed",
      arguments: JSON.stringify({ path }),
    }));
    expect(buildToolActivityProcessSegments(items)).toMatchObject([{ kind: "read", count: 2 }]);
  });

  it("compacts long OR search patterns by common prefix", () => {
    const segments = buildToolActivityProcessSegments([
      {
        id: "tool-1",
        type: "tool_call",
        name: "grep",
        status: "completed",
        arguments: JSON.stringify({
          pattern:
            "WORKSPACE_RIGHT_PANEL_MIN_WIDTH|WORKSPACE_RIGHT_PANEL_MAX_WIDTH|WORKSPACE_RIGHT_PANEL_DEFAULT_WIDTH",
        }),
      },
    ] satisfies ThreadItem[]);

    expect(segments).toMatchObject([
      {
        kind: "search",
        text: expect.stringContaining("WORKSPACE_RIGHT_PANEL_*"),
      },
    ]);
  });

  it("uses action-oriented copy for repeated searches", () => {
    const segments = buildToolActivityProcessSegments([
      {
        id: "search-1",
        type: "tool_call",
        name: "grep",
        status: "completed",
        arguments: JSON.stringify({ pattern: "cache_read" }),
      },
      {
        id: "search-2",
        type: "tool_call",
        name: "grep",
        status: "completed",
        arguments: JSON.stringify({ pattern: "cache_write" }),
      },
      {
        id: "search-3",
        type: "tool_call",
        name: "grep",
        status: "completed",
        arguments: JSON.stringify({ pattern: "cache_write" }),
      },
    ] satisfies ThreadItem[]);

    expect(segments).toMatchObject([
      {
        kind: "search",
        count: 3,
      },
    ]);
  });

  it("describes recognizable data-check commands by their purpose", () => {
    const [timeSegment] = buildToolActivityProcessSegments([
      {
        id: "time-1",
        type: "tool_call",
        name: "bash",
        status: "completed",
        arguments: JSON.stringify({ command: "date '+%H:%M'" }),
      },
    ] satisfies ThreadItem[]);
    expect(timeSegment).toMatchObject({
      kind: "command",
      text: "查看本地时间",
    });

    const [databaseSegment] = buildToolActivityProcessSegments([
      {
        id: "database-1",
        type: "tool_call",
        name: "bash",
        status: "completed",
        arguments: JSON.stringify({
          command: "python3 - <<'PY'\nimport sqlite3\nPY",
        }),
      },
    ] satisfies ThreadItem[]);
    expect(databaseSegment).toMatchObject({
      kind: "command",
      text: "操作本地数据库",
    });
  });

  it("describes package, download, and filesystem commands by their purpose", () => {
    const label = (command: string) => {
      const [segment] = buildToolActivityProcessSegments([
        {
          id: `cmd-${command}`,
          type: "tool_call",
          name: "bash",
          status: "completed",
          arguments: JSON.stringify({ command }),
        },
      ] satisfies ThreadItem[]);
      return segment.text;
    };

    expect(label("brew search herdr")).toBe("搜索软件包");
    expect(label("brew install herdr")).toBe("安装软件包");
    expect(label("npm uninstall herdr")).toBe("卸载软件包");
    expect(label("curl -L https://example.test/archive.tgz -o archive.tgz")).toBe("下载网络内容");
    expect(label("command -v herdr")).toBe("检查命令是否可用");
    expect(label("rm archive.tgz")).toBe("删除文件");
  });

  it("does not mislabel commands that merely mention sqlite3 or date", () => {
    const label = (command: string) => {
      const [segment] = buildToolActivityProcessSegments([
        {
          id: `cmd-${command.length}`,
          type: "tool_call",
          name: "bash",
          status: "completed",
          arguments: JSON.stringify({ command }),
        },
      ] satisfies ThreadItem[]);
      return segment;
    };

    expect(label("rm sessions.sqlite3")).toMatchObject({
      kind: "command",
      text: "删除文件",
    });
    expect(label("go test ./internal/sqlite3/...")).toMatchObject({
      kind: "command",
      text: "运行测试",
    });
    expect(label("npm run typecheck && date")).toMatchObject({
      kind: "command",
      text: "检查类型",
    });
    expect(label("sqlite3 state.db 'SELECT 1'")).toMatchObject({
      kind: "command",
      text: "操作本地数据库",
    });
  });

  it("shows the raw command when no purpose bucket matches", () => {
    const [segment] = buildToolActivityProcessSegments([
      {
        id: "cmd-raw-fallback",
        type: "tool_call",
        name: "bash",
        status: "completed",
        arguments: JSON.stringify({ command: "bun run codegen" }),
      },
    ] satisfies ThreadItem[]);
    expect(segment).toMatchObject({ kind: "command", text: "运行 bun run codegen" });
  });
});


describe("collectTurnSources", () => {
  it("returns an empty list when the turn has no web_search or web_fetch items", () => {
    expect(
      collectTurnSources([
        {
          id: "tool-1",
          type: "tool_call",
          name: "read_file",
          status: "completed",
          arguments: JSON.stringify({ path: "foo.ts" }),
        } satisfies ThreadItem,
      ]),
    ).toEqual([]);
  });

  it("collects one source per web_search hit with the canonical host", () => {
    expect(
      collectTurnSources([
        {
          id: "ws-1",
          type: "tool_call",
          name: "web_search",
          status: "completed",
          result: JSON.stringify({
            results: [
              {
                url: "https://www.anthropic.com/news/claude-opus-4-7",
                title: "Opus 4.7",
              },
              { url: "https://docs.anthropic.com/api", title: "API docs" },
            ],
          }),
        } satisfies ThreadItem,
      ]),
    ).toEqual([
      {
        url: "https://www.anthropic.com/news/claude-opus-4-7",
        host: "anthropic.com",
        title: "Opus 4.7",
        origin: "web_search",
      },
      {
        url: "https://docs.anthropic.com/api",
        host: "docs.anthropic.com",
        title: "API docs",
        origin: "web_search",
      },
    ]);
  });

  it("captures a web_fetch URL from arguments even when result has none", () => {
    expect(
      collectTurnSources([
        {
          id: "wf-1",
          type: "tool_call",
          name: "web_fetch",
          status: "completed",
          arguments: JSON.stringify({
            url: "https://platform.openai.com/docs/models",
          }),
          result: JSON.stringify({ status_code: 200, text: "..." }),
        } satisfies ThreadItem,
      ]),
    ).toEqual([
      {
        url: "https://platform.openai.com/docs/models",
        host: "platform.openai.com",
        origin: "web_fetch",
      },
    ]);
  });

  it("collapses multiple hits on the same host into a single favicon slot", () => {
    expect(
      collectTurnSources([
        {
          id: "ws-1",
          type: "tool_call",
          name: "web_search",
          status: "completed",
          result: JSON.stringify({
            results: [
              { url: "https://docs.anthropic.com/a", title: "Doc A" },
              { url: "https://docs.anthropic.com/b", title: "Doc B" },
            ],
          }),
        } satisfies ThreadItem,
      ]),
    ).toEqual([
      {
        url: "https://docs.anthropic.com/a",
        host: "docs.anthropic.com",
        title: "Doc A",
        origin: "web_search",
      },
    ]);
  });

  it("upgrades a slot's title when a later hit on the same host provides one", () => {
    expect(
      collectTurnSources([
        {
          id: "wf-1",
          type: "tool_call",
          name: "web_fetch",
          status: "completed",
          arguments: JSON.stringify({
            url: "https://www.anthropic.com/landing",
          }),
        } satisfies ThreadItem,
        {
          id: "ws-1",
          type: "tool_call",
          name: "web_search",
          status: "completed",
          result: JSON.stringify({
            results: [
              {
                url: "https://anthropic.com/news/claude-opus-4-7",
                title: "Claude Opus 4.7 迁移指南",
              },
            ],
          }),
        } satisfies ThreadItem,
      ]),
    ).toEqual([
      {
        url: "https://www.anthropic.com/landing",
        host: "anthropic.com",
        title: "Claude Opus 4.7 迁移指南",
        origin: "web_fetch",
      },
    ]);
  });

  it("preserves first-seen order across the turn", () => {
    const sources = collectTurnSources([
      {
        id: "ws-1",
        type: "tool_call",
        name: "web_search",
        status: "completed",
        result: JSON.stringify({
          results: [{ url: "https://example.com/a" }],
        }),
      } satisfies ThreadItem,
      {
        id: "wf-1",
        type: "tool_call",
        name: "web_fetch",
        status: "completed",
        arguments: JSON.stringify({ url: "https://other.test/page" }),
      } satisfies ThreadItem,
    ]);
    expect(sources.map((source) => source.host)).toEqual([
      "example.com",
      "other.test",
    ]);
  });

  it("skips web_search hits with no parseable URL", () => {
    expect(
      collectTurnSources([
        {
          id: "ws-1",
          type: "tool_call",
          name: "web_search",
          status: "completed",
          result: JSON.stringify({
            results: [
              { title: "no URL" },
              { url: "https://valid.example/x" },
            ],
          }),
        } satisfies ThreadItem,
      ]),
    ).toEqual([
      {
        url: "https://valid.example/x",
        host: "valid.example",
        origin: "web_search",
      },
    ]);
  });

});
