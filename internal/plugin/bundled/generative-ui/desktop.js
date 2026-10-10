// Model data is never spread into React props. The fixed vocabulary below is
// the complete rendering and interaction capability of protocol version 1.
const MIME = "application/vnd.wuu.ui+json";
const MAX_BYTES = 128 * 1024;
const MAX_STATE_BYTES = 16 * 1024;
const ID = /^[A-Za-z][A-Za-z0-9_-]{0,47}$/;
const encoder = new TextEncoder();
const writes = new Map();

function requireValue(condition, message) {
  if (!condition) throw new Error(message);
}
function object(value, keys, required = keys) {
  requireValue(value !== null && typeof value === "object" && !Array.isArray(value), "Expected an object");
  requireValue(Object.keys(value).every(key => keys.includes(key)), "Unknown property");
  requireValue(required.every(key => Object.hasOwn(value, key)), "Missing property");
}
function string(value, max, nonempty = true) {
  requireValue(typeof value === "string" && [...value].length <= max && (!nonempty || value.trim().length > 0), "Invalid text length");
}
function list(value, min, max) {
  requireValue(Array.isArray(value) && value.length >= min && value.length <= max, "Invalid list length");
}
function identifier(value) {
  requireValue(typeof value === "string" && ID.test(value), "Invalid identifier");
}
function number(value) {
  requireValue(typeof value === "number" && Number.isFinite(value) && Math.abs(value) <= 1e12, "Invalid number");
}
function scalar(value) {
  if (typeof value === "string") string(value, 500, false);
  else if (typeof value === "number") number(value);
  else requireValue(value === null || typeof value === "boolean", "Invalid cell value");
}
function unique(values) {
  requireValue(new Set(values).size === values.length, "Duplicate identifier");
}
function fieldValue(field, value) {
  if (field.kind === "text") string(value, 500, false);
  else if (field.kind === "number") number(value);
  else if (field.kind === "checkbox") requireValue(typeof value === "boolean", "Invalid checkbox value");
  else requireValue(field.options.includes(value), "Invalid selection");
}

// Validate historical content again: a stored MIME label is not a trust signal.
export function parseSpec(raw) {
  requireValue(typeof raw === "string" && raw.length <= MAX_BYTES && encoder.encode(raw).length <= MAX_BYTES, "UI exceeds 128 KiB");
  const spec = JSON.parse(raw);
  object(spec, ["version", "title", "fallback", "blocks"]);
  requireValue(spec.version === 1, "Unsupported UI version");
  string(spec.title, 160);
  string(spec.fallback, 8000);
  list(spec.blocks, 1, 16);
  unique(spec.blocks.map(block => block?.id));
  for (const block of spec.blocks) {
    identifier(block?.id);
    if (block.title !== undefined) string(block.title, 160);
    switch (block.type) {
      case "text":
        object(block, ["id", "type", "text"]);
        string(block.text, 4000);
        break;
      case "table":
        object(block, ["id", "type", "title", "columns", "rows"], ["id", "type", "columns", "rows"]);
        list(block.columns, 1, 8);
        unique(block.columns.map(column => column?.key));
        for (const column of block.columns) {
          object(column, ["key", "label"]);
          identifier(column.key);
          string(column.label, 80);
        }
        list(block.rows, 0, 200);
        for (const row of block.rows) {
          list(row, block.columns.length, block.columns.length);
          row.forEach(scalar);
        }
        break;
      case "chart":
        object(block, ["id", "type", "title", "chartType", "xLabel", "yLabel", "points"], ["id", "type", "chartType", "points"]);
        requireValue(block.chartType === "bar" || block.chartType === "line", "Unsupported chart type");
        for (const key of ["xLabel", "yLabel"]) if (block[key] !== undefined) string(block[key], 80);
        list(block.points, 1, 100);
        for (const point of block.points) {
          object(point, ["label", "value"]);
          string(point.label, 80);
          number(point.value);
        }
        break;
      case "form":
        object(block, ["id", "type", "title", "fields"], ["id", "type", "fields"]);
        list(block.fields, 1, 12);
        unique(block.fields.map(field => field?.id));
        for (const field of block.fields) {
          object(field, ["id", "label", "kind", "options", "initial"], ["id", "label", "kind"]);
          identifier(field.id);
          string(field.label, 80);
          requireValue(["text", "number", "select", "checkbox"].includes(field.kind), "Unsupported field kind");
          if (field.kind === "select") {
            list(field.options, 1, 20);
            field.options.forEach(option => string(option, 80));
            unique(field.options);
          } else requireValue(field.options === undefined, "Options require a select field");
          if (field.initial !== undefined) fieldValue(field, field.initial);
        }
        break;
      default: throw new Error("Unsupported UI block");
    }
  }
  return spec;
}

