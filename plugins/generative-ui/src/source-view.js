import { compileSource, evaluateSource, validateSourceState } from "./source-language.js";
import { renderSourceTree, validateSourceTree } from "./source-components.js";

export function createSourceView(api, shared) {
  const { createElement: h, useState, useMemo, useRef, useEffect } = api.react;
  const encoder = new TextEncoder();
  let sequence = 0;
  return function SourceUI({ spec, raw, metadata, host, preview, stopped }) {
    const compiled = useMemo(() => compileSource(spec.source, preview), [spec.source, preview]);
    const [snapshot, setSnapshot] = useState({ values: {}, components: {} });
    const [ready, setReady] = useState(false);
    const [notice, setNotice] = useState("");
    const [pending, setPending] = useState(false);
    const current = useRef(snapshot), persistence = useRef({ mounted: true }), busy = useRef(false);
    const hostRef = useRef(host), surface = useRef(null), instance = useRef(null);
    hostRef.current = host;
    if (instance.current === null) instance.current = `source-ui-${++sequence}`;
    function restore(saved) {
      const state = saved ? JSON.parse(saved) : { version: 3, values: {}, components: {} };
      if (state.version !== 3 || !state.values || !state.components) throw new Error("Invalid saved view");
      validateSourceState({ values: state.values, components: state.components });
      for (const record of Object.values(state.components)) {
        if (!record || typeof record !== "object") throw new Error("Invalid saved component state");
        const keys = Object.keys(record).sort().join(",");
        const valid = keys === "open" && typeof record.open === "boolean"
          || keys === "descending,query,sort" && typeof record.query === "string" && record.query.length <= 100 && Number.isInteger(record.sort) && record.sort >= -1 && record.sort < 8 && typeof record.descending === "boolean"
          || keys === "dataOpen,hidden" && typeof record.dataOpen === "boolean" && Array.isArray(record.hidden) && record.hidden.every(id => typeof id === "string");
        if (!valid) throw new Error("Invalid saved component state");
      }
      const result = evaluateSource(compiled, state.values);
      validateSourceTree(result.nodes);
      return { values: result.values, components: state.components };
    }
    useEffect(() => {
      const state = { mounted: true };
      persistence.current = state;
      if (preview) return () => { state.mounted = false; };
      setReady(false);
      void (async () => {
        try {
          const identity = JSON.stringify([metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId, raw]);
          const digest = await crypto.subtle.digest("SHA-256", encoder.encode(identity));
          const key = "ui-v3-" + Array.from(new Uint8Array(digest), n => n.toString(16).padStart(2, "0")).join("");
          await shared.hydrateView(hostRef.current, key, restore, next => { current.current = next; setSnapshot(next); }, state);
        } catch (error) { if (state.mounted) setNotice(`Saved view state is unavailable. ${error.message}`); }
        finally { if (state.mounted) setReady(true); }
      })();
      return () => { state.mounted = false; state.unsubscribe?.(); };
    }, [preview, raw, metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId]);
    const disabled = preview || !ready || pending;
    const canSend = !preview && host.actions?.includes("conversation.composer.submit");
    function commit(next) {
      validateSourceState({ values: next.values, components: next.components });
      current.current = next; setSnapshot(next); setNotice("");
      const storage = persistence.current;
      if (storage.key) void shared.writeSnapshot(hostRef.current, storage.key, { version: 3, ...next }, 16448).catch(() => {
        if (storage.mounted) setNotice("This view could not be saved. Your changes are still available here.");
      });
    }
    const effects = [];
    let evaluation, tree, error;
    function run(handler, args, event, action) {
      if (disabled || busy.current || !event.isTrusted) return false;
      effects.length = 0;
      try {
        const values = evaluation.invoke(handler, args);
        const next = evaluateSource(compiled, values);
        validateSourceTree(next.nodes);
        if (effects.filter(effect => effect.type === "sendMessage").length > 1) throw new Error("Use one conversation continuation per interaction.");
        if (effects.some(effect => effect.type === "sendMessage")) {
          if (!action.action) throw new Error("Continue the conversation from a button or form.");
          if (!canSend) throw new Error(metadata.interaction?.disabledReason || "Conversation continuation is unavailable here.");
          const invalid = [...(surface.current?.querySelectorAll("input,textarea,select") ?? [])].find(control => !control.validity.valid);
          if (invalid) { invalid.reportValidity(); throw new Error("Correct the highlighted value before sending."); }
        }
        commit({ values: next.values, components: current.current.components });
      } catch (caught) { setNotice(caught.message); return false; }
      // Invoke within the original trusted gesture. The host binds this action
      // to its conversation source and checks whether that source is still live.
      if (effects.length) { busy.current = true; setPending(true); }
      const results = effects.map(effect => {
        let result;
        try { result = effect.type === "copy" ? navigator.clipboard.writeText(effect.text) : hostRef.current.invoke("conversation.composer.submit", { text: effect.text, context: { actionId: action.id, label: action.label, values: current.current.values } }); }
        catch (caught) { result = Promise.reject(caught); }
        return Promise.resolve(result).then(() => effect.type === "copy" ? "Copied" : "Sent to this conversation.", caught => caught?.message || "The action could not be completed.");
      });
      if (results.length) void Promise.all(results).then(messages => { if (persistence.current.mounted) setNotice(messages.join(" ")); }).finally(() => { busy.current = false; if (persistence.current.mounted) setPending(false); });
      return true;
    }
    function changeComponent(address, value) {
      if (disabled || busy.current) return false;
      try { commit({ values: evaluation.values, components: { ...current.current.components, [address]: value } }); return true; }
      catch (caught) { setNotice(caught.message); return false; }
    }
    try {
      evaluation = evaluateSource(compiled, snapshot.values, { effect: (type, text) => { if (effects.length >= 16) throw new Error("Too many effects in one interaction"); effects.push({ type, text }); } });
      tree = renderSourceTree(evaluation.nodes, {
        h, Fragment: api.react.Fragment, ui: api.ui, title: spec.title, disabled, run, instanceId: instance.current, components: snapshot.components, changeComponent,
        Details: shared.Details, NumberInput: shared.NumberInput,
        data(tag, node, address, props) {
          if (tag === "Chart" && ["pie", "donut"].includes(node.chartType)) {
            const seen = new Map();
            node.pointIdentities = node.points.map(point => {
              const identity = JSON.stringify([point.label, point.value]), ordinal = seen.get(identity) ?? 0;
              seen.set(identity, ordinal + 1); return JSON.stringify([identity, ordinal]);
            });
          }
          const value = snapshot.components[address] ?? (tag === "Table" ? { query: "", sort: -1, descending: false } : { hidden: [], dataOpen: false });
          return h(tag === "Table" ? shared.Table : shared.Chart, { ...props, node, disabled, value, change: update => changeComponent(address, update(value)) });
        },
      });
    } catch (caught) { error = caught.message; }
    return h("section", { ref: surface, className: "plugin-genui plugin-genui-program plugin-genui-source", "data-wuu-component": "generated-ui", "data-genui-preview": String(Boolean(preview)), "aria-label": spec.title, "aria-busy": !stopped && Boolean(preview || !ready || pending) },
      h("header", { className: "plugin-genui-header" }, h("h2", null, spec.title)),
      error ? h(api.ui.ErrorState, { title: "This view could not be calculated", description: error }) : tree,
      notice ? h("p", { role: "status", className: "plugin-genui-notice" }, notice) : null,
    );
  };
}
