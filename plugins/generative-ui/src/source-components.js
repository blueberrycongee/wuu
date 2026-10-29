const components = {
  Stack: "gap align", Row: "gap align", Flow: "gap align", Grid: "gap columns", Card: "gap title",
  Text: "", Caption: "", Heading: "level", Badge: "tone", Divider: "", Metric: "label value detail", Progress: "label value max",
  Button: "label onClick disabled variant", Input: "label value onChange inputType placeholder required disabled invalid", TextArea: "label value onChange placeholder required disabled invalid",
  Slider: "label value onChange min max step disabled", Checkbox: "label checked onChange required disabled", Select: "label value onChange options required disabled", Radio: "label value onChange options disabled", Segmented: "label value onChange options disabled",
  Disclosure: "title open", Chart: "title chartType points series xLabel yLabel stacked", Table: "title columns rows", Form: "label onSubmit",
  Fragment: "", strong: "", em: "", code: "", br: "",
};
const svgProperties = {
  svg: "viewBox width height label", g: "transform", path: "d pathLength", circle: "cx cy r", ellipse: "cx cy rx ry", rect: "x y width height rx ry", line: "x1 y1 x2 y2", polyline: "points", polygon: "points", text: "x y dx dy textAnchor dominantBaseline fontSize fontWeight", tspan: "x y dx dy textAnchor", title: "", desc: "", defs: "", marker: "markerWidth markerHeight refX refY orient markerUnits viewBox", linearGradient: "x1 y1 x2 y2 gradientUnits gradientTransform", radialGradient: "cx cy r fx fy gradientUnits", stop: "offset stopColor stopOpacity", clipPath: "clipPathUnits",
};
const svgCommon = "id fill stroke strokeWidth opacity fillOpacity strokeOpacity strokeDasharray strokeLinecap strokeLinejoin markerEnd markerStart clipPath".split(" ");
const tones = { accent: "var(--wuu-color-accent,currentColor)", text: "var(--wuu-color-text,currentColor)", muted: "var(--wuu-color-text-muted,currentColor)", surface: "var(--wuu-color-surface-muted,transparent)", border: "var(--genui-border)", success: "var(--wuu-color-success,#137a3d)", warning: "var(--wuu-color-warning,#8a5b10)", danger: "var(--wuu-color-danger,#b42318)", info: "var(--wuu-color-info,#0068da)" };
function check(ok, message) { if (!ok) throw new Error(message); }
function text(value) { check(value == null || ["string", "number", "boolean"].includes(typeof value), "Expected display text"); return value == null ? "" : String(value); }
function color(value, prefix) {
  if (Object.hasOwn(tones, value)) return tones[value];
  if (/^url\(#[A-Za-z][\w-]*\)$/.test(value)) return value.replace("#", `#${prefix}-`);
  check(/^(none|currentColor|transparent|#[\da-fA-F]{3,8}|(?:rgb|hsl)a?\([\d.,%\s+-]+\))$/.test(value), "Use a semantic color, CSS color value, or local SVG reference");
  return value;
}
export function validateSourceTree(nodes) {
  let count = 0;
  const svgIds = new Set();
  function checkKeys(children) {
    const seen = new Set();
    let expanded = 0, index = 0;
    function scan(items, depth) {
      check(depth <= 32, "Rendered view is too deep");
      for (const child of items) {
        check(++expanded <= 2048, "Rendered view is too large");
        if (Array.isArray(child)) { scan(child, depth + 1); continue; }
        const position = index++;
        if (!child || typeof child !== "object") continue;
        const key = child.props?.key ?? child.address ?? position;
        check(typeof key === "string" || typeof key === "number", "Invalid component key");
        check(!seen.has(String(key)), "Repeated components need unique keys"); seen.add(String(key));
      }
    }
    scan(children, 0);
  }
  function visit(node, depth = 0, inSVG = false) {
    check(++count <= 2048 && depth <= 32, "Rendered view is too large");
    if (node == null || typeof node === "boolean") return;
    if (Array.isArray(node)) { check(node.length <= 200, "Too many repeated children"); checkKeys(node); node.forEach(child => visit(child, depth, inSVG)); return; }
    if (typeof node !== "object") { text(node); return; }
    const { tag, props, children } = node;
    check(Object.hasOwn(components, tag) || Object.hasOwn(svgProperties, tag), `Unknown component: ${tag}`);
    check(props && typeof props === "object" && Array.isArray(children), "Invalid component result");
    check(!Object.hasOwn(svgProperties, tag) || tag === "svg" || inSVG, "SVG elements belong inside svg");
    check(!inSVG || tag === "Fragment" || Object.hasOwn(svgProperties, tag), "Use SVG elements inside svg");
    const keys = ["key", ...(Object.hasOwn(svgProperties, tag) ? [...svgCommon, ...svgProperties[tag].split(" ")] : components[tag].split(" "))];
    for (const [key, value] of Object.entries(props)) {
      check(keys.includes(key), `Unknown ${tag} property: ${key}`);
      if (key.startsWith("on")) check(typeof value === "function", `${key} expects a function`);
      else if (["options", "rows", "columns", "points", "series"].includes(key) && tag !== "Grid") check(Array.isArray(value), `${key} expects an array`);
      else check(value == null || ["string", "number", "boolean"].includes(typeof value), `Invalid ${key} value`);
    }
    for (const key of ["disabled", "required", "invalid", "checked", "open", "stacked"]) if (props[key] !== undefined) check(typeof props[key] === "boolean", `${key} expects a boolean`);
    if (tag === "Slider") check(typeof props.value === "number" && props.value >= props.min && props.value <= props.max, "Slider value must be within its range");
    if (tag === "Input" && props.inputType === "number") check(typeof props.value === "number" && Number.isFinite(props.value), "Number input needs a finite number");
    if (tag === "Input" && props.inputType === "date" && props.value !== "") check(typeof props.value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(props.value) && !props.value.startsWith("0000") && Number.isFinite(Date.parse(props.value)) && new Date(props.value).toISOString().slice(0, 10) === props.value, "Date input needs a valid YYYY-MM-DD date");
    if (props.key !== undefined) check(typeof props.key === "string" && props.key.trim() && props.key.length <= 80 || typeof props.key === "number" && Number.isFinite(props.key), "Keys must be short nonempty text or finite numbers");
    if (props.gap !== undefined) check(["none", "small", "medium", "large"].includes(props.gap), "Invalid layout gap");
    if (props.align !== undefined) check(["start", "center", "end", "stretch"].includes(props.align), "Invalid alignment");
    if (tag === "Grid") check(props.columns === undefined || Number.isInteger(props.columns) && props.columns >= 1 && props.columns <= 4, "Grid supports 1–4 columns");
    if (tag === "Heading") check(props.level === undefined || [1, 2, 3].includes(props.level), "Heading level must be 1–3");
    if (tag === "Input") check(props.inputType === undefined || ["text", "number", "date"].includes(props.inputType), "Invalid input type");
    if (tag === "Button") check(typeof props.onClick === "function", "Button needs onClick");
    if (tag === "Form") check(typeof props.onSubmit === "function", "Form needs onSubmit");
    if (tag === "Badge" && props.tone !== undefined) color(props.tone, "validate");
    if (tag === "Button") check(props.variant === undefined || ["primary", "secondary"].includes(props.variant), "Invalid button variant");
    if (tag === "Slider") check(Number.isFinite(props.min) && Number.isFinite(props.max) && props.max > props.min && (props.step === undefined || Number.isFinite(props.step) && props.step > 0), "Slider requires a valid range");
    if (tag === "Progress") check(Number.isFinite(props.value) && props.value >= 0 && props.value <= (props.max ?? 100) && (props.max ?? 100) > 0, "Invalid progress range");
    if (["Input", "TextArea", "Slider", "Checkbox", "Select", "Radio", "Segmented"].includes(tag)) {
      check(typeof props.label === "string" && props.label.trim(), `${tag} needs an accessible label`);
      check(typeof props.onChange === "function", `${tag} needs onChange`);
      if (tag === "Checkbox") check(typeof props.checked === "boolean", "Checkbox expects checked");
      else check(["string", "number", "boolean"].includes(typeof props.value), `${tag} needs a controlled value`);
    }
    if (props.options) { check(props.options.length > 0 && props.options.length <= 20, "Use 1–20 options"); props.options.forEach(option => { check(option && typeof option.label === "string", "An option needs a label"); text(option.value); }); }
    if (tag === "svg") check(typeof props.viewBox === "string" && /^[-\d.e+\s,]+$/.test(props.viewBox) && props.viewBox.trim().split(/[\s,]+/).length === 4 && props.viewBox.trim().split(/[\s,]+/).map(Number).every(Number.isFinite), "svg needs a numeric viewBox");
    if (Object.hasOwn(svgProperties, tag)) for (const [key, value] of Object.entries(props)) {
      if (["fill", "stroke", "stopColor", "markerEnd", "markerStart", "clipPath"].includes(key)) color(String(value), "validate");
      if (key === "id") { check(/^[A-Za-z][\w-]*$/.test(value) && !svgIds.has(value), "SVG ids must be valid and unique within the view"); svgIds.add(value); }
    }
    if (tag === "Table") {
      check(props.columns?.length > 0 && props.columns.length <= 8 && props.rows?.length <= 200, "Invalid table dimensions");
      props.columns.forEach(column => check(column && typeof column.key === "string" && typeof column.label === "string", "Invalid table column"));
      props.rows.forEach(row => { check(Array.isArray(row) && row.length === props.columns.length, "Table row width differs from columns"); row.forEach(text); });
    }
    if (tag === "Chart") {
      check(["bar", "line", "area", "scatter", "pie", "donut"].includes(props.chartType), "Invalid chart type");
      check(Boolean(props.points) !== Boolean(props.series), "Use chart points or series");
      if (props.series) {
        check(!["pie", "donut"].includes(props.chartType), "Circular charts require points");
        const ids = new Set();
        for (const item of props.series) {
          check(typeof item.id === "string" && item.id.trim() && !ids.has(item.id) && typeof item.label === "string", "Series need unique ids and labels"); ids.add(item.id);
          check(item.chartType === undefined || ["bar", "line", "area", "scatter"].includes(item.chartType), "Invalid series type");
        }
        if (!props.series.some(item => (item.chartType ?? props.chartType) === "scatter")) {
          const labels = JSON.stringify(props.series[0]?.points?.map(point => point.label));
          check(props.series.every(item => JSON.stringify(item.points?.map(point => point.label)) === labels), "Category series require the same ordered labels");
        }
      }
      const series = props.series ?? [{ points: props.points }];
      check(series.length > 0 && series.length <= 8, "Invalid chart series");
      for (const item of series) { check(item.points?.length > 0 && item.points.length <= 100, "Invalid chart points"); for (const p of item.points) check(typeof p.label === "string" && Number.isFinite(p.value) && Math.abs(p.value) <= 1e12 && (!["pie", "donut"].includes(props.chartType) || p.value >= 0) && ((item.chartType ?? props.chartType) !== "scatter" || Number.isFinite(p.x)), "Invalid chart point"); }
    }
    checkKeys(children);
    children.forEach(child => visit(child, depth + 1, inSVG || tag === "svg"));
  }
  visit(nodes); return nodes;
}

export function renderSourceTree(nodes, runtime) {
  validateSourceTree(nodes);
  const { h, ui } = runtime;
  function render(list, path = "root", inSVG = false) {
    return list.flat(Infinity).filter(node => node != null && typeof node !== "boolean").map((node, index) => {
      if (typeof node !== "object") return text(node);
      const { tag, props: p, children } = node;
      const key = p.key ?? `${node.address ?? index}`;
      const address = `${path}/${key}`;
      const props = { key, "data-genui-node": address };
      const child = () => render(children, address, inSVG || tag === "svg");
      const event = (handler, args, e, action = false) => runtime.run(handler, args, e, { id: address, label: p.label ?? children.filter(c => typeof c === "string").join(" "), action });
      const disabled = runtime.disabled || Boolean(p.disabled);
      if (tag === "Fragment") return h(runtime.Fragment, { key }, child());
      if (Object.hasOwn(svgProperties, tag)) {
        const svgProps = { ...props };
        for (const [name, value] of Object.entries(p)) {
          if (["key", "label"].includes(name)) continue;
          svgProps[name] = ["fill", "stroke", "stopColor", "markerEnd", "markerStart", "clipPath"].includes(name) ? color(String(value), runtime.instanceId) : name === "id" ? `${runtime.instanceId}-${value}` : value;
        }
        if (tag === "svg") { svgProps.className = "plugin-genui-diagram"; svgProps.role = "img"; svgProps["aria-label"] = p.label ?? runtime.title; }
        return h(tag, svgProps, child());
      }
      if (["Stack", "Row", "Flow", "Grid", "Card"].includes(tag)) return h(tag === "Card" ? ui.Card : "div", { ...props, className: `plugin-genui-layout plugin-genui-${tag.toLowerCase()}`, "data-gap": p.gap ?? "medium", "data-align": p.align ?? "stretch", ...(tag === "Grid" ? { style: { "--genui-columns": p.columns ?? 2 } } : {}) }, p.title ? h("h3", { className: "plugin-genui-card-title" }, p.title) : null, child());
      if (["Text", "Caption"].includes(tag)) return h("p", { ...props, className: `plugin-genui-text${tag === "Caption" ? " plugin-genui-muted" : ""}` }, child());
      if (["strong", "em", "code", "br"].includes(tag)) return h(tag, props, tag === "br" ? undefined : child());
      if (tag === "Heading") return h(`h${(p.level ?? 2) + 1}`, { ...props, className: "plugin-genui-heading", "data-level": p.level ?? 2 }, child());
      if (tag === "Badge") return h("span", { ...props, className: "plugin-genui-badge", style: p.tone ? { color: color(p.tone, runtime.instanceId) } : undefined }, child());
      if (tag === "Divider") return h("hr", { ...props, className: "plugin-genui-divider" });
      if (tag === "Metric") return h("div", { ...props, className: "plugin-genui-metric" }, h("span", { className: "plugin-genui-muted" }, text(p.label)), h("strong", null, text(p.value)), p.detail == null ? null : h("span", { className: "plugin-genui-muted" }, text(p.detail)));
      if (tag === "Progress") return h("label", { ...props, className: "plugin-genui-progress" }, h("span", null, p.label ?? "Progress"), h("progress", { value: p.value, max: p.max ?? 100, "aria-label": p.label ?? "Progress" }), h("span", null, `${Math.round(p.value / (p.max ?? 100) * 100)}%`));
      if (tag === "Button") return h(ui.Button, { ...props, disabled, variant: p.variant ?? "secondary", onClick: e => event(p.onClick, [], e, true) }, p.label ?? child());
      if (tag === "Form") return h("form", { ...props, className: "plugin-genui-form", onSubmit: e => { e.preventDefault(); if (e.currentTarget.reportValidity()) event(p.onSubmit, [], e, true); } }, child(), h(ui.Button, { type: "submit", disabled, variant: "primary" }, p.label ?? "Continue"));
      if (tag === "Disclosure") return h(runtime.Details, { ...props, title: p.title, open: runtime.components[address]?.open ?? p.open ?? false, disabled, change: open => runtime.changeComponent(address, { open }) }, h("div", { className: "plugin-genui-stack plugin-genui-layout" }, child()));
      const control = { ...props, label: p.label, disabled, required: p.required, "aria-invalid": p.invalid || undefined, ref: element => element?.setCustomValidity?.(p.invalid ? "Please check this value." : "") };
      if (tag === "Input" && p.inputType === "number") return h(runtime.NumberInput, { ...control, value: p.value, placeholder: p.placeholder, change: (value, e) => event(p.onChange, [value], e) });
      if (tag === "Input" || tag === "TextArea") return h(tag === "TextArea" ? ui.TextArea : ui.TextInput, { ...control, value: p.value, type: p.inputType ?? "text", placeholder: p.placeholder, maxLength: 4000, onChange: e => event(p.onChange, [e.target.value], e) });
      if (tag === "Checkbox") return h(ui.Checkbox, { ...control, checked: p.checked, onChange: e => event(p.onChange, [e.target.checked], e) });
      if (tag === "Slider") return h("label", { ...props, className: "plugin-ui-field plugin-genui-slider" }, h("span", { className: "plugin-genui-slider-label" }, p.label, h("output", null, p.value)), h("input", { type: "range", min: p.min, max: p.max, step: p.step ?? 1, value: p.value, disabled, onChange: e => event(p.onChange, [e.target.valueAsNumber], e) }), h("span", { className: "plugin-genui-slider-bounds plugin-genui-muted", "aria-hidden": true }, h("span", null, p.min), h("span", null, p.max)));
      if (tag === "Select") return h(ui.Select, { ...control, value: String(p.options.findIndex(o => o.value === p.value)), onChange: e => event(p.onChange, [p.options[Number(e.target.value)].value], e) }, p.options.map((o, i) => h("option", { key: i, value: String(i) }, o.label)));
      if (tag === "Radio" || tag === "Segmented") return h("fieldset", { ...props, disabled, className: `plugin-genui-options${tag === "Segmented" ? " plugin-genui-segmented" : ""}` }, h("legend", null, p.label), h("div", { className: "plugin-genui-options-list" }, p.options.map((o, i) => h("label", { key: i, "data-selected": String(p.value === o.value) }, h("input", { type: "radio", name: `${runtime.instanceId}-${address}`, value: String(i), checked: p.value === o.value, onChange: e => event(p.onChange, [o.value], e) }), o.label))));
      if (tag === "Table" || tag === "Chart") return runtime.data(tag, { ...p, id: address }, address, props);
      throw new Error(`Unsupported component: ${tag}`);
    });
  }
  return h("div", { className: "plugin-genui-program-body" }, render(nodes));
}
