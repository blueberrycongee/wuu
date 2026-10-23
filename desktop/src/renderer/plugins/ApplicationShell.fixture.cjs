  const research = `export function activate(api) {
    const React = api.react;
    api.registerUISlot({id:'evidence',cardinality:'many'});
    api.registerApplicationShell({id:'ui.shell',render: function Shell({application,slots}) {
      const state=React.useSyncExternalStore(application.subscribe,application.getSnapshot);
      return React.createElement('main',{'data-research-shell':true},
        React.createElement('h1',null,'Research shell'),
        React.createElement('p',null,state.conversation.title),
        React.createElement('textarea',{'aria-label':'Research draft',value:state.composer.draftText,onChange:event=>application.invoke('composer.set-draft',{text:event.target.value})}),
        React.createElement('button',{onClick:()=>application.invoke('settings.open',{})},'Settings'),
        state.settings.open&&React.createElement('section',{'data-research-settings':true},'Settings open'),
        React.createElement(slots.Slot,{name:api.pluginId+'/evidence'}));
    }});
  }`;
  const evidence = `export function activate(api) {api.registerUIContribution({id:'evidence',target:'user:research-shell/evidence',render:()=>api.react.createElement('aside',{'data-evidence':true},'Third-party evidence')});}`;
// Extends the shared synthetic desktop backend with explicit composition state.
module.exports = (api) => {
  const listeners = new Set();
  const identities = ["builtin:wuu.default-shell", "user:research-shell", "user:evidence"];
  let selected = identities[0];
  let disabled = false;
  let epoch = 0;
  let failure = "";
  let bridgeFailure = false;
  const plugins = identities.map((id, index) => ({
    id, kind: "plugin", name: id, state: "active", approval_state: "granted", enabled: true,
    runtime_state: "active", fingerprint: `fixture-${index}`, desktop: { entry: "desktop.js", services: index < 2 ? [{ name: "ui.shell", version: "1.0.0" }] : [] },
    provenance: { kind: "plugin", source: index ? "user" : "builtin", scope: "user", plugin_id: id.split(":")[1] },
    requires: index === 2 ? ["research-shell"] : [],
  }));
  const inventory = () => plugins.map(plugin => ({ ...plugin, graph_generation: `fixture-graph-${epoch}`, fingerprint: `${plugin.fingerprint}${failure}`, enabled: !(disabled && plugin.id === selected) }));
  const binding = () => selected ? { plugin_id: selected, generation: inventory().find(plugin => plugin.id === selected).fingerprint, provider_id: "ui.shell" } : null;
  const initialize = api.initialize;
  return {
    ...api,
    initialize: async () => ({ ...await initialize(), application_shell: binding(), extension_inventory: inventory() }),
    reportPluginDesktopActivation: async report => {
      if (bridgeFailure) throw new Error("fixture ACK bridge unavailable");
      await api.reportPluginDesktopActivation(report);
    },
    getPluginConflictPreferences: async () => ({}),
    loadPluginDesktopModule: async ({ id, fingerprint }) => ({ id, fingerprint, digest: "0".repeat(64),
      url: id === identities[0] ? "wuu-builtin:default-shell" : `wuu-plugin://fixture/${id === identities[1] ? (failure === "root" ? "researchBroken" : "research") : (failure === "child" ? "evidenceBroken" : "evidence")}.js` }),
    onServerEvent: listener => { listeners.add(listener); return () => listeners.delete(listener); },
    testShell: async (mode) => {
      bridgeFailure = mode === "bridge-failure";
      if (bridgeFailure) { selected = identities[0]; disabled = false; }
      failure = mode === "broken-root" ? "root" : mode === "broken-child" ? "child" : "";
      if (failure) { selected = identities[1]; disabled = false; }
      epoch++;
      if (mode === "default") { selected = identities[0]; disabled = false; }
      if (mode === "research") { selected = identities[1]; disabled = false; }
      if (mode === "disable") disabled = true;
      for (const listener of listeners) listener({ kind: "notification", workdir: process.env.WUU_RESIZE_E2E_CWD || process.cwd(), message: {
        method: "plugin/inventory/changed", params: { epoch, extension_inventory: inventory(), application_shell: binding() },
      } });
    },
  };
};

module.exports.sources = {
  research, evidence,
  researchBroken: `export function activate(api) {api.registerApplicationShell({id:'ui.shell',render:()=>{throw new Error('fixture root failure')}});}`,
  evidenceBroken: `export function activate(api) {api.registerUIContribution({id:'evidence',target:'user:research-shell/evidence',render:()=>{throw new Error('fixture child failure')}});}`,
};