function defaultValues(spec) {
  return Object.fromEntries(spec.blocks.map(block => [block.id,
    block.type === "table" ? { query: "", sort: -1, descending: false }
      : block.type === "chart" ? { range: "all" }
        : block.type === "form" ? Object.fromEntries(block.fields.map(field => [field.id,
          field.initial ?? (field.kind === "checkbox" ? false : field.kind === "select" ? field.options[0] : "")]))
          : {},
  ]));
}
function restoreValues(spec, raw) {
  if (!raw) return defaultValues(spec);
  requireValue(raw.length <= MAX_STATE_BYTES && encoder.encode(raw).length <= MAX_STATE_BYTES, "Saved state is too large");
  const saved = JSON.parse(raw);
  object(saved, ["version", "values"]);
  requireValue(saved.version === 1, "Unsupported saved state");
  object(saved.values, spec.blocks.map(block => block.id));
  for (const block of spec.blocks) {
    const value = saved.values[block.id];
    if (block.type === "table") {
      object(value, ["query", "sort", "descending"]);
      string(value.query, 100, false);
      requireValue(Number.isInteger(value.sort) && value.sort >= -1 && value.sort < block.columns.length && typeof value.descending === "boolean", "Invalid saved sort");
    } else if (block.type === "chart") {
      object(value, ["range"]);
      requireValue(["all", "10", "25"].includes(value.range), "Invalid saved range");
    } else if (block.type === "form") {
      object(value, block.fields.map(field => field.id));
      for (const field of block.fields) {
        // An empty number input is an editable local draft, not a model value.
        if (field.kind !== "number" || value[field.id] !== "") fieldValue(field, value[field.id]);
      }
    } else object(value, []);
  }
  return saved.values;
}
function saveValues(host, key, values) {
  const raw = JSON.stringify({ version: 1, values });
  if (encoder.encode(raw).length > MAX_STATE_BYTES) return Promise.reject(new Error("State exceeds 16 KiB"));
  const pending = (writes.get(key) ?? Promise.resolve()).catch(() => {}).then(() => host.setStorage(key, raw, "user"));
  writes.set(key, pending);
  void pending.finally(() => { if (writes.get(key) === pending) writes.delete(key); }).catch(() => {});
  return pending;
}
function display(value) { return value === null ? "—" : String(value); }

