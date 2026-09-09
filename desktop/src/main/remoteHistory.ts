const PAGE_TURNS = 20;
const PAGE_BYTES = 256 * 1024;

/** Native snapshot notifications can still contain full histories. Keep their
 * recent tail and use the same source-backed cursor as paged resume replies. */
export function projectRemoteHistory(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(projectRemoteHistory);
  if (!value || typeof value !== "object") return value;
  const record = value as Record<string, unknown>;
  if (record.history_paged === true) return record;
  if (typeof record.id === "string" && Array.isArray(record.turns)) {
    const turns = record.turns as Array<{ id: string }>;
    let start = turns.length, bytes = 0;
    while (start > 0 && turns.length - start < PAGE_TURNS) {
      const size = Buffer.byteLength(JSON.stringify(turns[start - 1]));
      if (start < turns.length && bytes + size > PAGE_BYTES) break;
      bytes += size; start--;
    }
    return { ...record, turns: turns.slice(start), history_cursor: start > 0 ? `turn:${turns[start].id}` : undefined };
  }
  return Object.fromEntries(Object.entries(record).map(([key, item]) => [key, projectRemoteHistory(item)]));
}
