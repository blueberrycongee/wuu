import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { once } from "node:events";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { Script, constants } from "node:vm";
import { createHash } from "node:crypto";
import { createUIComposition } from "./ApplicationShell";
import { DesktopPluginRuntime, desktopPluginHost as host } from "./DesktopPluginRuntime";
import type { ExtensionInventoryRecord, WuuDesktopApi } from "../../shared/protocol";
import type { PluginGenerationApi } from "../../../../packages/plugin-sdk/src";

// Invoked by additive-package.mjs after independently compiling the example
// packages against the SDK tarball. No source imports from the examples are used.
it("installed additive packages cooperate across the process and renderer boundary", { skip: !process.env.WUU_ADDITIVE_BINARY, timeout: 120_000 }, async () => {
  const fixture = process.env.WUU_ADDITIVE_PROJECT!;
  const output = process.env.WUU_ADDITIVE_OUTPUT!;
  const home = join(fixture, "home"), config = join(home, "config.json");
  await mkdir(home, { recursive: true });
  await writeFile(config, JSON.stringify({ default_provider: "fixture", providers: { fixture: { type: "openai-compatible", base_url: "http://127.0.0.1:1", api_key: "synthetic", model: "unused" } }, skills: { enabled: false } }));
  const child = spawn(process.env.WUU_ADDITIVE_BINARY!, ["app-server", "--workdir", fixture, "--config", config], {
    env: { ...process.env, HOME: fixture, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath }, stdio: ["pipe", "pipe", "pipe"],
  });
  const exited = once(child, "exit");
  const pending = new Map<string, { resolve(value: any): void; reject(error: Error): void; timer: ReturnType<typeof setTimeout> }>();
  const transcript: unknown[] = [], cases: string[] = [];
  let sequence = 0, stopped = false;
  child.stderr.on("data", data => transcript.push({ stderr: String(data) }));
  createInterface({ input: child.stdout }).on("line", line => {
    transcript.push({ received: line });
    const message = JSON.parse(line), waiter = pending.get(String(message.id));
    if (!waiter) return;
    pending.delete(String(message.id)); clearTimeout(waiter.timer);
    message.error ? waiter.reject(new Error(JSON.stringify(message.error))) : waiter.resolve(message.result);
  });
  child.on("exit", () => { stopped = true; for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(new Error("app-server exited")); } pending.clear(); });
  const request = (method: string, params: unknown = {}): Promise<any> => new Promise((resolve, reject) => {
    const id = String(++sequence), message = { id, method, params };
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 20_000);
    pending.set(id, { resolve, reject, timer }); transcript.push({ sent: message }); child.stdin.write(JSON.stringify(message) + "\n");
  });
  const originalWuu = window.wuu;
  const modules = new Map<string, unknown>(), handles: PluginGenerationApi[] = [];
  const watches = new Map<string, { id: string; fingerprint: string; graph_generation: string }>();
  window.wuu = {
    reportPluginDesktopActivation: (params: unknown) => request("plugin/desktop-activation", params),
    requestPluginRuntime: async (params: { id: string; fingerprint: string; graph_generation: string; request_id: string; method: string }) => {
      if (params.method === "watch") watches.set(params.request_id, params);
      try { return await request("plugin/client/request", params); }
      finally { watches.delete(params.request_id); }
    },
    cancelPluginRuntime: (params: unknown) => request("plugin/client/cancel", params),
    loadPluginDesktopModule: async (identity: { id: string; fingerprint: string }) => {
      const module = await request("plugin/desktop-module/read", identity);
      expect(createHash("sha256").update(module.source).digest("hex")).toBe(module.digest);
      const url = `${identity.id}:${identity.fingerprint}`;
      // Installed desktop entrypoints are import-free ESM bundles. Evaluate the
      // returned bytes, not a local copy. Use the Node loader outside jsdom.
      const sourceURL = "data:text/javascript;base64," + Buffer.from(module.source).toString("base64");
      const loaded = await new Script(`import(${JSON.stringify(sourceURL)})`, { importModuleDynamically: constants.USE_MAIN_CONTEXT_DEFAULT_LOADER }).runInThisContext();
      modules.set(url, { activate(api: PluginGenerationApi) { handles.push(api); return loaded.activate(api); } });
      return { ...identity, digest: module.digest, url };
    },
  } as unknown as WuuDesktopApi;
  const runtime = new DesktopPluginRuntime(host, async url => modules.get(url));
  const element = document.createElement("div"), root = createRoot(element); document.body.append(element);
  const { Slot } = createUIComposition(host);
  let inventory: ExtensionInventoryRecord[] = [];
  const sync = async (result: { extension_inventory: ExtensionInventoryRecord[] }) => {
    inventory = result.extension_inventory;
    // Keep the server's normal backend composition. Mount just the public
    // provider-owned panel here, not a second product application implementation.
    const ours = inventory.filter(item => item.provenance.plugin_id?.startsWith("additive-counter"));
    await act(async () => { expect(await runtime.sync(ours)).toEqual([]); });
    await host.flushActivationReports();
  };
  const install = async (name: string) => {
    let result: any;
    await act(async () => { result = await request("plugin/package/install", { path: join(fixture, name) }); expect(result.pending).toBe(false); await sync(result); });
    return result;
  };
  const remove = async (id: string) => { await act(async () => { await sync(await request("plugin/package/remove", { id })); }); };
  const manifest = async (name: string, change: (value: any) => void) => {
    const path = join(fixture, name, "plugin.json"), value = JSON.parse(await readFile(path, "utf8")); change(value); await writeFile(path, JSON.stringify(value));
  };
  const latest = (id: string) => handles.filter(api => api.manifestId === id).at(-1)!;
  const eventually = async (check: () => void | Promise<void>) => {
    const deadline = Date.now() + 10_000;
    for (;;) { try { await check(); return; } catch (error) { if (Date.now() > deadline) throw error; } await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); }); }
  };
  const count = () => element.querySelector("[data-counter-value]")?.textContent;
  const observed = () => [...element.querySelectorAll("[data-counter-controls] output")].map(node => node.textContent);
  const click = async (id: string) => {
    await eventually(() => expect((element.querySelector(`[data-counter-controls="${id}"] button`) as HTMLButtonElement)?.disabled).toBe(false));
    await act(async () => { (element.querySelector(`[data-counter-controls="${id}"] button`) as HTMLButtonElement).click(); });
  };
  try {
    const initialized = await request("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "additive-cooperation-conformance" } });
    const unrelated = (initialized.extension_inventory as ExtensionInventoryRecord[]).filter(item => item.runtime_state === "active").map(item => item.id);
    expect(unrelated.length).toBeGreaterThan(0);
    await act(async () => { root.render(<main><p data-unrelated>Unrelated application</p><Slot name="additive-counter/panel" /></main>); });
    await expect(install("additive-counter-controls")).rejects.toThrow(/missing_requirement/);
    expect(element.querySelector("[data-counter-controls]")).toBeNull();
    expect(element.querySelector("[data-unrelated]")?.textContent).toBe("Unrelated application");
    cases.push("missing required package rejects addon installation without disturbing application");

    await install("additive-counter");
    await install("additive-counter-controls");
    await eventually(() => expect(count()).toBe("0"));
    await eventually(() => expect(element.querySelector("[data-counter-controls] button")).not.toBeNull());
    await click("additive-counter-controls");
    await eventually(() => expect(count()).toBe("1"));
    await eventually(() => expect(watches.size).toBe(2));
    await eventually(() => expect(observed()).toEqual(["1"]));
    cases.push("install alone activates typed process state/action and nested owned UI");
    const stale = latest("additive-counter-controls");
    const priorGraph = inventory.find(item => item.provenance.plugin_id === stale.manifestId)!.graph_generation;
    const before = new Set((await request("plugin/executions/list")).executions.map((execution: { id: string }) => execution.id));
    const acceptedIdentity = { id: stale.pluginId, fingerprint: stale.generation, graph_generation: priorGraph, request_id: "captured-watch" };
    let acceptedSettled = false;
    const accepted = request("plugin/client/request", { ...acceptedIdentity, service: "example.counter", method: "watch", input: { after: 1 } })
      .then(value => { acceptedSettled = true; return value; }, error => { acceptedSettled = true; throw error; });
    const cancelledAccepted = expect(accepted).rejects.toThrow(/cancel/);
    let acceptedExecution: string;
    await eventually(async () => {
      const live = (await request("plugin/executions/list")).executions.find((execution: { id: string; plugin_id: string; generation_id: string }) => !before.has(execution.id) && execution.plugin_id === "additive-counter-controls" && execution.generation_id === priorGraph);
      expect(live).toBeDefined(); acceptedExecution = live.id;
    });
    await manifest("additive-counter-controls", value => { value.version = "1.0.1"; });
    await install("additive-counter-controls");
    expect(stale.signal.aborted).toBe(true);
    await eventually(() => expect([...watches.values()].some(watch => watch.graph_generation === priorGraph)).toBe(false));
    expect(acceptedSettled).toBe(false);
    expect((await request("plugin/executions/list")).executions).toContainEqual(expect.objectContaining({ id: acceptedExecution!, generation_id: priorGraph, retained: true }));
    expect((await request("plugin/client/cancel", acceptedIdentity)).cancelled).toBe(true);
    await cancelledAccepted;
    cases.push("accepted service task retains its captured graph through update until caller cancellation");
    await expect(stale.invokeService({ service: "example.counter", method: "add", params: { delta: 100 } })).rejects.toThrow(/no longer active/);
    await expect(request("plugin/client/request", { id: stale.pluginId, fingerprint: stale.generation, graph_generation: priorGraph, service: "example.counter", method: "add", input: { delta: 100 } })).rejects.toThrow(/no longer active/);
    await eventually(() => expect(element.querySelectorAll("[data-counter-controls]")).toHaveLength(1));
    cases.push("same-source addon update cleans old generation and rejects stale renderer/wire actions");

    await install("additive-counter-controls-peer");
    await eventually(() => expect([...element.querySelectorAll("[data-counter-controls]")].map(node => node.getAttribute("data-counter-controls"))).toEqual(["additive-counter-controls", "additive-counter-controls-peer"]));
    await click("additive-counter-controls-peer");
    await eventually(() => expect(count()).toBe("1"));
    await eventually(() => expect(observed()).toEqual(["1", "1"]));
    await expect(latest("additive-counter-controls").invokeService({ service: "example.counter", method: "add", params: { delta: "bad" } })).rejects.toThrow(/integer/);
    await click("additive-counter-controls");
    await eventually(() => expect(count()).toBe("2"));
    await eventually(() => expect(observed()).toEqual(["2", "2"]));
    cases.push("multiple addons have stable ordering and isolated service errors");

    const providerConsumer = latest("additive-counter-controls");
    await manifest("additive-counter", value => { value.version = "2.0.0"; });
    await expect(install("additive-counter")).rejects.toThrow(/version_mismatch/);
    expect(providerConsumer.signal.aborted).toBe(false);
    expect(element.querySelectorAll("[data-counter-controls]")).toHaveLength(2);
    expect(count()).toBe("2");
    cases.push("incompatible provider update is rejected atomically and preserves active consumers");
    await remove("additive-counter-controls");
    await remove("additive-counter-controls-peer");
    await install("additive-counter");
    await expect(install("additive-counter-controls")).rejects.toThrow(/version_mismatch/);
    await eventually(() => expect(count()).toBe("0"));
    expect(element.querySelector("[data-unrelated]")).not.toBeNull();
    cases.push("incompatible package blocks only addon installation, not owner or application");

    await manifest("additive-counter", value => { value.version = "1.1.0"; });
    await install("additive-counter");
    await install("additive-counter-controls");
    await manifest("additive-counter", value => { value.version = "1.2.0"; value.runtime.args = ["runtime-v2.mjs"]; value.desktop.required_services[0].major_version = 2; });
    await expect(install("additive-counter")).rejects.toThrow(/required service|requires service/);
    cases.push("required service-major mismatch rejects incompatible candidate");
    await manifest("additive-counter-controls", value => { value.version = "1.0.2"; value.dependencies[0].optional = true; value.desktop.required_services[0].required = false; });
    await install("additive-counter-controls");
    await install("additive-counter");
    await eventually(() => expect(element.querySelector("[data-counter-controls] [role=status]")?.textContent).toBe("Counter unavailable"));
    expect((element.querySelector("[data-counter-controls] button") as HTMLButtonElement).disabled).toBe(true);
    await expect(install("additive-counter-controls-peer")).rejects.toThrow(/required service|requires service/);
    cases.push("optional dependency keeps addon active while its unavailable feature is disabled");
    const unavailableHandle = latest("additive-counter-controls");
    await manifest("additive-counter", value => { value.version = "1.3.0"; value.runtime.args = ["runtime.mjs"]; value.desktop.required_services[0].major_version = 1; });
    await install("additive-counter");
    expect(unavailableHandle.signal.aborted).toBe(true);
    await expect(unavailableHandle.invokeService({ service: "example.counter", method: "add", params: { delta: 100 } })).rejects.toThrow(/no longer active/);
    await install("additive-counter-controls-peer");
    await eventually(() => expect(element.querySelectorAll("[data-counter-controls]")).toHaveLength(2));
    const removed = latest("additive-counter-controls");
    await remove("additive-counter-controls");
    expect(removed.signal.aborted).toBe(true);
    await expect(removed.invokeService({ service: "example.counter", method: "add", params: { delta: 100 } })).rejects.toThrow(/no longer active/);
    await eventually(() => expect(element.querySelectorAll("[data-counter-controls]")).toHaveLength(1));
    await click("additive-counter-controls-peer");
    await eventually(() => expect(count()).toBe("1"));
    const orphaned = latest("additive-counter-controls-peer");
    await remove("additive-counter");
    expect(orphaned.signal.aborted).toBe(true);
    await expect(orphaned.invokeService({ service: "example.counter", method: "add", params: { delta: 100 } })).rejects.toThrow(/no longer active/);
    await eventually(() => expect(watches.size).toBe(0));
    expect(inventory.find(item => item.provenance.plugin_id === "additive-counter-controls-peer")).toMatchObject({
      enabled: true, runtime_state: "inactive",
      activation_issues: [{ kind: "missing_requirement", related_plugin_id: "additive-counter" }],
    });
    expect(element.querySelectorAll("[data-counter-controls]")).toHaveLength(0);
    expect(element.querySelector("[data-unrelated]")).not.toBeNull();
    for (const id of unrelated) expect(inventory.find(item => item.id === id)?.runtime_state).toBe("active");
    cases.push("addon removal leaves peers working; owner removal removes nested UI and subscriptions");
    const saved = JSON.parse(await readFile(config, "utf8"));
    expect(saved.composition).toBeUndefined();
    cases.push("no provider replacement or composition override was written");
  } catch (error) {
    await writeFile(join(output, "result.json"), JSON.stringify({ passed: false, cases, failure: String(error) }, null, 2));
    throw error;
  } finally {
    await act(async () => root.unmount()); element.remove();
    try { await runtime.shutdown(); }
    finally {
      window.wuu = originalWuu;
      if (!stopped) child.stdin.end();
      const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
      const [code, signal] = await exited; clearTimeout(timer);
      expect({ code, signal }).toEqual({ code: 0, signal: null });
      await writeFile(join(output, "transcript.jsonl"), transcript.map(value => JSON.stringify(value)).join("\n") + "\n");
    }
  }
  await writeFile(join(output, "result.json"), JSON.stringify({ passed: true, cases, renderer: "production host and slot renderer in jsdom, not visual acceptance" }, null, 2));
});