export async function activate(api) {
  const { createElement: h, useState, useEffect, useMemo, useRef } = api.react;
  const ui = api.ui;

  function GeneratedUI({ content, metadata, host }) {
    const parsed = useMemo(() => {
      try { return { spec: parseSpec(content?.text) }; }
      catch (error) { return { error: error.message }; }
    }, [content?.text]);
    return parsed.spec
      ? h(InteractiveUI, { key: `${metadata.threadId}:${metadata.partId}:${metadata.artifactId}:${content.text}`, spec: parsed.spec, raw: content.text, metadata, host })
      : h("div", null, h(ui.ErrorState, { title: "Interactive preview unavailable", description: parsed.error }),
        typeof content?.fallbackText === "string" ? h("p", { className: "plugin-genui-text" }, content.fallbackText) : null);
  }

  function InteractiveUI({ spec, raw, metadata, host }) {
    const initial = useMemo(() => defaultValues(spec), [spec]);
    const [values, setValues] = useState(initial);
    const [ready, setReady] = useState(false);
    const [notice, setNotice] = useState("");
    const current = useRef(initial);
    const persistence = useRef({ key: undefined, mounted: true });
    const hostRef = useRef(host);
    hostRef.current = host;
    useEffect(() => {
      const state = { key: undefined, mounted: true };
      persistence.current = state;
      void (async () => {
        try {
          const identity = JSON.stringify([metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId, raw]);
          const digest = await crypto.subtle.digest("SHA-256", encoder.encode(identity));
          const key = "ui-v1-" + Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
          // Await the previous mount's last write before restoring this instance.
          await writes.get(key)?.catch(() => {});
          const saved = await hostRef.current.getStorage(key, "user");
          if (!state.mounted) return;
          state.key = key;
          current.current = restoreValues(spec, saved);
          setValues(current.current);
        } catch {
          if (state.mounted) setNotice("Saved view state is unavailable. Changes may not be restored.");
        } finally {
          if (state.mounted) setReady(true);
        }
      })();
      return () => { state.mounted = false; };
    }, [spec, raw, metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId]);
    function change(id, value) {
      if (!ready) return;
      const next = { ...current.current, [id]: value };
      current.current = next;
      setValues(next);
      const state = persistence.current;
      if (state.key) void saveValues(hostRef.current, state.key, next).catch(() => {
        if (state.mounted) setNotice("This view could not be saved. Your current changes are still available here.");
      });
    }
    return h("section", { className: "plugin-genui", "data-wuu-component": "generated-ui", "aria-label": spec.title, "aria-busy": !ready },
      h("header", { className: "plugin-genui-header" }, h("h2", null, spec.title), h("span", { className: "plugin-genui-muted" }, "Experimental")),
      notice ? h("p", { role: "status", className: "plugin-genui-muted" }, notice) : null,
      h("fieldset", { disabled: !ready, className: "plugin-genui-blocks" }, spec.blocks.map(block => {
        const props = { key: block.id, block, value: values[block.id], change: value => change(block.id, value) };
        return block.type === "text" ? h("p", { key: block.id, className: "plugin-genui-text" }, block.text)
          : block.type === "table" ? h(TableBlock, props)
            : block.type === "chart" ? h(ChartBlock, props) : h(FormBlock, props);
      })),
      h("details", { className: "plugin-genui-summary" }, h("summary", null, "Text summary"), h("p", { className: "plugin-genui-text" }, spec.fallback)),
    );
  }

  function TableBlock({ block, value, change }) {
    const rows = block.rows.filter(row => row.some(cell => display(cell).toLocaleLowerCase().includes(value.query.toLocaleLowerCase())));
    if (value.sort >= 0) rows.sort((left, right) => {
      const a = left[value.sort], b = right[value.sort];
      const result = typeof a === "number" && typeof b === "number" ? a - b : display(a).localeCompare(display(b));
      return value.descending ? -result : result;
    });
    return h(ui.Section, { title: block.title, "data-genui-block": block.id },
      h(ui.TextInput, { label: "Filter rows", type: "search", value: value.query, maxLength: 100, onChange: event => change({ ...value, query: event.target.value }) }),
      h("div", { className: "plugin-genui-table-scroll", tabIndex: 0, role: "region", "aria-label": block.title ?? "Data table" },
        h("table", null,
          h("thead", null, h("tr", null, block.columns.map((column, index) => h("th", {
            key: column.key, scope: "col", "aria-sort": value.sort === index ? (value.descending ? "descending" : "ascending") : "none",
          }, h(ui.Button, { variant: "ghost", onClick: () => change({ ...value, sort: index, descending: value.sort === index && !value.descending }) }, column.label, value.sort === index ? (value.descending ? " ↓" : " ↑") : " ↕"))))),
          h("tbody", null, rows.map((row, index) => h("tr", { key: index }, row.map((cell, column) => h("td", { key: column }, display(cell))))))),
      ),
      h("p", { className: "plugin-genui-muted", role: "status" }, rows.length ? `${rows.length} of ${block.rows.length} rows` : "No matching rows"),
    );
  }

  function ChartBlock({ block, value, change }) {
    const [active, setActive] = useState(-1);
    const points = value.range === "all" ? block.points : block.points.slice(-Number(value.range));
    const low = Math.min(0, ...points.map(point => point.value));
    const maximum = Math.max(0, ...points.map(point => point.value));
    const high = maximum === low ? low + 1 : maximum;
    const span = high - low;
    const x = index => (index + 0.5) * 520 / points.length;
    const y = number => 180 - (number - low) / span * 180;
    const selected = points[active];
    return h(ui.Section, { title: block.title, "data-genui-block": block.id },
      h(ui.Select, { label: "Show points", value: value.range, onChange: event => { setActive(-1); change({ range: event.target.value }); } },
        h("option", { value: "all" }, "All points"), h("option", { value: "10" }, "Last 10"), h("option", { value: "25" }, "Last 25")),
      h("div", { className: "plugin-genui-plot" },
        // Keep labels outside the scalable SVG so narrow windows and large UI
        // font preferences never shrink the text with the chart geometry.
        h("div", { className: "plugin-genui-y-labels", "aria-hidden": true },
          ...[high, low + span / 2, low].map((value, index) => h("span", { key: index }, formatAxis(value)))),
        h("svg", { className: "plugin-genui-chart", viewBox: "0 0 520 180", preserveAspectRatio: "none", role: "img", "aria-label": `${block.title ?? "Chart"}. ${points.length} points. Use the data table for exact values.` },
          h("title", null, block.title ?? "Chart"),
          ...[0, 0.5, 1].map((fraction, index) => h("line", { key: `grid-${index}`, x1: 0, x2: 520, y1: fraction * 180, y2: fraction * 180, className: "plugin-genui-grid" })),
          h("line", { x1: 0, x2: 520, y1: y(0), y2: y(0), className: "plugin-genui-axis" }),
          block.chartType === "line" ? h("polyline", { points: points.map((point, index) => `${x(index)},${y(point.value)}`).join(" "), className: "plugin-genui-line" }) : null,
          ...points.map((point, index) => h("g", { key: `point-${index}`, onMouseEnter: () => setActive(index), onMouseLeave: () => setActive(-1) },
            block.chartType === "bar" ? h("rect", { x: x(index) - 180 / points.length, width: 360 / points.length, y: Math.min(y(0), y(point.value)), height: Math.max(1, Math.abs(y(point.value) - y(0))), className: "plugin-genui-mark" })
              : h("circle", { cx: x(index), cy: y(point.value), r: points.length > 40 ? 2 : 4, className: "plugin-genui-mark" }),
            h("title", null, `${point.label}: ${point.value}`))),
        ),
        h("div", { className: "plugin-genui-x-labels", "aria-hidden": true },
          h("span", { title: points[0].label }, points[0].label),
          points.length > 1 ? h("span", { title: points.at(-1).label }, points.at(-1).label) : null),
      ),
      h("p", { className: "plugin-genui-chart-caption" }, selected ? `${selected.label}: ${selected.value}` : [block.xLabel, block.yLabel].filter(Boolean).join(" · ") || `${points.length} points`),
      h("details", null, h("summary", null, "Chart data"),
        h("div", { className: "plugin-genui-table-scroll", tabIndex: 0, role: "region", "aria-label": "Chart data" }, h("table", null,
          h("thead", null, h("tr", null, h("th", { scope: "col" }, block.xLabel ?? "Label"), h("th", { scope: "col" }, block.yLabel ?? "Value"))),
          h("tbody", null, points.map((point, index) => h("tr", { key: index }, h("td", null, point.label), h("td", null, point.value))))))),
    );
  }

  function FormBlock({ block, value, change }) {
    const [preview, setPreview] = useState(false);
    const [message, setMessage] = useState("");
    const summary = block.fields.map(field => `${field.label}: ${display(value[field.id])}`).join("\n");
    function update(field, next) { setPreview(false); setMessage(""); change({ ...value, [field.id]: next }); }
    return h(ui.Section, { title: block.title, "data-genui-block": block.id },
      h("form", { className: "plugin-genui-form", onSubmit: event => { event.preventDefault(); setPreview(true); setMessage(""); } },
        block.fields.map(field => field.kind === "checkbox"
          ? h(ui.Checkbox, { key: field.id, label: field.label, checked: value[field.id], onChange: event => update(field, event.target.checked) })
          : field.kind === "select" ? h(ui.Select, { key: field.id, label: field.label, value: value[field.id], onChange: event => update(field, event.target.value) }, field.options.map(option => h("option", { key: option, value: option }, option)))
            : h(ui.TextInput, { key: field.id, label: field.label, type: field.kind === "number" ? "number" : "text", value: value[field.id], maxLength: 500,
              ...(field.kind === "number" ? { min: -1e12, max: 1e12, step: "any" } : {}),
              onChange: event => { const next = field.kind === "number" && event.target.value !== "" ? event.target.valueAsNumber : event.target.value;
                if (typeof next !== "number" || (Number.isFinite(next) && Math.abs(next) <= 1e12)) update(field, next); },
            })),
        h("p", { className: "plugin-genui-muted" }, "Local draft. Preview and copy do not send a message or submit data."),
        h(ui.Row, null, h(ui.Button, { type: "submit" }, "Preview values"), h(ui.Button, { variant: "ghost", onClick: () => { setPreview(false); setMessage(""); change(defaultValues({ blocks: [block] })[block.id]); } }, "Reset")),
      ),
      preview ? h("div", { className: "plugin-genui-form-preview" }, h("pre", null, summary), h(ui.Button, { onClick: async (event) => {
        if (!event.isTrusted) return;
        try { await navigator.clipboard.writeText(summary); setMessage("Copied"); }
        catch { setMessage("Copy unavailable. Select the preview text to copy it."); }
      } }, "Copy values")) : null,
      message ? h("p", { role: "status", className: "plugin-genui-muted" }, message) : null,
    );
  }

  api.registerRenderer({ id: "generated-ui-v1", category: "tool-result", match: MIME, render: GeneratedUI });
  api.registerCSSSnippet({ id: "generated-ui-layout", css: styles });
}

