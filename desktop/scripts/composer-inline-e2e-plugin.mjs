// Test-only third-party module. It uses the same public registration and cleanup
// contracts as an installed extension, never imports renderer internals.
export function activate(api) {
  const history = window.__inlineCompletionE2E ??= { generations: [] };
  const controller = {
    requests: [], order: [], pending: new Map(), disposed: false,
    failFirst: false, fallbackText: "", runtimeMode: false, provider: undefined,
    resolve(id, text) {
      const resolve = this.pending.get(id);
      if (!resolve) throw new Error(`Unknown fixture completion ${id}`);
      this.pending.delete(id);
      resolve(text === null ? null : { text });
    },
    register() {
      this.provider = api.registerComposerInlineCompletionProvider({
        id: "controlled", order: 10,
        provideCompletion: (context, signal) => {
          const request = { id: this.requests.length + 1, context: { ...context }, aborted: signal.aborted };
          this.order.push(`controlled:${request.id}`);
          this.requests.push(request);
          signal.addEventListener("abort", () => { request.aborted = true; }, { once: true });
          if (this.runtimeMode) return api.invokeRuntime("inline.complete", context, { signal });
          return new Promise((resolve) => this.pending.set(request.id, resolve));
        },
      });
    },
  };
  history.generations.push(controller);
  history.active = controller;
  api.registerComposerInlineCompletionProvider({
    id: "first", order: 0,
    provideCompletion: () => {
      controller.order.push("first");
      if (controller.failFirst) throw new Error("Expected fixture provider failure");
      return null;
    },
  });
  controller.register();
  api.registerComposerInlineCompletionProvider({
    id: "fallback", order: 20,
    provideCompletion: () => {
      controller.order.push("fallback");
      return controller.fallbackText ? { text: controller.fallbackText } : null;
    },
  });
  api.registerCleanup(() => { controller.disposed = true; });
}
