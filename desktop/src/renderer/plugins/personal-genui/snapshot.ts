import { boundedJSON, exact, invalid, record, validateSpec, validateState } from "../../../../../plugins/personal/src/genui/schema";
import type { ActionResult } from "../../../../../plugins/personal/src/genui/service";

export function decodeSnapshot(input: unknown): ActionResult {
  boundedJSON(input, 262144);
  const result = record(input); exact(result, ["surface", "acceptedRevision", "delivery", "deliveryRevision", "submission"]);
  const surface = record(result.surface); exact(surface, ["schemaVersion", "surfaceId", "messageId", "spec", "state", "revision", "submitted"]);
  if (surface.schemaVersion !== 1 || typeof surface.submitted !== "boolean") invalid("invalid surface version/state");
  for (const name of ["surfaceId", "messageId"]) if (typeof surface[name] !== "string" || !(surface[name] as string).length || (surface[name] as string).length > 128) invalid("invalid surface identity");
  if (!Number.isSafeInteger(surface.revision) || (surface.revision as number) < 0 || !Number.isSafeInteger(result.acceptedRevision) || (result.acceptedRevision as number) < 0 || (result.acceptedRevision as number) > (surface.revision as number)) invalid("invalid revision");
  if (result.delivery !== undefined && !["pending", "accepted", "held", "unconfirmed", "stopped", "rejected"].includes(String(result.delivery))) invalid("invalid delivery state");
  if (result.deliveryRevision !== undefined && (!Number.isSafeInteger(result.deliveryRevision) || (result.deliveryRevision as number) < 0)) invalid("invalid delivery revision");
  if (result.submission !== undefined) {
    const admission = record(result.submission); exact(admission, ["requestId", "prompt", "resumeReceiptId"]);
    if (typeof admission.requestId !== "string" || !admission.requestId || admission.requestId.length > 128 || typeof admission.prompt !== "string" || admission.prompt.length > 1000 || (admission.resumeReceiptId !== undefined && (typeof admission.resumeReceiptId !== "string" || !admission.resumeReceiptId || admission.resumeReceiptId.length > 128))) invalid("invalid submission admission");
  }
  const spec = validateSpec(surface.spec); validateState(spec, surface.state);
  return structuredClone(input) as ActionResult;
}
