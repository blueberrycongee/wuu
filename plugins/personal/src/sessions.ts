import { createHash } from "node:crypto";
import type { RuntimeHost, ToolExecuteParams, ToolRegistration, MessageRef, SessionLocatorV1, SessionRouteRequest } from "@wuu/plugin-sdk";

const string = { type: "string", minLength: 1, maxLength: 128 };
const locatorSchema = {type:"object",properties:{version:{const:1},execution_host:{type:"string",minLength:1,maxLength:512},session_id:string,storage_host:{type:"string"},display_host:{type:"string"}},required:["version","execution_host","session_id"],additionalProperties:false};
const schema = (properties: Record<string, unknown>, required: string[] = []) => ({ type: "object" as const, properties, required, additionalProperties: false });
export const personalSessionTools: ToolRegistration[] = [
  { id: "list_sessions", description: "List this Personal host and registered local desktop Coding sessions owned by the same admitted desktop user. Copy the returned locator when inspecting or sending to Coding. scope=local works without the desktop bridge; unavailable desktop discovery fails explicitly, never silently omits hosts.", input_schema: schema({scope:{type:"string",enum:["all","local","desktop"]}}), activity: { read_only: true, concurrency_safe: true } },
  { id: "inspect_session", description: "Inspect an exact available session and, optionally, the status of a send_to_session request. Does not start, resume or change work.", input_schema: schema({ locator: locatorSchema, session_id: string, request_id: string }, ["session_id"]), activity: { read_only: true, concurrency_safe: true } },
  { id: "send_to_session", description: "Send an agent-authored instruction to an exact available session, automatically including the host-bound original user message that started this turn, its attachments and direct reply context. Explicitly add source_message_seqs for all relevant earlier requirements AND later user steer/correction messages from this main conversation; never quote a rewritten summary as the user's words. The host authenticates original content and materializes attachments at the destination. Reuse request_id to recover a lost response; changing an admitted request conflicts. This queues one message and never steers, resumes canceled work or grants external action permission.", input_schema: schema({ locator: locatorSchema, session_id: string, request_id: string, instruction: { type: "string", minLength: 1, maxLength: 64000 }, source_message_seqs: { type: "array", maxItems: 63, uniqueItems: true, items: { type: "integer", minimum: 1 } } }, ["session_id", "request_id", "instruction"]), activity: { concurrency_safe: true } },
  { id: "cancel_session_message", description: "Cancel only an identified message previously sent with send_to_session, using its exact session_id and request_id. Does not archive/delete a session or stop unrelated work. Cancellation requested is not confirmed completion.", input_schema: schema({ locator: locatorSchema, session_id: string, request_id: string }, ["session_id", "request_id"]), activity: { concurrency_safe: true } },
];
const required = (input: Record<string, unknown>, key: string, max = 128): string => {
  const value = input[key];
  if (typeof value !== "string" || !value.trim() || value.length > max) throw new Error(`invalid ${key}`);
  return value;
};
const requestID = (space: string, id: string) => `personal-session-${createHash("sha256").update(`${space}\0${id}`).digest("hex").slice(0, 32)}`;

