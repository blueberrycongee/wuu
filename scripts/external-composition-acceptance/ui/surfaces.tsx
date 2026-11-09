import type { PluginGenerationApi, UISlotRenderProps } from "@wuu/plugin-sdk";

let context: Readonly<Record<string, unknown>> = {};
// Acceptance controls ordinary package state, never a private host API.
export function configureFixture(next: Readonly<Record<string, unknown>>): void { context = next; }

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  api.registerUISlot({ id: "composer-host", cardinality: "one", service: "ui.composer" });
  api.registerUISlot({ id: "navigation-host", cardinality: "one", service: "ui.navigation" });
  api.registerUISlot({ id: "composer.above", cardinality: "many" });
  api.registerUISlot({ id: "sidebar.primary", cardinality: "many" });
  api.registerApplicationShell({ id: "ui.shell", render: ({ slots: { Slot } }) => <main>
    <p data-independent-shell>Independent shell remains available</p>
    <Slot name={`${api.manifestId}/composer-host`} />
    <Slot name={`${api.manifestId}/navigation-host`} />
  </main> });
  api.registerUIContribution({ id: "ui.composer", target: `${api.manifestId}/composer-host`,
    render: ({ slots: { Slot } }: UISlotRenderProps) => <section data-independent-composer><Slot name={`${api.manifestId}/composer.above`} context={context} /></section> });
  api.registerUIContribution({ id: "ui.navigation", target: `${api.manifestId}/navigation-host`,
    render: ({ slots: { Slot } }: UISlotRenderProps) => <aside data-independent-navigation><Slot name={`${api.manifestId}/sidebar.primary`} context={context} /></aside> });
}
