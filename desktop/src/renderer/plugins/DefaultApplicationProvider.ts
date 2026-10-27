import type { ApplicationActionMapV1, ApplicationCheckpointV1, ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { PluginGenerationApi } from "../../../../packages/plugin-sdk/src";
import type { WorkbenchSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { ApplicationController } from "./ApplicationController";
import { createDefaultWorkbench } from "./Workbench";

const checkpointSchema = "wuu.application/v1";
type Checkpoint = ReturnType<ApplicationController["exportCheckpoint"]> & { workbench: WorkbenchSnapshotV1 };

/** A bundled implementation, registered and selected exactly like an external business provider. */
export function activate(api: PluginGenerationApi): void {
  api.registerApplicationProvider({ id: "ui.application", prepare: () => {
    let controller: ApplicationController | undefined;
    let workbench: ReturnType<typeof createDefaultWorkbench> | undefined;
    let stop: (() => void) | undefined;
    let checkpoint: ApplicationCheckpointV1 | undefined;
    let handoffCleanup: Promise<void> = Promise.resolve();
    const retire = (): Promise<void> => {
      if (!controller) return handoffCleanup;
      const previous = controller;
      stop?.(); stop = undefined;
      handoffCleanup = previous.flushHandoffCleanup();
      try {
        checkpoint = { schema: checkpointSchema, value: { ...previous.exportCheckpoint(), workbench: workbench!.getSnapshot() } satisfies Checkpoint };
      } finally {
        // Rendered children may hold the concrete workbench, not a scoped proxy.
        // Revoke those callbacks and the controller before any asynchronous join.
        workbench?.dispose(); workbench = undefined; controller = undefined;
      }
      return handoffCleanup;
    };
    const requireController = () => { if (!controller) throw new Error("Application provider is inactive"); return controller; };
    return {
      activate: async (saved?: ApplicationCheckpointV1) => {
        if (saved && saved.schema !== checkpointSchema) throw new Error(`Unsupported application checkpoint: ${saved.schema}`);
        const restored = saved?.value as Checkpoint | undefined;
        workbench = createDefaultWorkbench(api.contributions, requireController, restored?.workbench);
        controller = new ApplicationController(restored && { ...restored.state, initialized: undefined }, {
          applicationProviderId: api.pluginId,
          contributions: api.contributions,
          get workbench() { if (!workbench) throw new Error("Application workbench is not activated"); return workbench; },
        });
        workbench.start();
        // A pending destination performs its own selected-authority history read.
        // Do not first resume the checkpoint tab and then resume the intent again.
        await controller.initialize(api.signal, { resumeThread: !restored?.navigationIntent });
        if (restored) controller.restorePresentation(restored);
        if (restored?.navigationIntent) await controller.restoreNavigationIntent(restored.navigationIntent, api.signal);
        else if (restored?.presentationIntent) controller.restorePresentationIntent(restored.presentationIntent);
        stop = controller.start();
      },
      deactivate: async () => {
        await retire();
        return checkpoint;
      },
      createScope: signal => {
        const source = requireController();
        const check = () => { signal.throwIfAborted(); if (controller !== source) throw new Error("Application scope is retired"); };
        const desktop = source.createDesktopScope(signal);
        const composerPresentation = source.createComposerPresentationScope(signal, () => api.contributions);
        return new Proxy({} as ApplicationServiceV1, { get: (_target, key) => {
          check();
          if (key === "desktop") return desktop;
          if (key === "composerPresentation") return composerPresentation;
          if (key === "subscribe") return (listener: () => void) => {
            check(); const off = source.subscribe(() => { if (!signal.aborted) listener(); });
            const stop = () => { signal.removeEventListener("abort", stop); off(); };
            signal.addEventListener("abort", stop, { once: true }); return stop;
          };
          if (key === "invoke") return async <Action extends keyof ApplicationActionMapV1>(action: Action, input: ApplicationActionMapV1[Action]["input"], options?: { signal?: AbortSignal }) => {
            check();
            if (action === "preview.set-visibility") { source.setPreviewVisibility((input as { open: boolean }).open, signal); return; }
            const result = await source.invoke(action, input, { signal: options?.signal ? AbortSignal.any([signal, options.signal]) : signal });
            check(); return result;
          };
          const value = Reflect.get(source, key, source);
          return typeof value === "function" ? (...args: unknown[]) => { check(); return value.apply(source, args); } : value;
        } });
      },
      // Full PTY joins belong to the renderer document, not this serialized
      // provider-removal queue. Repeating retirement still observes web failure.
      dispose: async () => { await retire(); },
    };
  } });
}
