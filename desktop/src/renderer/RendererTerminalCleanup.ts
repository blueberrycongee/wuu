// Native PTYs have process-unique IDs and an owning window. Retired controllers
// can finish closing those captured IDs without retaining a business writer.
// The renderer document, not a replacement application generation, owns the join.
const pending = new Set<Promise<void>>();
const failures: unknown[] = [];

export function retainTerminalCleanup(cleanup: Promise<void>): void {
  if (pending.has(cleanup)) return;
  pending.add(cleanup);
  void cleanup.then(() => pending.delete(cleanup), error => {
    failures.push(error);
    pending.delete(cleanup);
  });
}

/** Called after application shutdown has synchronously retired its last owner. */
export async function flushTerminalCleanup(): Promise<void> {
  while (pending.size) await Promise.allSettled([...pending]);
  const rejected = failures.splice(0);
  if (rejected.length) throw new AggregateError(rejected, "Retired terminal cleanup failed");
}
