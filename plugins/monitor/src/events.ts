const lineBytes = 500;
const batchBytes = 3_000;
const bufferBytes = 1_048_576;
const refillMS = 2_000;
const overloadMS = 30_000;

export function truncate(text: string, limit: number): string {
  const bytes = Buffer.from(text);
  if (bytes.length <= limit) return text;
  let end = limit;
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
  return bytes.subarray(0, end).toString() + "…(truncated)";
}

/** Framing and volume policy live in the monitor, independently of execution. */
export class Events {
  private partial = "";
  private tokens = 10;
  private refilled = performance.now();
  private overloadAt: number | undefined;
  private suppressedAt = 0;
  private suppressed = 0;
  lines = 0;
  suppressedTotal = 0;

  push(output: string, ended: boolean): { text?: string; overloaded?: boolean } {
    this.partial += output;
    if (Buffer.byteLength(this.partial) > bufferBytes) {
      const tail = Buffer.from(this.partial).subarray(-bufferBytes);
      let start = 0;
      while (start < tail.length && (tail[start] & 0xc0) === 0x80) start++;
      this.partial = tail.subarray(start).toString();
    }
    const lines = this.partial.split("\n");
    this.partial = ended ? "" : lines.pop()!;
    const complete = lines.map(line => line.trim()).filter(Boolean);
    this.lines += complete.length;
    if (!complete.length) return {};
    const now = performance.now();
    const refill = Math.floor((now - this.refilled) / refillMS);
    this.tokens = Math.min(10, this.tokens + refill);
    this.refilled += refill * refillMS;
    if (now - this.suppressedAt > refillMS * 3) this.overloadAt = undefined;
    if (this.tokens > 0 || ended) {
      if (!ended) this.tokens--;
      let notice = "";
      if (this.suppressed) {
        notice = `[${this.suppressed} event batches suppressed because output was too frequent. Use a more selective filter.]\n`;
        this.suppressed = 0;
      }
      return { text: notice + truncate(complete.map(line => truncate(line, lineBytes)).join("\n"), batchBytes) };
    }
    this.suppressed++;
    this.suppressedTotal++;
    this.suppressedAt = now;
    this.overloadAt ??= now;
    if (now - this.overloadAt > overloadMS) {
      return { overloaded: true, text: "Monitor stopped: sustained excessive output. Restart with a more selective filter, such as grep --line-buffered or a script that prints only relevant changes." };
    }
    return {};
  }
}
