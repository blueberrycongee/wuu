// Real selection, composition and error boundaries; only providers are synthetic.
import React from "react";
import { PluginHost } from "../../src/renderer/plugins/PluginHost";
import { ApplicationShellOutlet } from "../../src/renderer/plugins/ApplicationShell";

const host = new PluginHost({ react: React });
let shellMounts = 0;
let alternativeRenders = 0;
const ready = (async () => {
  await host.activateGeneration({ pluginId: "acceptance-shell", generation: "one", register(api) {
    api.registerUISlot({ id: "selected", cardinality: "one", service: "ui.acceptance" });
    api.registerUISlot({ id: "decorated", cardinality: "one" });
    api.registerApplicationShell({ id: "shell", render: function Shell({ slots }) {
      React.useEffect(() => { shellMounts++; }, []);
      return <section data-testid="surface-shell">
        <label>Unrelated shell draft<input data-testid="surface-shell-draft" defaultValue="Keep this draft" /></label>
        <div data-testid="selected-surface-region"><slots.Slot name="acceptance-shell/selected"
          fallback={<p data-testid="forbidden-surface-fallback">Do not replace the selected provider</p>} /></div>
        <div data-testid="wrapped-region"><slots.Slot name="acceptance-shell/decorated"
          fallback={<button data-testid="wrapper-child">Underlying content remains usable</button>} /></div>
      </section>;
    } });
  } });
  await host.activateGeneration({ pluginId: "acceptance-selected", generation: "one", register(api) {
    api.registerUIContribution({ id: "surface", target: "acceptance-shell/selected", render: function Selected() {
      const [broken, setBroken] = React.useState(false);
      if (broken) throw new Error("WUU_ACCEPTANCE_SELECTED_RENDER_FAILURE");
      return <button data-testid="break-selected-surface" onClick={() => setBroken(true)}>Exercise selected provider failure</button>;
    } });
  } });
  await host.activateGeneration({ pluginId: "acceptance-alternative", generation: "one", register(api) {
    api.registerUIContribution({ id: "surface", target: "acceptance-shell/selected", render: () => {
      alternativeRenders++;
      return <p data-testid="forbidden-alternative-provider">Unselected provider</p>;
    } });
  } });
  await host.activateGeneration({ pluginId: "acceptance-wrapper", generation: "one", register(api) {
    api.registerUIContribution({ id: "wrapper", target: "acceptance-shell/decorated", mode: "wrap", render: function Wrapper({ children }) {
      const [broken, setBroken] = React.useState(false);
      if (broken) throw new Error("WUU_ACCEPTANCE_WRAPPER_RENDER_FAILURE");
      return <><button data-testid="break-optional-wrapper" onClick={() => setBroken(true)}>Exercise optional wrapper failure</button>{children}</>;
    } });
  } });
})();

export function SelectedSurfaceScene(): React.ReactNode {
  const [initialized, setInitialized] = React.useState(false);
  React.useEffect(() => { let active = true; void ready.then(() => { if (active) setInitialized(true); }); return () => { active = false; }; }, []);
  return initialized ? <ApplicationShellOutlet host={host}
    binding={{ pluginId: "acceptance-shell", generation: "one", id: "shell" }}
    surfaceBindings={{ "ui.acceptance": { pluginId: "acceptance-selected", generation: "one", id: "surface" } }} /> : null;
}

export function selectedSurfaceEvidence() {
  return { shellMounts, alternativeRenders,
    diagnostics: ["acceptance-selected", "acceptance-wrapper"].flatMap(id => host.getGenerationDiagnostics(id, "one")
      .map(({ pluginId, kind, message }) => ({ pluginId, kind, message }))),
  };
}