export async function executeSessionTool(host: RuntimeHost, space: string, mainSession: string, call: ToolExecuteParams, signal: AbortSignal): Promise<unknown> {
  const input = call.arguments as Record<string, unknown>;
  const route = async (request: SessionRouteRequest): Promise<any> => {
    const response=await host.call("host.session.route",request,{signal});
    if(response.status!=="completed" || response.error)throw new Error(response.error?.message ?? "desktop message outcome unknown; inspect or retry the SAME request_id, never issue a duplicate intent");
    return response.result;
  };
  const localList=async()=>{
    const own=await host.call("host.session.list",{scope:"owned"},{signal});
    const shared=await host.call("host.session.list",{scope:"shared"},{signal});
    return [...new Map([...own.sessions,...shared.sessions].map(item=>[item.session_id,{...item,locator:{version:1 as const,execution_host:"local",session_id:item.session_id}}])).values()];
  };
  if(call.tool_id==="list_sessions"){
    const scope=input.scope ?? "all";if(!["all","local","desktop"].includes(String(scope)))throw new Error("invalid session scope");
    const local=scope==="desktop"?[]:await localList();
    const desktop=scope==="local"?{sessions:[]}:await route({version:1,operation:"list",operation_id:requestID(space,`list:${call.call_id}`),locator:{version:1,execution_host:"desktop"},params:{scope:"shared"}});
    return {sessions:[...local,...desktop.sessions]};
  }
  const session_id=required(input,"session_id");
  const locator=(input.locator ?? {version:1,execution_host:"local",session_id}) as SessionLocatorV1;
  if(locator.version!==1||locator.session_id!==session_id||typeof locator.execution_host!=="string"||!locator.execution_host)throw new Error("locator must be copied from the exact listed session");
  const remote=locator.execution_host!=="local";
  const inspect=async(request_id?:string)=>remote?route({version:1,operation:"inspect",operation_id:requestID(space,`inspect:${request_id ?? call.call_id}`),locator,params:{session_id,...(request_id?{request_id}:{}),wait:"none"}}):request_id?host.call("host.session.inspect",{session_id,request_id,wait:"none"},{signal}):{session:(await localList()).find(value=>value.session_id===session_id) ?? (()=>{throw new Error("session is not available in the authorized scope")})()};
  if(call.tool_id==="inspect_session")return inspect(input.request_id===undefined?undefined:requestID(space,required(input,"request_id")));
  const request_id=requestID(space,required(input,"request_id"));
  if(call.tool_id==="cancel_session_message"){
    const inspected=await inspect(request_id);const turn=inspected.turn;
    if(!turn||["completed","failed","interrupted","discarded"].includes(turn.state))return {session_id,request_id:input.request_id,cancelled:false,state:turn?.state ?? "not_found"};
    const target=turn.state==="queued"?{queue_id:turn.queue_id}:{turn_id:turn.turn_id};
    if(!target.queue_id&&!target.turn_id)throw new Error("message has no exact cancellable identity; inspect again");
    const params={session_id,...target};
    return remote?route({version:1,operation:"cancel",operation_id:`${request_id}:cancel`,locator,params}):host.call("host.session.cancel",params,{signal});
  }
  if(call.tool_id!=="send_to_session")throw new Error("unknown session tool");
  if(!remote&&session_id===mainSession)throw new Error("reply directly in the main conversation instead of sending to itself");
  const source_messages=originalSourceMessages(call,mainSession),instruction=required(input,"instruction",64000);
  const params={session_id,request_id,input:{prompt:instruction,handoff:{source_messages}},if_running:"queue" as const,cause:"personal.session.handoff",...(!remote?{presentation:{kind:"session_message" as const,text:instruction,related_session_id:mainSession}}:{})};
  return remote?route({version:1,operation:"send",operation_id:request_id,locator,params}):host.call("host.session.send",params,{signal});
}

/** Only host-bound current input can anchor a handoff; model arguments select additional addresses (including a later user steer), never authorship or bytes. */
export function originalSourceMessages(call: ToolExecuteParams, mainSession: string): MessageRef[] {
  const current = call.source_message;
  if (!current || current.session_id !== mainSession || !Number.isSafeInteger(current.seq) || current.seq < 1) throw new Error("sending requires a host-bound original user source message");
  const prior = (call.arguments as Record<string, unknown>).source_message_seqs ?? [];
  if (!Array.isArray(prior) || prior.length > 63 || prior.some(seq => !Number.isSafeInteger(seq) || seq < 1)) throw new Error("invalid original source message sequence");
  return [...new Set<number>([...prior, current.seq])].sort((a, b) => a - b).map(seq => ({ session_id: mainSession, seq }));
}
