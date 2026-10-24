import type { RuntimeHost, ServiceDescriptor, ServiceRequirement } from "./index.js";

export const DOCUMENT_STORE_SERVICE = "state.documents";
/** Decimal integer string. Do not convert durable revisions to JS numbers. */
export type StateRevision = string;
export interface StateDocument<Value = unknown> {
  readonly namespace: string;
  readonly key: string;
  readonly value: Value;
}
export interface StateQuery {
  readonly namespace: string;
  readonly prefix?: string;
  /** Omit to scan the namespace; an empty array selects no keys. */
  readonly keys?: readonly string[];
  /** Inclusive lower key bound; selectors intersect in UTF-8 byte order. */
  readonly start?: string;
  /** Exclusive upper key bound. */
  readonly end?: string;
  /** Descending key order; defaults to ascending. */
  readonly reverse?: boolean;
  /** 1..10000 bounds page size. Omitted or zero retains an unbounded read. */
  readonly limit?: number;
  /** Exclusive last key in this direction; requires at_revision. */
  readonly cursor?: string;
  /** Any intervening store commit rejects the read with state_conflict. */
  readonly at_revision?: StateRevision;
}
export interface StateView<Value = unknown> {
  readonly revision: StateRevision;
  readonly documents: readonly StateDocument<Value>[];
  /** Present only when more keys match. Continue with unchanged selectors,
   * cursor=next_cursor and at_revision=revision. This is not a saved snapshot:
   * after state_conflict, restart a pure read from the first page.
   */
  readonly next_cursor?: string;
}
export type StateWrite<Value = unknown> =
  | (StateDocument<Value> & { readonly delete?: false })
  | { readonly namespace: string; readonly key: string; readonly delete: true; readonly value?: never };
export interface StateTransaction<Value = unknown> {
  /** Retry this exact ID and intent after an uncertain response. */
  readonly id: string;
  readonly expected_revision: StateRevision;
  readonly writes: readonly StateWrite<Value>[];
}
export interface StateReceipt {
  readonly id: string;
  readonly revision: StateRevision;
}
/** Optional provider maintenance; document revision and receipts are unchanged. */
export interface StateCompaction {
  readonly revision: StateRevision;
  readonly bytes_before: number;
  readonly bytes_after: number;
}
/** Import acknowledgement; verify a full export before publishing a new authority. */
export interface StateImportResult {
  readonly revision: StateRevision;
}
export interface StateSnapshot<Value = unknown> {
  readonly version: 1;
  readonly revision: StateRevision;
  readonly documents: readonly StateDocument<Value>[];
  /** Receipts must migrate with documents to preserve retry semantics. */
  readonly receipts: readonly (StateReceipt & { readonly digest: string })[];
}

/** Continue after the last actual document, ordered by UTF-8 namespace then key. */
export interface StateDocumentCursor {
  readonly namespace: string;
  readonly key: string;
}
/** Page the existing export method; continuation and receipts require both pins. */
export type StateExportPageRequest = {
  /** Positive UTF-8 JSON byte target. One oversized record may exceed it. */
  readonly target_bytes: number;
  /** Positive maximum number of records in this page. */
  readonly max_items: number;
} & (
  | { readonly section: "documents"; readonly after?: never; readonly at_revision?: never; readonly generation?: never }
  | { readonly section: "documents"; readonly after?: StateDocumentCursor; readonly at_revision: StateRevision; readonly generation: string }
  | { readonly section: "receipts"; readonly after?: { readonly revision: StateRevision }; readonly at_revision: StateRevision; readonly generation: string }
);
export interface StateExportPage<Value = unknown> {
  readonly format: "page.v1";
  readonly revision: StateRevision;
  readonly generation: string;
  readonly section: "documents" | "receipts";
  readonly documents: readonly StateDocument<Value>[];
  /** Strictly increasing revision order; the unused section is an empty array. */
  readonly receipts: StateSnapshot<Value>["receipts"];
  /** False requires at least one item. Build after from the last actual item. */
  readonly done: boolean;
}

export function requireDocumentStore(): ServiceRequirement {
  return { name: DOCUMENT_STORE_SERVICE, major_version: 1, required: true };
}

/** Advertise optional maintenance only when the provider actually implements it. */
export function documentStoreDescriptor(options: { compact?: boolean; import?: boolean; paged_export?: boolean } = {}): ServiceDescriptor {
  const methods = ["read", "commit", "export"];
  if (options.compact) methods.push("compact");
  if (options.import) methods.push("import");
  return {
    name: DOCUMENT_STORE_SERVICE,
    version: "1.0.0",
    methods: methods.map((name) => {
      const schemaVersion = name === "export" && options.paged_export === true ? 2 : 1;
      return {
        name,
        input_schema: `state.documents.${name}.input.v${schemaVersion}`,
        output_schema: `state.documents.${name}.output.v${schemaVersion}`,
        ...(name === "read" || name === "export" ? { preparation: true } : {}),
      };
    }),
  };
}

/** All calls use the selected service binding. No local storage fallback exists.
 * A commit compares the entire store revision, preventing write skew and phantom
 * insertions across namespaces. A canceled or lost response cannot undo a write;
 * retry identical intent to recover its receipt. Never replay external effects
 * merely because a storage operation is retryable.
 */
export function documentStore(host: RuntimeHost) {
  async function call<T>(method: string, params: object): Promise<T> {
    return await host.call("host.service.call", {
      service: DOCUMENT_STORE_SERVICE, method, params,
    }) as T;
  }
  return {
    read: <Value = unknown>(query: StateQuery) => call<StateView<Value>>("read", query),
    commit: <Value = unknown>(transaction: StateTransaction<Value>) => call<StateReceipt>("commit", transaction),
    compact: () => call<StateCompaction>("compact", {}),
    import: <Value = unknown>(snapshot: StateSnapshot<Value>) => call<StateImportResult>("import", snapshot),
    export: <Value = unknown>() => call<StateSnapshot<Value>>("export", {}),
    /** Requires export v2. Errors propagate without retry or full-export fallback. */
    exportPage: <Value = unknown>(page: StateExportPageRequest) => call<StateExportPage<Value>>("export", { page }),
  };
}
