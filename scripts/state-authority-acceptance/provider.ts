import { documentStoreDescriptor, runJSONLRuntime, RuntimeHostServiceError, type RuntimePlugin } from "@wuu/plugin-sdk";

// Independent HTTP adapter: no filesystem storage, local data mirror, native
// implementation imports, or knowledge of Wuu product provider identities.
const endpoint = process.env.DOCUMENTS_ENDPOINT!;
let storeID: string | undefined;
async function request(method: string, params: unknown): Promise<unknown> {
  const response = await fetch(`${endpoint}/${method}`, {
    method: "POST", headers: { "content-type": "application/json", ...(storeID ? { "x-documents-store-id": storeID } : {}) },
    body: JSON.stringify(params),
  });
  const result = await response.json() as { error?: { code: string; message: string }; store_id?: string };
  if (result.error) throw new RuntimeHostServiceError(result.error);
  if (!response.ok) throw new Error(`document backend returned HTTP ${response.status}`);
  if (method === "authority" && typeof result.store_id === "string") storeID = result.store_id;
  return result;
}
const plugin: RuntimePlugin = {
  initialize() {
    return { protocol_version: 3, lifecycle_version: 2,
      provided_services: [documentStoreDescriptor({ authority: true, paged_export: true, import: true })] };
  },
  async invokeService({ method, params }) {
    if (!storeID && method !== "authority") await request("authority", {});
    // Every data operation carries the captured identity. The backend checks it
    // in the same serialized operation as CAS, closing a reset/check-call race.
    return await request(method, params);
  },
};
await runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout });
