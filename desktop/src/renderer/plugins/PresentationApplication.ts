import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";

// These synchronous reads contain presentation data, not authority. Async reads,
// handle factories, subscriptions and mutations always retain the original scope.
const PRESENTATION_READS = new Set([
  "getSnapshot", "getStatusSnapshot", "getTelemetry", "getCommands", "getComposerInlineCompletionProviders",
  "getShellSkins", "readDraftEngineMemory", "currentAppliedTheme", "availableExtensionThemes",
  "selectedExtensionThemeKey", "readAppearance", "renderLayers",
  "get", "label", "orderModels", "visibleModels", "getPages", "getConflicts", "getDiagnostics",
]);
// Retired observers are empty, not rebound: no listener may attach to the old
// writer. React subscribes to the new facade after its application prop changes.
const PRESENTATION_OBSERVERS = new Set([
  "subscribe", "subscribeStatus", "subscribeTelemetry", "subscribeComposerInlineCompletionProviders",
  "subscribeRemote", "subscribeShellSkins", "observeBackground", "observeAppliedTheme", "observeContentThemeSelection",
  "observeAppearance", "onSystemPermissionGuideChanged", "onAppContextChanged", "onThemePreferenceChange",
  "onResizeState", "onMaximizedChange", "onFullScreenChange", "onVisibilityChange", "onEvent", "onDock", "onInvalidate",
  "onState", "onReveal", "onFocusAddress", "onFocusFind", "onDownloads", "onPopoverDismiss", "onOpenPage",
  "onSurface", "onTabAdopted", "onUserInput", "subscribeRefresh",
]);

/** Retain values already drawn, never the retired writer. Each returned facade is
 * bound to ONE business lifetime and is replaced in render props after rebind. */
export function retainPresentationReads(service: ApplicationServiceV1, signal: AbortSignal): ApplicationServiceV1 {
  const wrapped = new WeakMap<object, object>();
  const wrap = (source: object, application = false): object => {
    const existing = wrapped.get(source); if (existing) return existing;
    const values = new Map<string | symbol, unknown>();
    const proxy = new Proxy({}, {
      // React's development profiler compares old and new props after a
      // handoff. Only expose values already observed on the retired facade;
      // enumerating an untouched getter must not reopen its dead scope.
      ownKeys: () => signal.aborted ? [...values.keys()] : Reflect.ownKeys(source),
      has: (_target, key) => signal.aborted ? values.has(key) : Reflect.has(source, key),
      getOwnPropertyDescriptor: (_target, key) => (signal.aborted ? values.has(key) : Reflect.has(source, key)) ? { configurable: true, enumerable: true } : undefined,
      get(_target, key) {
      if (values.has(key)) return values.get(key);
      // React also probes these metadata properties directly, even on a Proxy
      // whose source has no such property. They carry no application authority.
      if (signal.aborted && (key === "$$typeof" || key === Symbol.toStringTag)) return undefined;
      const value = Reflect.get(source, key, source);
      if (typeof value === "function") {
        const reads: { args: unknown[]; value: unknown }[] = [];
        const method = (...args: unknown[]) => {
          if (signal.aborted && PRESENTATION_OBSERVERS.has(String(key))) return () => {};
          if (signal.aborted && PRESENTATION_READS.has(String(key))) {
            const saved = reads.find(row => samePresentationInput(row.args, args));
            if (saved) return saved.value;
          }
          const result = value.apply(source, args);
          if (PRESENTATION_READS.has(String(key)) && !(result instanceof Promise)) {
            const previous = reads.findIndex(row => samePresentationInput(row.args, args));
            if (previous >= 0) reads.splice(previous, 1);
            reads.push({ args: capturePresentationInput(args) as unknown[], value: result });
          }
          return result;
        };
        values.set(key, method); return method;
      }
      // Facets are accessed while active and retain only already-observed values.
      const serviceObject = value && typeof value === "object" && (application || Reflect.ownKeys(value).some(name =>
        typeof Reflect.getOwnPropertyDescriptor(value, name)?.value === "function" || Reflect.getOwnPropertyDescriptor(value, name)?.get));
      const result = serviceObject ? wrap(value) : value;
      values.set(key, result); return result;
    } });
    wrapped.set(source, proxy); return proxy;
  };
  return wrap(service, true) as ApplicationServiceV1;
}

// Public read arguments are immutable data. Compare their values, not the new
// object React constructs on each render. Non-data objects/functions keep identity.
function samePresentationInput(left: unknown, right: unknown, seen = new WeakMap<object, object>()): boolean {
  if (Object.is(left, right)) return true;
  if (!left || !right || typeof left !== "object" || typeof right !== "object") return false;
  if (Object.getPrototypeOf(left) !== Object.getPrototypeOf(right)) return false;
  if (!Array.isArray(left) && Object.getPrototypeOf(left) !== Object.prototype) return false;
  if (seen.get(left) === right) return true;
  seen.set(left, right);
  const keys = Object.keys(left);
  return keys.length === Object.keys(right).length && keys.every(key => Object.hasOwn(right, key)
    && samePresentationInput(Reflect.get(left, key), Reflect.get(right, key), seen));
}
function capturePresentationInput(value: unknown, seen = new WeakMap<object, object>()): unknown {
  if (!value || typeof value !== "object" || !Array.isArray(value) && Object.getPrototypeOf(value) !== Object.prototype) return value;
  const previous = seen.get(value); if (previous) return previous;
  const copy: Record<string, unknown> | unknown[] = Array.isArray(value) ? [] : {};
  seen.set(value, copy);
  for (const key of Object.keys(value)) Reflect.set(copy, key, capturePresentationInput(Reflect.get(value, key), seen));
  return copy;
}
