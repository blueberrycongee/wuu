import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { Script, constants } from "node:vm";
import { createHash } from "node:crypto";
import type { ExtensionInventoryRecord, WuuDesktopApi } from "../../shared/protocol";
import type { PluginGenerationApi } from "../../../../packages/plugin-sdk/src";
import { PluginHost } from "./PluginHost";
import { DesktopPluginRuntime } from "./DesktopPluginRuntime";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { WorkbenchController } from "./Workbench";
import { createPluginTranslator } from "./pluginI18n";

// Failure cases: split UI still invokes its old package, target follows a product
// ID instead of the selected surface, removing UI stops runtime work, or removing
// runtime crashes the shell / lets a stale UI perform mutations.
it("installed independent UI packages replace shipped presentation through selected services", { skip: !process.env.WUU_FEATURE_UI_BINARY, timeout: 120_000 }, async () => {
  const binary = process.env.WUU_FEATURE_UI_BINARY!, project = process.env.WUU_FEATURE_UI_PROJECT!, output = process.env.WUU_FEATURE_UI_OUTPUT!;
  const home = join(project, "home"), workspace = join(project, "workspace"), config = join(home, "config.json");
  await mkdir(home, { recursive: true }); await mkdir(workspace, { recursive: true });
  await writeFile(join(home, "projects.json"), JSON.stringify({ projects: [{ id: "acceptance-workspace", name: "Acceptance workspace", path: workspace }] }));
  const env = { ...process.env, HOME: project, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
  const base = { default_provider: "fixture", providers: { fixture: { type: "openai-compatible", base_url: "http://127.0.0.1:1", api_key: "synthetic", model: "unused" } }, skills: { enabled: false } };
  await writeFile(config, JSON.stringify(base));
  for (const name of ["acceptance-surfaces", "acceptance-goal-ui"]) execFileSync(binary, ["plugin", "install", "--json", join(project, name)], { cwd: project, env, stdio: "pipe" });
  const installed = JSON.parse(await readFile(config, "utf8"));
  installed.composition = { version: 5, profile: "desktop", patches: [{ modules: {
    "wuu.default-shell": false, "wuu.default-composer": false, "wuu.default-navigation": false,
    "acceptance-surfaces": true, "acceptance-goal-ui": true, goal: true, "goal-ui": true, automation: true, "automation-ui": true,
  } }], services: ["ui.shell", "ui.composer", "ui.navigation"].map(service => ({ service, major: 1, provider: "acceptance-surfaces" })) };
  await writeFile(config, JSON.stringify(installed));
  const child = spawn(binary, ["app-server", "--workspace-id", "acceptance-workspace", "--workdir", workspace, "--config", config], { env, stdio: ["pipe", "pipe", "pipe"] });
  const exited = once(child, "exit"), transcript: unknown[] = [], cases: string[] = [];
  const notifications: Array<{ method: string; params: any }> = [];
  const pending = new Map<string, { resolve(value: any): void; reject(error: Error): void; timer: ReturnType<typeof setTimeout> }>();
  let sequence = 0, stopped = false;
  child.stderr.on("data", data => transcript.push({ stderr: String(data) }));
  createInterface({ input: child.stdout }).on("line", line => {
    transcript.push({ received: line }); const message = JSON.parse(line), waiter = pending.get(String(message.id));
    if (message.method) notifications.push(message);
    if (!waiter) return; pending.delete(String(message.id)); clearTimeout(waiter.timer);
    message.error ? waiter.reject(new Error(JSON.stringify(message.error))) : waiter.resolve(message.result);
  });
  child.on("exit", () => { stopped = true; for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(new Error("app-server exited")); } pending.clear(); });
  const rpc = (method: string, params: unknown = {}): Promise<any> => new Promise((resolve, reject) => {
    const id = String(++sequence), request = { id, method, params };
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 25_000);
    pending.set(id, { resolve, reject, timer }); transcript.push({ sent: request }); child.stdin.write(JSON.stringify(request) + "\n");
  });
  let inventory: ExtensionInventoryRecord[] = [], initialized: any;
  const get = (id: string) => { const item = inventory.find(item => item.provenance.plugin_id === id); if (!item) throw new Error(`Missing ${id}: ${JSON.stringify(inventory)}`); return item; };
  const request = async (id: string, method: string, input?: unknown, service?: string) => {
    const item = get(id);
    return (await rpc("plugin/client/request", { id: item.id, fingerprint: item.fingerprint, graph_generation: item.graph_generation, request_id: `feature-${++sequence}`, method, input, ...(service ? { service } : {}) })).result;
  };
  const host = new PluginHost({ react: React,
    reportActivation: params => rpc("plugin/desktop-activation", params),
    invokeRuntime: async ({ pluginId, generation, graphGeneration, service, method, input, workspaceId, workspaceRoot, signal }) => {
      // Like the desktop owner router, select a backend before sending its RPC.
      // This fixture has one backend; workspace hints are not wire parameters.
      if (workspaceId && workspaceId !== "acceptance-workspace") throw new Error("Unknown fixture workspace");
      if (workspaceRoot && workspaceRoot !== workspace) throw new Error("Unknown fixture workspace root");
      const identity = { id: pluginId, fingerprint: generation, graph_generation: graphGeneration!, request_id: `renderer-${++sequence}` };
      const cancel = () => { void rpc("plugin/client/cancel", identity).catch(() => {}); };
      signal?.addEventListener("abort", cancel, { once: true });
      try { return (await rpc("plugin/client/request", { ...identity, service, method, input })).result; }
      finally { signal?.removeEventListener("abort", cancel); }
    },
    listWorkspaces: async () => {
      const list = await rpc("workspace/list");
      return {
        activeWorkspaceId: list.workspaces.find((item: { path: string }) => item.path === list.current)?.id,
        workspaces: list.workspaces.map((item: { id: string; name: string; path: string }) => ({ id: item.id, name: item.name, root: item.path, available: true })),
      };
    }, listThreads: async () => [],
  });
  const modules = new Map<string, any>(), byName = new Map<string, any>(), originalWuu = window.wuu;
  window.wuu = { loadPluginDesktopModule: async (identity: { id: string; fingerprint: string }) => {
    const module = await rpc("plugin/desktop-module/read", identity);
    expect(createHash("sha256").update(module.source).digest("hex")).toBe(module.digest);
    const url = `${identity.id}:${identity.fingerprint}`, source = "data:text/javascript;base64," + Buffer.from(module.source).toString("base64");
    const loaded = await new Script(`import(${JSON.stringify(source)})`, { importModuleDynamically: constants.USE_MAIN_CONTEXT_DEFAULT_LOADER }).runInThisContext();
    modules.set(url, loaded); byName.set(inventory.find(item => item.id === identity.id)!.provenance.plugin_id!, loaded);
    return { ...identity, digest: module.digest, url };
  } } as unknown as WuuDesktopApi;
  const runtime = new DesktopPluginRuntime(host, async url => modules.get(url)), controller = new WorkbenchController(host);
  const element = document.createElement("div"), root = createRoot(element); document.body.append(element);
  let threadID = "", renderRevision = 0;
  const render = async () => {
    const context = Object.freeze({});
    byName.get("acceptance-surfaces").configureFixture({ threadId: threadID, mainConversation: true, viewTypeId: "automation.catalog", viewContext: context });
    const surfaces = Object.fromEntries(Object.entries(initialized.application_surfaces ?? {}).map(([key, value]: [string, any]) => [key, { pluginId: value.plugin_id, generation: value.generation, id: value.provider_id }]));
    const selected = initialized.application_shell;
    const catalog = host.getViewTypes().find(item => item.pluginId === get("automation-ui").id && item.id === "automation.catalog"), Catalog = catalog?.render;
    await act(async () => root.render(<React.Fragment key={++renderRevision}>
      <ApplicationShellOutlet host={host} binding={{ pluginId: selected.plugin_id, generation: selected.generation, id: selected.provider_id }} surfaceBindings={surfaces} />
      {Catalog && catalog ? <Catalog context={context} locale="en-US" translate={createPluginTranslator(host, "en-US")} host={controller.createRendererHostAPI(catalog.pluginId, catalog.generation)} /> : null}
    </React.Fragment>));
  };
  const initialize = () => rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "feature-ui-acceptance" } });
  const sync = async (committed?: any) => {
    initialized = committed ?? await initialize();
    inventory = initialized.extension_inventory;
    const names = new Set(["goal-ui", "automation-ui", "acceptance-surfaces", "acceptance-goal-ui"]);
    await act(async () => { expect(await runtime.sync(inventory.filter(item => names.has(item.provenance.plugin_id ?? "")))).toEqual([]); });
    await host.flushActivationReports();
  };
  const eventually = async (check: () => void | Promise<void>) => {
    const deadline = Date.now() + 12_000;
    for (;;) { try { await check(); return; } catch (error) { if (Date.now() >= deadline) throw error; } await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); }); }
  };
  const toggle = async (id: string, enabled: boolean) => {
    // An explicit composition owns its selections. Change that public config
    // rather than disabling a package while still requiring it in the graph.
    const current = JSON.parse(await readFile(config, "utf8"));
    current.composition.patches.push({ modules: { [id]: enabled } });
    const previousGeneration = get(id).graph_generation, notificationStart = notifications.length;
    await writeFile(config, JSON.stringify(current));
    // The config watcher owns this transaction. An explicit catalog refresh
    // races that mutation; observe its completed publication instead of issuing
    // or retrying a second mutation against an uncertain generation.
    let committed: any;
    await eventually(async () => {
      const observed = notifications.slice(notificationStart);
      expect(observed.filter(event => event.method === "config/error")).toEqual([]);
      expect(observed.some(event => event.method === "config/changed")).toBe(true);
      const next = await initialize();
      const selected = next.extension_inventory.find((item: ExtensionInventoryRecord) => item.provenance.plugin_id === id);
      expect(selected.graph_generation).not.toBe(previousGeneration);
      expect(selected.runtime_state).toBe(enabled ? "active" : "inactive");
      committed = next;
    });
    await sync(committed); await render();
  };
  try {
    await sync();
    expect(initialized.application_shell.plugin_id).toBe(get("acceptance-surfaces").id);
    threadID = (await rpc("thread/start", {})).thread.id;
    await request("goal", "create_goal", { thread_id: threadID, objective: "Preserve goal across independent UIs" });
    await request("goal", "pause", { thread_id: threadID });
    await request("automation-ui", "request", { method: "automation.create", input: { title: "Independent sidebar schedule", prompt: "Synthetic scheduled task", cron: "0 9 * * *", timezone: "UTC", durable: true, recurring: true } }, "automation.scheduler");
    await render();
    await eventually(() => expect(element.querySelector('[data-independent-composer] .plugin-goal')?.textContent).toContain("Preserve goal"));
    await eventually(() => expect(element.querySelector('[data-independent-navigation]')?.textContent).toContain("Independent sidebar schedule"));
    cases.push("real goal-ui and automation-ui mount in independently compiled selected composer/navigation package");
    await toggle("goal-ui", false);
    expect(element.querySelector(".plugin-goal")).toBeNull();
    expect(get("goal").runtime_state).toBe("active");
    await eventually(() => expect(element.querySelector('[data-independent-goal-ui] output')?.textContent).toContain("Preserve goal"));
    await act(async () => (element.querySelector('[data-independent-goal-ui] button') as HTMLButtonElement).click());
    await eventually(() => expect(element.querySelector('[data-independent-status]')?.textContent).toBe("paused"));
    expect((await request("goal", "get_goal", { thread_id: threadID })).goal.objective).toBe("Preserve goal across independent UIs");
    cases.push("disabling shipped goal UI leaves goal runtime/state intact and independent UI consumes the same service");
    await toggle("goal-ui", true); await toggle("goal", false);
    await eventually(() => expect(element.querySelector('.plugin-goal [role="alert"]')?.textContent).toMatch(/unavailable|disabled|provider|service/i));
    const drawer = element.querySelector<HTMLButtonElement>('.plugin-goal button[aria-expanded]')!;
    await act(async () => drawer.click());
    expect(element.querySelector<HTMLTextAreaElement>('.plugin-goal textarea')?.disabled).toBe(true);
    expect(element.querySelector<HTMLButtonElement>('[data-independent-goal-ui] button')?.disabled).toBe(true);
    expect(element.querySelector('[data-independent-shell]')?.textContent).toBe("Independent shell remains available");
    expect(host.getGenerationDiagnostics(get("acceptance-surfaces").id, get("acceptance-surfaces").fingerprint!).some(item => item.kind === "render")).toBe(false);
    cases.push("disabling real goal runtime leaves UI error/disabled writes and unrelated shell/nav available without fallback");
    await toggle("automation-ui", false);
    expect(get("automation").runtime_state).toBe("active");
    expect(element.querySelector('.plugin-automation')).toBeNull();
    cases.push("disabling automation presentation leaves scheduler active");
    await writeFile(join(output, "result.json"), JSON.stringify({ passed: true, cases, limitation: "Production renderer host and real Go app-server in jsdom; not pixel/desktop acceptance." }, null, 2));
  } catch (error) { await writeFile(join(output, "result.json"), JSON.stringify({ passed: false, cases, error: String(error) }, null, 2)); throw error; }
  finally {
    await act(async () => root.unmount()); element.remove(); controller.dispose(); await runtime.shutdown(); window.wuu = originalWuu;
    if (!stopped) child.stdin.end(); const kill = setTimeout(() => child.kill("SIGKILL"), 5000); await exited; clearTimeout(kill);
    await writeFile(join(output, "transcript.jsonl"), transcript.map(value => JSON.stringify(value)).join("\n") + "\n");
  }
});