function formatAxis(value) {
  return new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

const styles = `
.plugin-genui { --genui-border:var(--wuu-color-border-subtle, color-mix(in srgb, currentColor 16%, transparent)); display:grid; gap:20px; min-width:0; padding:20px; border:1px solid var(--genui-border); border-radius:var(--wuu-radius-panel, 16px); color:var(--wuu-color-text, currentColor); font-size:var(--wuu-font-size-ui, 14px); line-height:1.5; }
.plugin-genui-header { display:flex; flex-wrap:wrap; gap:8px 16px; align-items:baseline; justify-content:space-between; }
.plugin-genui-header h2 { font:inherit; font-weight:600; margin:0; overflow-wrap:anywhere; }
.plugin-genui-muted { color:var(--wuu-color-text-muted, currentColor); margin:0; }
.plugin-genui-blocks { display:grid; gap:24px; margin:0; padding:0; border:0; min-width:0; }
.plugin-genui-blocks > * { min-width:0; }
.plugin-genui-text { white-space:pre-wrap; overflow-wrap:anywhere; margin:0; }
.plugin-genui-table-scroll { max-width:100%; overflow:auto; border:1px solid var(--genui-border); border-radius:var(--wuu-radius-control, 8px); }
.plugin-genui table { width:100%; border-collapse:collapse; font:inherit; text-align:left; }
.plugin-genui th, .plugin-genui td { padding:8px 12px; border-bottom:1px solid var(--genui-border); max-width:320px; overflow-wrap:anywhere; vertical-align:top; }
.plugin-genui th { font-weight:500; }
.plugin-genui th button { white-space:normal; text-align:left; padding:0; }
.plugin-genui tr:last-child td { border-bottom:0; }
.plugin-genui-plot { display:grid; grid-template-columns:minmax(3ch, max-content) minmax(0, 1fr); gap:8px 12px; padding-top:4px; }
.plugin-genui-y-labels { display:flex; flex-direction:column; justify-content:space-between; text-align:right; line-height:1; height:180px; }
.plugin-genui-y-labels span:first-child { transform:translateY(-50%); }
.plugin-genui-y-labels span:last-child { transform:translateY(50%); }
.plugin-genui-chart { display:block; width:100%; height:180px; overflow:visible; }
.plugin-genui-chart line, .plugin-genui-chart polyline { vector-effect:non-scaling-stroke; }
.plugin-genui-x-labels { grid-column:2; display:flex; justify-content:space-between; gap:12px; min-width:0; }
.plugin-genui-x-labels span { max-width:48%; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.plugin-genui-grid { stroke:var(--genui-border); stroke-dasharray:3 4; }
.plugin-genui-axis { stroke:var(--wuu-color-text-muted, currentColor); }
.plugin-genui-line { fill:none; stroke:var(--wuu-color-accent, currentColor); stroke-width:2.5; }
.plugin-genui-mark { fill:var(--wuu-color-accent, currentColor); }
.plugin-genui-chart-caption { min-height:1.5em; margin:0; overflow-wrap:anywhere; }
.plugin-genui-form { display:grid; gap:12px; }
.plugin-genui-form-preview { display:grid; gap:12px; margin-top:12px; justify-items:start; }
.plugin-genui-form-preview pre { font:inherit; white-space:pre-wrap; overflow-wrap:anywhere; max-width:100%; margin:0; }
.plugin-genui summary { cursor:pointer; color:var(--wuu-color-text-muted, currentColor); }
.plugin-genui details[open] > summary { margin-bottom:12px; }
.plugin-genui :focus-visible { outline:2px solid var(--wuu-color-accent, currentColor); outline-offset:3px; }
@media (max-width:600px) { .plugin-genui { padding:12px; } .plugin-genui th, .plugin-genui td { padding:8px; } }
`;
