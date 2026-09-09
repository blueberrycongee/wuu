// Runs the production desktop service pool without an Electron window.
import { createInterface } from "node:readline";
import { AppServerClientPool } from "../src/main/appServerClients";
import { RemoteAppServerBridge } from "../src/main/remoteAppServerBridge";
import { GitService } from "../src/main/gitService";
import { requestRemoteFiles } from "../src/main/remoteFiles";
import { requestRemoteGit } from "../src/main/remoteGit";

const cwd = process.env.WUU_TEST_WORKSPACE!;
const context = { kind: "no_project" as const, cwd };
const send = (value: unknown) => process.stdout.write(JSON.stringify(value) + "\n");
const pool = new AppServerClientPool(() => context, () => cwd, event => {
  bridge.publish(event);
  send({ desktopEvent: event });
});
const bridge = new RemoteAppServerBridge((workdir, method, params, reply) => {
  if (workdir !== cwd) throw new Error("Unknown workspace");
  if (method === "shutdown") throw new Error("Cannot shut down shared service");
  if (method.startsWith("desktop/file/")) return Promise.resolve(requestRemoteFiles(context,method,params));
  if (method.startsWith("desktop/git/")) return Promise.resolve(requestRemoteGit(new GitService(() => context, () => pool.runningThreadCwds(), () => pool.threadCwdsForWorkdir(cwd)), method, params));
  return pool.requestInContext(context, method, params, reply);
});
await pool.request("initialize", { client: { name: "desktop-test" } });
send({ bridge: await bridge.start(cwd) });
const input = createInterface({ input: process.stdin });
input.on("line", line => {
  const request = JSON.parse(line);
  void pool.request(request.method, request.params).then(
    result => send({ desktopResponse: { id: request.id, result } }),
    error => send({ desktopResponse: { id: request.id, error: String(error) } }),
  );
});
const stop = () => { bridge.stop(); pool.shutdown(); input.close(); process.exit(0); };
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
