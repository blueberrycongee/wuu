// Actual desktop bridge/pool fixture. Only synthetic data and an explicit core
// binary/config are accepted; no Electron installation or personal profile.
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { AppServerClientPool } from "../src/main/appServerClients";
import { RemoteAppServerBridge } from "../src/main/remoteAppServerBridge";
import { requestTrustedRemote, bindRemoteEventAuthorization } from "../src/main/trustedRemoteTransport";

const cwd = process.env.WUU_TRUSTED_WORKSPACE!;
const config = process.env.WUU_TRUSTED_CONFIG!;
if (!cwd || !config || !process.env.WUU_DESKTOP_CORE || !process.env.WUU_HOME) throw new Error("Isolated fixture environment required");
const context = { kind: "no_project" as const, cwd };
let coreProcesses = 0;
let bridge: RemoteAppServerBridge;
const pool = new AppServerClientPool(() => context, () => cwd, event => bridge?.publish(event), (command,args,options) => {
  coreProcesses++;
  return spawn(command,[...args,"--config",config],options);
});
bridge = new RemoteAppServerBridge(async (_workdir,method,params,reply,_peerID,_activation,peer) => {
  if (method.startsWith("desktop/") || method.startsWith("plugin/")) throw new Error("Fixture has no delegated OS provider");
  return requestTrustedRemote(pool,context,peer,method,params,reply);
}, undefined, (event,peer) => bindRemoteEventAuthorization(pool,event,peer));
await pool.requestInContext(context,"initialize",{});
const first = await pool.requestInContext<{thread:{id:string}}>(context,"thread/start",{});
const second = await pool.requestInContext<{thread:{id:string}}>(context,"thread/start",{});
await pool.requestInContext(context,"thread/rename",{thread_id:first.thread.id,title:"Allowed local session"});
await pool.requestInContext(context,"thread/rename",{thread_id:second.thread.id,title:"Private local session"});
const endpoint = await bridge.start(cwd);
process.stdout.write(JSON.stringify({endpoint,first:first.thread.id,second:second.thread.id,core_processes:coreProcesses})+"\n");
const input = createInterface({input:process.stdin});
for await (const line of input) {
  const request = JSON.parse(line);
  if(request.method==="fixture/close") break;
  try {
    const result = request.method === "fixture/process-count" ? coreProcesses : await pool.requestInContext(context,request.method,request.params);
    process.stdout.write(JSON.stringify({id:request.id,result})+"\n");
  } catch(error) {process.stdout.write(JSON.stringify({id:request.id,error:String(error)})+"\n");}
}
bridge.stop();
await pool.shutdown();
