import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { access } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { AppServerClientPool } from "../main/appServerClients";
import { shutdownChild } from "../main/childShutdown";
import { PluginRuntimeRequests } from "../main/pluginRuntimeRequests";
import { ProjectManager, wuuHomePath } from "../main/projects";
import { RemoteAppServerBridge } from "../main/remoteAppServerBridge";
import { RemoteHostManager } from "../main/remoteControl";
import { requestRemoteWorkspace } from "../main/remoteWorkspace";
import { TerminalSessionManager } from "../main/terminalSessions";
import { APP_SERVER_PROTOCOL_VERSION, type PluginClientCancelParams, type PluginClientRequestParams } from "../shared/protocol";

async function main(): Promise<void> {
  const { values } = parseArgs({ options: {
    workdir: { type: "string" },
    core: { type: "string" },
    "web-root": { type: "string" },
    port: { type: "string", default: "8787" },
    "browser-port": { type: "string" },
    pair: { type: "boolean", default: false },
    help: { type: "boolean", default: false },
  } });
  if (values.help) {
    console.log("Wuu headless workspace host\nUsage: npm run remote:serve -- --workdir /path/to/project [--port 8787] [--pair]\nOptions: --core /path/to/wuu --web-root /path/to/built/web --browser-port LOCAL_PORT\nRun under a service manager or tmux to keep tasks alive after SSH disconnects.\nThe listener binds only to 127.0.0.1. Connect with wuu remote ssh --port 8787 user@host.");
    return;
  }
  const port = Number(values.port);
  const browserPort = Number(values["browser-port"] || port);
  if ([port, browserPort].some(value => !Number.isInteger(value) || value < 1 || value > 65535)) throw new Error("Port must be between 1 and 65535");
  const bundleDir = dirname(fileURLToPath(import.meta.url));
  const core = resolve(values.core || join(bundleDir, "../build/bin/wuu-core"));
  const webRoot = resolve(values["web-root"] || join(bundleDir, "../build/mobile-web"));
  const workdir = resolve(values.workdir || process.cwd());
  await Promise.all([access(core), access(join(webRoot, "index.html"))]);
  // All children use this exact build, including plugin helpers. No client-side
  // provider credentials or agent installation is copied to the remote host.
  process.env.WUU_DESKTOP_CORE = core;
  const projects = new ProjectManager();
  const initialize = () => ({ protocol_version: APP_SERVER_PROTOCOL_VERSION, client: { name: "wuu-ssh-workspace" } });
  const pool = new AppServerClientPool(
    () => projects.ensureRuntimeContext(), () => projects.activeWorkdir(),
    event => {
      bridge.publish(event);
      if (event.kind === "server-request") pool.rejectServerRequest(event.message.id, "This host has no native desktop window");
      if (event.kind === "server-error") console.error(event.message);
    }, undefined, initialize,
  );
  const plugins = new PluginRuntimeRequests(pool);
  const terminals = new TerminalSessionManager((peer, event) => bridge.notifyPeer(peer, "desktop/terminal/event", event));
  const bridge = new RemoteAppServerBridge((cwd, method, params, reply, peer) => {
    const owner = `remote:${peer}`;
    if (method === "plugin/client/cancel") return plugins.cancel(owner, params as PluginClientCancelParams);
    if (method === "initialize") params = initialize();
    return requestRemoteWorkspace(projects, pool, terminals, cwd, method, params, reply, peer,
      (context, method, params) => method === "plugin/client/request"
        ? plugins.request(owner, context, params as PluginClientRequestParams, reply)
        : pool.requestInContext(context, method, params, reply));
  }, peer => { terminals.stopForOwner(peer); plugins.closeOwner(`remote:${peer}`); });
  const startup = new AbortController();
  let stopping = false;
  let relay: ChildProcessWithoutNullStreams | undefined;
  let shutdown: Promise<void> | undefined;
  const host = new RemoteHostManager({
    resolveCommand: cwd => ({ command: core, args: [], cwd }),
    appServerEndpoint: () => bridge.currentEndpoint(),
    onEvent: event => {
      if (event.kind === "pair-uri" && !stopping) console.log(`Pair this browser (expires in 10 minutes):\nhttp://127.0.0.1:${browserPort}/#pair=${encodeURIComponent(event.uri)}`);
      else if (event.kind === "host-exit" && !stopping) { console.error("Remote host exited"); process.exitCode = 1; void stop(); }
      else if (event.kind === "host-log") console.error(event.message);
    },
  });
  const stop = (): Promise<void> => shutdown ??= (async () => {
    stopping = true;
    startup.abort(new Error("Remote host startup cancelled"));
    const hostStopped = host.stopHost();
    bridge.stop();
    terminals.cleanup();
    const results = await Promise.allSettled([
      hostStopped, pool.shutdown(),
      ...(relay ? [shutdownChild(relay, () => relay!.kill("SIGTERM"), 3000)] : []),
    ]);
    for (const result of results) if (result.status === "rejected") {
      console.error("Remote host shutdown failed", result.reason);
      process.exitCode = 1;
    }
  })();
  process.once("SIGINT", () => { void stop(); });
  process.once("SIGTERM", () => { void stop(); });
  process.once("SIGHUP", () => { void stop(); });
  try {
    const status = await host.status(workdir, startup.signal);
    if (status.account_server) {
      throw new Error("SSH workspaces use local pairing, but this WUU_HOME is signed in to a Wuu account. Start this service with a separate WUU_HOME (with its own Wuu configuration and history), or intentionally sign out of that Wuu account first. Existing account credentials have not been changed.");
    }
    if (stopping) return;
    projects.add(workdir);
    await bridge.start(workdir);
    if (stopping) { bridge.stop(); return; }
    relay = spawn(core, ["relay", "--addr", `127.0.0.1:${port}`, "--web-root", webRoot,
      "--state", join(wuuHomePath(), "ssh-relay-state.json")], { cwd: workdir, stdio: ["pipe", "pipe", "pipe"] });
    const child = relay;
    child.stderr.pipe(process.stderr);
    await new Promise<void>((ready, fail) => {
      const timer = setTimeout(() => finish(new Error("Remote listener startup timed out")), 15_000);
      let output = "";
      const finish = (error?: Error) => {
        clearTimeout(timer);
        child.stdout.off("data", onData); child.off("error", onError); child.off("exit", onExit);
        if (error) fail(error); else ready();
      };
      const onData = (data: Buffer) => { output = (output + data.toString()).slice(-4096); if (output.includes("connect url:")) finish(); };
      const onError = (error: Error) => finish(error);
      const onExit = () => finish(new Error("Remote listener exited before startup"));
      child.stdout.on("data", onData); child.once("error", onError); child.once("exit", onExit);
    });
    child.stdout.resume();
    child.on("error", error => { console.error(error.message); process.exitCode = 1; void stop(); });
    child.once("exit", () => { if (!stopping) { console.error("Remote listener exited"); process.exitCode = 1; void stop(); } });
    if (stopping) return;
    const relayURL = `ws://127.0.0.1:${port}/v1/connect`;
    host.startHost(workdir, { pair: values.pair, relay: `ws://127.0.0.1:${browserPort}/v1/connect`, localRelay: relayURL, reportReady: true });
    await host.waitForReady();
    if (values.pair) await host.waitForPairing();
    if (stopping) return;
    console.log(`Wuu workspace ready at http://127.0.0.1:${browserPort}/\nSSH: wuu remote ssh --port ${browserPort} --remote-port ${port} user@host\nStopping this service stops its running tasks. Disconnecting SSH does not.`);
  } catch (error) {
    const interrupted = stopping;
    await stop();
    if (!interrupted) throw error;
  }
}

void main().catch(error => { console.error(error instanceof Error ? error.message : String(error)); process.exitCode = 1; });
