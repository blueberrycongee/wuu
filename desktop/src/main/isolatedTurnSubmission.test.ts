import { describe, expect, it, vi } from "vitest";
import { AppServerResponseError } from "./appServerErrors";
import { steerOrQueueIsolatedTurn } from "./isolatedTurnSubmission";
import type { BoundAppServerRequest } from "./appServerClients";

const input = {thread_id:"original",expected_turn_id:"review",client_id:"same-input",prompt:"human question",
  images:[{media_type:"image/png",data:"aW1hZ2U="}],files:[{media_type:"text/plain",filename:"note.txt",data:"bm90ZQ=="}],
  active_document:{path:"/original/note.txt"},content_parts:[{type:"text" as const,text:"human question"}]};
// Only a definite isolation refusal permits another operation. The captured
// connection and original user intent must survive that asynchronous boundary.
describe("isolated turn submission",()=>{
  it("queues the same original intent on the captured request only after typed refusal",async()=>{
    const call=vi.fn().mockRejectedValueOnce(new AppServerResponseError("isolated", "isolated_turn_requires_queue"))
      .mockResolvedValueOnce({queued:{id:input.client_id,thread_id:input.thread_id}});
    const result=await steerOrQueueIsolatedTurn(call as unknown as BoundAppServerRequest,input);
    expect(call.mock.calls).toEqual([["turn/steer",input],["turn/queue",{thread_id:input.thread_id,client_id:input.client_id,prompt:input.prompt,images:input.images,files:input.files,active_document:input.active_document,content_parts:input.content_parts}]]);
    expect(result).toEqual({turn_id:"",delivery:"queued",queued:{id:input.client_id,thread_id:input.thread_id}});
  });
  it.each([new Error("isolated_turn_requires_queue"),Object.assign(new Error("uncertain"),{code:"isolated_turn_requires_queue"}),new AppServerResponseError("denied","access_denied")])("does not retry uncertain or other errors",async error=>{
    const call=vi.fn().mockRejectedValue(error);
    await expect(steerOrQueueIsolatedTurn(call as unknown as BoundAppServerRequest,input)).rejects.toBe(error);
    expect(call).toHaveBeenCalledOnce();
  });
  it("does not retry or replace an uncertain queue after the explicit refusal",async()=>{
    const uncertain=new Error("connection reset after queue admission");
    const call=vi.fn().mockRejectedValueOnce(new AppServerResponseError("isolated","isolated_turn_requires_queue")).mockRejectedValueOnce(uncertain);
    await expect(steerOrQueueIsolatedTurn(call as unknown as BoundAppServerRequest,input)).rejects.toBe(uncertain);
    expect(call).toHaveBeenCalledTimes(2);
  });
  it("does not queue an identity-less steer or an already accepted native queued steer",async()=>{
    const call=vi.fn().mockRejectedValueOnce(new AppServerResponseError("isolated","isolated_turn_requires_queue"));
    await expect(steerOrQueueIsolatedTurn(call as unknown as BoundAppServerRequest,{...input,client_id:undefined})).rejects.toThrow("isolated");
    expect(call).toHaveBeenCalledOnce();
    const accepted={turn_id:"native",delivery:"queued"};call.mockReset().mockResolvedValue(accepted);
    expect(await steerOrQueueIsolatedTurn(call as unknown as BoundAppServerRequest,input)).toBe(accepted);expect(call).toHaveBeenCalledOnce();
  });
});
