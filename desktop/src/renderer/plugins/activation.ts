/** Settle host-owned waits even when trusted plugin work ignores cancellation. */
export async function settleOnAbort<T>(start: () => T | PromiseLike<T>, signal: AbortSignal): Promise<T> {
  signal.throwIfAborted();
  let abort!: () => void;
  try {
    return await new Promise<T>((resolve, reject) => {
      abort = () => reject(signal.reason);
      signal.addEventListener("abort", abort, { once: true });
      // Promise assimilation observes late rejection as well as late success.
      // Neither may resume the caller after its lifetime has ended.
      try {
        Promise.resolve(start()).then(resolve, reject);
      } catch (error: unknown) {
        reject(error);
      }
    });
  } finally {
    signal.removeEventListener("abort", abort);
  }
}
