// jsdom 25 lacks this browser API. Remove when the test DOM supports AbortSignal.any.
if (!AbortSignal.any) {
  Object.defineProperty(AbortSignal, "any", { configurable: true, value: (signals: AbortSignal[]) => {
    const controller = new AbortController();
    const subscriptions: Array<() => void> = [];
    const abort = (source: AbortSignal) => {
      controller.abort(source.reason);
      subscriptions.splice(0).forEach(stop => stop());
    };
    for (const signal of signals) {
      if (signal.aborted) { abort(signal); break; }
      const listener = () => abort(signal);
      signal.addEventListener("abort", listener, { once: true });
      subscriptions.push(() => signal.removeEventListener("abort", listener));
    }
    return controller.signal;
  } });
}
