import type { ActiveDocumentContext, InputFile, InputImage, MessageContentPart, QueuedTurn, WuuDesktopApi } from "../shared/protocol";
import type { BoundAppServerRequest } from "./appServerClients";
import { AppServerResponseError } from "./appServerErrors";

interface SteerInput {
  thread_id: string;
  expected_turn_id: string;
  prompt: string;
  client_id?: string;
  images?: InputImage[];
  files?: InputFile[];
  active_document?: ActiveDocumentContext;
  content_parts?: MessageContentPart[];
}

/** The caller captures one session connection before either operation. Only an
 * explicit non-admission refusal permits queueing the same original input. */
export async function steerOrQueueIsolatedTurn(request: BoundAppServerRequest, input: SteerInput): Promise<Awaited<ReturnType<WuuDesktopApi["steerTurn"]>>> {
  try {
    return await request("turn/steer", input);
  } catch (error) {
    if (!(error instanceof AppServerResponseError) || error.code !== "isolated_turn_requires_queue" || !input.client_id) throw error;
    const { expected_turn_id: _expectedTurn, ...queuedInput } = input;
    const result = await request<{queued: QueuedTurn}>("turn/queue", queuedInput);
    return {turn_id: "", delivery: "queued", queued: result.queued};
  }
}
