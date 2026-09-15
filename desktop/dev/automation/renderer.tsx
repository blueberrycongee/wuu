import * as React from "react";
import { createRoot } from "react-dom/client";
import { PluginHost, type PluginGenerationApi } from "../../src/renderer/plugins/PluginHost";
import { PluginViewContent, WorkbenchController } from "../../src/renderer/plugins/Workbench";
import source from "../../../internal/plugin/bundled/automation/desktop.js?raw";
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
import "../../src/renderer/styles.css";

const workspace = { id: "wuu", name: "wuu", root: "/projects/wuu", available: true };
let tasks = [
  { id: "brief", title: "每日简报", prompt: "汇总这个项目最近的变更、待办工作，以及今天需要我关注的事项。", cron: "0 8 * * 1-5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: false, workspace_mode: "shared", next_run_at: "2026-09-16T00:00:00Z" },
  { id: "review", title: "每周回顾", prompt: "回顾本周进展，整理已完成的工作、尚未解决的问题和下周重点。", cron: "0 16 * * 5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: true, workspace_mode: "worktree", next_run_at: "2026-09-18T08:00:00Z" },
];
const host = new PluginHost({ react: React,
  listWorkspaces: async () => ({ activeWorkspaceId: "wuu", workspaces: [workspace] }),
  listThreads: async () => [{ id: "chat", title: "检查自动化插件的执行与恢复流程", pinned: true, updatedAt: "2026-09-15T12:00:00Z" }],
  invokeRuntime: async ({ method, input }) => {
    const value = input as Record<string, any>;
    if (method === "automation.list") return { tasks, workspace };
    if (method === "automation.run.list") return { runs: [] };
    if (method === "automation.remove") { tasks = tasks.filter(task => task.id !== value.id); return { ok: true }; }
    const task = { ...(tasks.find(task => task.id === value.id) || {}), ...value, id: value.id || crypto.randomUUID(), ...(value.schedule ? { cron: value.schedule } : {}), ...(value.workspace ? { workspace_mode: value.workspace } : {}) };
    tasks = [...tasks.filter(item => item.id !== task.id), task] as typeof tasks;
    return task;
  },
});
await host.activateGeneration({ pluginId: "automation", generation: "preview", register: activate });
const controller = new WorkbenchController(host);
const style = document.createElement("style");
style.textContent = "html,body,#root { width:100%; height:100%; margin:0; } .plugin-view-content {height:100%;} body {background:var(--paper);} ";
document.head.append(style);
createRoot(document.getElementById("root")!).render(<PluginViewContent controller={controller} pluginId="automation" viewTypeId="automation.catalog" />);
