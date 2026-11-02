import { afterEach, describe, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import { steerOrQueueIsolatedTurn } from "./isolatedTurnSubmission";
import { ArtifactSources } from "./artifactSources";
import { AppServerResponseError } from "./appServerErrors";
import { RemoteAppServerClient } from "./remoteAppServerClient";
import { AppServerClientPool } from "./appServerClients";
import { requestTrustedRemote, type TrustedRemotePeer } from "./trustedRemoteTransport";
import { ProtocolClient, type ProtocolEnvelope } from "../../../clients/core/src/rpc";
import type { RuntimeContext, ServerEvent } from "../shared/protocol";
import { type DesktopSessionConnection, type SessionConnectionHooks, type SessionRoute, parseSessionRoutes } from "./sessionRoutes";

// Admission, host identity, reconnect continuity and reverse-RPC authority are
// public contracts. The fixture uses the actual protocol client and pool.
class Peer implements DesktopSessionConnection {
  readonly sent: ProtocolEnvelope[] = [];
  readonly stopped = vi.fn(async () => { this.proto.close("detached"); });
  proto = new ProtocolClient(env => { this.sent.push(env); }, {
    onNotification: (method, params, workdir) => this.hooks.onNotification(method, params, workdir),
    onServerRequest: req => this.hooks.onServerRequest(req),
  });
  constructor(readonly hooks: SessionConnectionHooks) {}
  start() { this.hooks.onAttach({ session: "connection-1", resumed: false }); }
  stop = this.stopped;
  waitAttached = async () => {};
  rpc = () => this.proto;
  reply(result: unknown) { this.proto.feed({ id: this.sent.at(-1)?.id, result }); }
  notify(method: string, params: unknown) { this.proto.feed({ method, params }); }
}
const remote = (host: string): SessionRoute => ({ kind: "remote", execution_host: host, workdir: "/remote/project", connector: { kind: "keychain", service: "synthetic" } });
const context: RuntimeContext = { kind: "no_project", cwd: "/display/a" };
const pools: AppServerClientPool[] = [];
afterEach(async () => { await Promise.all(pools.splice(0).map(pool => pool.shutdown())); });
function fixture(select: (context: RuntimeContext) => SessionRoute = () => remote("host-a"), attached?: Promise<void>) {
  const peers: Peer[] = [], events: ServerEvent[] = [];
  const spawn = vi.fn(() => { throw new Error("Local process must not start"); });
  const pool = new AppServerClientPool(() => context, () => context.cwd, event => events.push(event), spawn,
    undefined, undefined, { select, connect: (_route, hooks) => { const peer = new Peer(hooks); if(attached)peer.waitAttached=()=>attached; peers.push(peer); return peer; } });
  pools.push(pool);
  return { pool, peers, events, spawn };
}
async function sent(peer: () => Peer | undefined) { await vi.waitFor(() => expect(peer()?.sent.length).toBeGreaterThan(0)); return peer()!; }

describe("desktop remote session routing", () => {
  it("preserves explicit protocol refusal codes across the remote adapter",async()=>{
    const {pool,peers}=fixture();const request=pool.request("turn/steer",{thread_id:"original",expected_turn_id:"review",client_id:"same",prompt:"question"});
    const rejected=expect(request).rejects.toMatchObject({code:"isolated_turn_requires_queue"});
    const peer=await sent(()=>peers[0]);peer.proto.feed({id:peer.sent.at(-1)!.id,error:{code:"isolated_turn_requires_queue",message:"isolated"}});
    await rejected;await expect(request).rejects.toBeInstanceOf(AppServerResponseError);
  });
  it("never moves the isolated-turn queue fallback to a replacement receiver",async()=>{
    let host="host-a";const {pool,peers}=fixture(()=>remote(host));
    const request=pool.bindSessionRequest(context,"original");
    const pending=steerOrQueueIsolatedTurn(request,{thread_id:"original",expected_turn_id:"review",client_id:"same",prompt:"question"});
    const rejected=expect(pending).rejects.toThrow(/changed|available/);
    const peer=await sent(()=>peers[0]);host="host-b";
    peer.proto.feed({id:peer.sent.at(-1)!.id,error:{code:"isolated_turn_requires_queue",message:"isolated"}});
    await rejected;expect(peers).toHaveLength(1);expect(peer.sent.map(item=>item.method)).toEqual(["turn/steer"]);
  });
  it("fails closed for disabled/invalid routes and missing credentials", async () => {
    expect(() => parseSessionRoutes({ version: 2 })).toThrow();
    expect(() => parseSessionRoutes({ version: 1, default: { kind: "remote", execution_host: "local" } })).toThrow();
    const { pool, spawn } = fixture(() => ({ kind: "disabled", reason: "provider disabled" }));
    await expect(pool.request("thread/list")).rejects.toThrow("disabled");
    expect(spawn).not.toHaveBeenCalled();
  });

  it.each([
    { user_space_id: "other", product_domain: "personal" },
    { user_space_id: "local", product_domain: "coding" },
    {},
  ])("rejects unverified remote user hosts before publishing or admitting data (%j)", async identity => {
    const { pool, peers, events, spawn } = fixture();
    const user = { kind: "user" as const, cwd: "/display/personal", user_space_id: "local", product_domain: "personal" as const, composition_profile: "personal" };
    await expect(pool.requestInContext(user, "user-question/list")).rejects.toThrow("user_host_initialization_required");
    expect(peers[0].sent).toHaveLength(0);
    const observed = vi.fn();
    const initialized = pool.requestInContext(user, "initialize", { protocol_version: "wuu-app-server/v0.1" }, observed);
    const refused = expect(initialized).rejects.toThrow("user_host_binding_mismatch");
    const peer = await sent(() => peers[0]);
    const request = peer.sent.at(-1)!;
    expect(request.params).toMatchObject({ user_space_id: "local", product_domain: "personal", composition_profile: "personal" });
    peer.notify("item/agentMessage/delta", { thread_id: "private", delta: "unverified" });
    expect(events.some(event => event.kind === "notification")).toBe(false);
    const reverse = await peer.hooks.onServerRequest({ id: "unverified", method: "approval/request" });
    expect(reverse.error?.code).toBe("permission_denied");
    peer.proto.feed({ id: request.id, result: { ...identity, workspace_root: "/remote/project", extension_inventory: [] } });
    await refused;
    expect(observed).not.toHaveBeenCalled();
    const count = peer.sent.length;
    await expect(pool.requestInContext(user, "plugin/client/request", { id: "personal" })).rejects.toThrow("user_host_binding_mismatch");
    await expect(pool.requestInContext(user, "initialize", {})).rejects.toThrow("user_host_binding_mismatch");
    expect(peer.sent).toHaveLength(count); expect(spawn).not.toHaveBeenCalled();
  });

  it("captures immutable user identity and never revives a rejected RPC with a concurrent handshake", async () => {
    const user = { userSpaceId: "local", productDomain: "personal", compositionProfile: "personal" };
    let peer!: Peer;
    const route = remote("host-a"); if (route.kind !== "remote") throw new Error("remote fixture required");
    const client = new RemoteAppServerClient("/display/personal", "", route, (_route, hooks) => peer = new Peer(hooks), () => {}, () => {}, () => true, user);
    user.userSpaceId = "changed"; user.compositionProfile = "changed";
    try {
      const observed = vi.fn();
      const first = client.request("initialize", {}, observed), rejected = expect(first).rejects.toThrow("user_host_binding_mismatch");
      await sent(() => peer);
      expect(peer.sent[0].params).toMatchObject({ user_space_id: "local", product_domain: "personal", composition_profile: "personal" });
      const second = client.request("initialize", {}, observed), alsoRejected = expect(second).rejects.toThrow("user_host_binding_mismatch");
      await vi.waitFor(() => expect(peer.sent).toHaveLength(2));
      peer.proto.feed({ id: peer.sent[0].id, result: { user_space_id: "foreign", product_domain: "personal" } }); await rejected;
      peer.proto.feed({ id: peer.sent[1].id, result: { user_space_id: "local", product_domain: "personal" } }); await alsoRejected;
      expect(observed).not.toHaveBeenCalled();
      await expect(client.request("user-question/list")).rejects.toThrow("user_host_binding_mismatch");
      expect(peer.sent).toHaveLength(2);
    } finally { await client.dispose(); }
  });

  it("keeps user verification on resumed RPCs and requires a new handshake for a replacement RPC", async () => {
    const { pool, peers, events, spawn } = fixture();
    const user = { kind: "user" as const, cwd: "/display/personal", user_space_id: "local", product_domain: "personal" as const, composition_profile: "personal" };
    const initialize = () => pool.requestInContext(user, "initialize", { protocol_version: "wuu-app-server/v0.1" });
    const first = initialize(); const peer = await sent(() => peers[0]);
    peer.reply({ user_space_id: "local", product_domain: "personal", workspace_root: "/remote/project" });
    expect(await first).toMatchObject({ workspace_root: user.cwd });
    const bound = pool.bindContextRequest(user); await Promise.resolve();
    const beforeConflict = peer.sent.length;
    await expect(bound("user-question/list", { user_space_id: "foreign" })).rejects.toThrow("user_host_binding_mismatch");
    await expect(bound("initialize", { composition_profile: "coding" })).rejects.toThrow("user_host_binding_mismatch");
    expect(peer.sent).toHaveLength(beforeConflict);
    const pending = bound("user-question/list");
    await vi.waitFor(() => expect(peer.sent.at(-1)?.method).toBe("user-question/list"));
    peer.hooks.onDetach(); peer.hooks.onAttach({ session: "connection-1", resumed: true });
    peer.reply({ questions: [] }); expect(await pending).toEqual({ questions: [] });
    expect(peer.sent.filter(frame => frame.method === "initialize")).toHaveLength(1);
    const old = peer.proto;
    peer.proto = new ProtocolClient(env => peer.sent.push(env), { onNotification: (method, params, workdir) => peer.hooks.onNotification(method, params, workdir) });
    old.close("fresh generation"); peer.hooks.onAttach({ session: "connection-2", resumed: false });
    await expect(bound("user-question/list")).rejects.toThrow(/generation|owning/);
    const sentCount = peer.sent.length;
    await expect(pool.requestInContext(user, "user-question/list")).rejects.toThrow("user_host_initialization_required");
    peer.notify("item/agentMessage/delta", { thread_id: "private", delta: "before-revalidation" });
    expect(events.some(event => event.kind === "notification")).toBe(false);
    expect(peer.sent).toHaveLength(sentCount);
    const second = initialize();
    await vi.waitFor(() => expect(peer.sent.length).toBe(sentCount + 1));
    peer.reply({ user_space_id: "local", product_domain: "personal" }); await second;
    const read = pool.requestInContext(user, "user-question/list");
    await vi.waitFor(() => expect(peer.sent.at(-1)?.method).toBe("user-question/list"));
    peer.reply({ questions: [] }); expect(await read).toEqual({ questions: [] });
    expect(spawn).not.toHaveBeenCalled(); expect(peers).toHaveLength(1);
  });

  it("routes same-named sessions to their execution host and binds attachment reads to the owner", async () => {
    const { pool, peers, spawn } = fixture(c => remote(c.cwd === context.cwd ? "host-a" : "host-b"));
    const other: RuntimeContext = { kind: "no_project", cwd: "/display/b" };
    const a = pool.requestInContext(context, "turn/start", { thread_id: "same" });
    (await sent(() => peers[0])).notify("turn/started", { thread_id: "same" }); peers[0].reply({ host: "a" }); await a;
    const b = pool.requestInContext(other, "turn/start", { thread_id: "same" });
    (await sent(() => peers[1])).notify("turn/started", { thread_id: "same" }); peers[1].reply({ host: "b" }); await b;
    expect(() => pool.bindSessionRequest({ ...context, session_locator: { version: 1, execution_host: "host-b", session_id: "same" } }, "same")).toThrow("locator");
    const bound = pool.bindSessionRequest({ ...context, session_locator: { version: 1, execution_host: "host-a", session_id: "same" } }, "same");
    const read = bound("artifact/source/read", { thread_id: "same" });
    await vi.waitFor(() => expect(peers[0].sent.at(-1)?.method).toBe("artifact/source/read"));
    peers[0].reply({ host: "a" }); expect(await read).toEqual({ host: "a" });
    expect(peers[1].sent).toHaveLength(1); expect(spawn).not.toHaveBeenCalled();
  });

  it("streams selected artifact leases through their captured host without local fallback", async () => {
    let selected = remote("host-a");
    const {pool,peers,spawn}=fixture(()=>selected);
    const locator={version:1 as const,execution_host:"host-a",session_id:"same"};
    const request=pool.bindSessionRequest({...context,session_locator:locator},"same");
    const sources=new ArtifactSources(); const bytes=Buffer.from("remote-only");const sha256=createHash("sha256").update(bytes).digest("hex");
    const acquiring=sources.acquire({uri:"cloud:opaque",threadId:"same",sha256,locator},request,()=>true);
    const peer=await sent(()=>peers[0]);
    expect(peer.sent.at(-1)?.method).toBe("thread/artifact/resolve");
    peer.reply({generation_id:"provider-1",artifact:{id:"opaque ID",uri:"cloud:opaque",name:"report.txt",mime_type:"text/plain",size:bytes.length,sha256}});
    const lease=await acquiring;
    const response=await sources.response(new Request(lease.url,{headers:{range:"bytes=0-5"}}));
    const reading=response.text();
    await vi.waitFor(()=>expect(peer.sent.at(-1)?.method).toBe("thread/artifact/read"));
    peer.reply({data:bytes.subarray(0,6).toString("base64"),mime_type:"text/plain",offset:0,total:bytes.length,sha256});
    expect(await reading).toBe("remote");
    const previousRead=peer.sent.at(-1)?.id;
    const pending=(await sources.response(new Request(lease.url))).text();
    const canceled=expect(pending).rejects.toThrow();
    await vi.waitFor(()=>expect(peer.sent.at(-1)?.id).not.toBe(previousRead));
    const dispatched=peer.sent.at(-1)!;
    selected=remote("host-b");
    await expect((await sources.response(new Request(lease.url))).text()).rejects.toThrow();
    sources.release(lease.leaseId);
    await vi.waitFor(()=>expect(peer.sent.at(-1)?.method).toBe("thread/artifact/cancel"));
    expect(peer.sent.at(-1)?.params).toEqual({thread_id:"same",request_id:(dispatched.params as {request_id:string}).request_id});
    peer.reply({ok:true});
    peer.proto.feed({id:dispatched.id,error:{code:"canceled",message:"canceled"}});await canceled;
    sources.release(lease.leaseId);
    expect(peer.sent.filter(frame=>frame.method==="thread/artifact/cancel")).toHaveLength(1);
    expect(peers).toHaveLength(1);expect(spawn).not.toHaveBeenCalled();
  });

  it("does not admit queued artifact metadata after source cancellation and route retirement", async () => {
    let attach!:()=>void; const attached=new Promise<void>(resolve=>{attach=resolve;});
    let selected=remote("host-a");const {pool,peers,spawn}=fixture(()=>selected,attached);
    const sources=new ArtifactSources();const request=pool.bindSessionRequest(context,"same");
    const acquiring=sources.acquire({requestId:"queued-metadata",threadId:"same",uri:"cloud:opaque",sha256:"a".repeat(64)},request,()=>true);
    const canceled=expect(acquiring).rejects.toThrow();
    await vi.waitFor(()=>expect(peers).toHaveLength(1));expect(peers[0].sent).toHaveLength(0);
    sources.release("queued-metadata");selected=remote("host-b");attach();
    await canceled;
    await pool.shutdown();
    // Neither metadata/read nor a cancel for a never-admitted operation reaches
    // the retired connection after its delayed attachment finally completes.
    expect(peers[0].sent).toHaveLength(0);
    expect(peers).toHaveLength(1);expect(spawn).not.toHaveBeenCalled();
  });

  it("fences old responses and notifications when the selected route changes", async () => {
    let selected = remote("host-a");
    const { pool, peers, events } = fixture(() => selected);
    const onResponse = vi.fn();
    const old = pool.requestInContext(context, "turn/start", { thread_id: "same" }, onResponse);
    const oldResult = expect(old).rejects.toThrow(/unknown|changed/i);
    await sent(() => peers[0]); selected = remote("host-b");
    peers[0].reply({ accepted: true }); await oldResult;
    peers[0].notify("item/agentMessage/delta", { thread_id: "same", delta: "stale" });
    expect(onResponse).not.toHaveBeenCalled();
    expect(events.filter(event => event.kind === "notification")).toHaveLength(0);
    const next = pool.request("thread/list"); (await sent(() => peers[1])).reply({ threads: [] }); await next;
  });

  it("fences captured ownership when a selector mutates the same route object", async () => {
    const selected=remote("host-a");
    if(selected.kind!=="remote")throw new Error("remote fixture required");
    const {pool,peers,events}=fixture(()=>selected);
    const observed=vi.fn();
    const pending=pool.requestInContext(context,"thread/resume",{session_id:"same"},observed);
    const denied=expect(pending).rejects.toThrow(/unknown|changed|available/i);
    const first=await sent(()=>peers[0]);
    selected.execution_host="host-b";
    selected.connector={kind:"keychain",service:"replacement"};
    first.reply({thread:{id:"same",secret:"old-host"}});await denied;
    first.notify("thread/updated",{thread:{id:"same",secret:"old-host"}});
    expect(observed).not.toHaveBeenCalled();
    expect(events.filter(event=>event.kind==="notification")).toHaveLength(0);
    const next=pool.request("thread/list");(await sent(()=>peers[1])).reply({threads:[]});await next;
    expect(peers).toHaveLength(2);
  });

  it("preserves admitted requests across resumed attach and fences bound readers on a fresh generation", async () => {
    const { pool, peers } = fixture();
    const turn = pool.request("turn/start", { thread_id: "same" });
    const peer = await sent(() => peers[0]);
    const bound = pool.bindSessionRequest(context, "same");
    await Promise.resolve();
    peer.hooks.onDetach(); peer.hooks.onAttach({ session: "connection-1", resumed: true });
    peer.reply({ accepted: true }); expect(await turn).toEqual({ accepted: true });
    expect(peer.sent.filter(env => env.method === "turn/start")).toHaveLength(1);
    const old = peer.proto;
    peer.proto = new ProtocolClient(env => peer.sent.push(env));
    old.close("fresh generation"); peer.hooks.onAttach({ session: "connection-2", resumed: false });
    await expect(bound("artifact/source/read")).rejects.toThrow(/generation|owning/);
    const status = pool.request("thread/read", { thread_id: "same" });
    await vi.waitFor(() => expect(peer.sent.at(-1)?.method).toBe("thread/read"));
    peer.reply({ state: "idle" }); expect(await status).toEqual({ state: "idle" });
  });

  it("never forwards local validation capabilities to a selected remote connector", async () => {
    const { pool, peers } = fixture();
    const authority: TrustedRemotePeer = { version: 1, peer_id: "trusted-peer", owner: false,
      principal: {kind:"agent",issuer:"fixture",tenant:"tenant",subject:"reader"},
      validation: {address:"127.0.0.1:1",token:"x".repeat(32)} };
    await expect(requestTrustedRemote(pool,context,authority,"thread/resume",{session_id:"same"})).rejects.toThrow("local stdio");
    expect(peers).toHaveLength(0);
  });

  it("releases only a pending owned operation on its old connection before retirement", async () => {
    let selected=remote("host-a");
    const {pool,peers}=fixture(()=>selected);
    const bound=pool.bindSessionRequest(context,"same");
    const cancel={thread_id:"same",request_id:"operation-a"};
    const operation=bound.owned("thread/artifact/read",{...cancel},{method:"thread/artifact/cancel",params:cancel});
    const outcome=expect(operation.result).rejects.toThrow(/unknown|changed|closed|generation/i);
    const old=await sent(()=>peers[0]);
    cancel.request_id="must-not-be-used";
    selected=remote("host-b");
    const replacement=pool.request("thread/list");
    await vi.waitFor(()=>expect(old.sent.at(-1)?.method).toBe("thread/artifact/cancel"));
    expect(old.sent.at(-1)?.params).toEqual({thread_id:"same",request_id:"operation-a"});
    expect(old.stopped).not.toHaveBeenCalled();
    const fresh=await sent(()=>peers[1]);fresh.reply({threads:[]});await replacement;
    old.reply({cancelled:true});await outcome;
    await vi.waitFor(()=>expect(old.stopped).toHaveBeenCalledOnce());
    const count=old.sent.length;await operation.release();await operation.release();
    expect(old.sent).toHaveLength(count);
    expect(fresh.sent.some(message=>message.method==="thread/artifact/cancel")).toBe(false);
  });

  it("reports an admitted request with no confirmed reply as unknown after a fresh connection", async () => {
    const { pool, peers, spawn } = fixture();
    const turn = pool.request("turn/start", { thread_id: "same" });
    const result = expect(turn).rejects.toThrow(/outcome is unknown/);
    const peer = await sent(() => peers[0]); peer.proto.close("fresh generation");
    await result; expect(spawn).not.toHaveBeenCalled();
    expect(peer.sent).toHaveLength(1);
  });

  it("does not use local execution when the selected connector fails before admission", async () => {
    const spawn = vi.fn(() => { throw new Error("local spawn forbidden"); });
    const pool = new AppServerClientPool(() => context, () => context.cwd, () => {}, spawn,
      undefined, undefined, { select: () => remote("host-a"), connect: () => { throw new Error("remote offline"); } });
    pools.push(pool);
    await expect(pool.request("thread/list")).rejects.toThrow("remote offline");
    expect(spawn).not.toHaveBeenCalled();
  });

  it("detaches on shutdown and denies remote desktop control while routing safe reverse requests", async () => {
    const { pool, peers, events } = fixture();
    const req = pool.request("thread/list"); const peer = await sent(() => peers[0]); peer.reply({ threads: [] }); await req;
    peer.proto.feed({ id: "unsafe", method: "browser/navigate", params: { url: "https://example.test" } });
    await vi.waitFor(() => expect(peer.sent.find(env => env.id === "unsafe")?.error?.code).toBe("permission_denied"));
    peer.proto.feed({ id: "safe", method: "approval/request", params: { prompt: "synthetic" } });
    await vi.waitFor(() => expect(events.some(event => event.kind === "server-request")).toBe(true));
    const event = events.find(event => event.kind === "server-request");
    if (event?.kind !== "server-request") throw new Error("missing reverse request");
    pool.respondToServerRequest(event.message.id, { approved: false });
    await vi.waitFor(() => expect(peer.sent.find(env => env.id === "safe")?.result).toEqual({ approved: false }));
    await pool.shutdown(); expect(peer.stopped).toHaveBeenCalledOnce();
    expect(peer.sent.some(env => env.method === "shutdown")).toBe(false);
  });
});
