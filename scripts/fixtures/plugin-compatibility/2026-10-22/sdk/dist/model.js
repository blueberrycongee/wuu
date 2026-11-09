export const MODEL_INVOKE_SERVICE = "host.model.invoke";
export const MODEL_EMIT_SERVICE = "host.model.emit";
export const BACKGROUND_SERVICE = "host.background";
export const MODEL_REQUEST_TRANSFORM_CAPABILITY = "model.request.transform";
export const MODEL_REQUEST_OBSERVE_CAPABILITY = "model.request.observe";
export const MODEL_MIDDLEWARE_CAPABILITY = "model.middleware";
export const MODEL_ADAPTER_CAPABILITY = "model.adapter";
export function requireModelInvokeService() {
    return { name: MODEL_INVOKE_SERVICE, major_version: 1, required: true };
}
export function requireModelEmitService() {
    return { name: MODEL_EMIT_SERVICE, major_version: 1, required: true };
}
export function requireBackgroundService() {
    return { name: BACKGROUND_SERVICE, major_version: 1, required: true };
}
/** Independent inference. Returned tool calls are data; this never executes them. */
export async function invokeModel(host, input) {
    return await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "call", params: input });
}
export async function startModelInvocation(host, input) {
    return await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "start", params: input });
}
export async function readModelInvocation(host, input) {
    return await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "read", params: input });
}
export async function cancelModelInvocation(host, input) {
    await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "cancel", params: input });
}
/** Pull model events with automatic cancellation when iteration exits early. */
export async function* streamModel(host, input) {
    const handle = await startModelInvocation(host, input);
    let cursor = 0;
    let done = false;
    let failed = false;
    try {
        while (!done) {
            const page = await readModelInvocation(host, { ...handle, cursor, wait_ms: 1000 });
            cursor = page.cursor;
            done = page.done;
            for (const event of page.events)
                yield event;
            if (page.error)
                throw Object.assign(new Error(page.error.message), { code: page.error.code });
            if (done)
                return page.result;
        }
    }
    catch (error) {
        failed = true;
        throw error;
    }
    finally {
        if (!done) {
            try {
                await cancelModelInvocation(host, handle);
            }
            catch (error) {
                if (!failed)
                    throw error;
            }
        }
    }
}
/** Valid only in a live streaming adapter/middleware callback. */
export async function emitModelEvent(host, event) {
    await host.call("host.service.call", { service: MODEL_EMIT_SERVICE, method: "call", params: event });
}
/** Start independent work in a caller-owned service. Allowed after activation, including timers. */
export async function startBackground(host, input) {
    return await host.call("host.service.call", { service: BACKGROUND_SERVICE, method: "start", params: input });
}
export async function inspectBackground(host, input) {
    return await host.call("host.service.call", { service: BACKGROUND_SERVICE, method: "inspect", params: input });
}
export async function cancelBackground(host, input) {
    return await host.call("host.service.call", { service: BACKGROUND_SERVICE, method: "cancel", params: input });
}
